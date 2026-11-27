package transcript

import (
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// lineKind is the top-level "type" of a transcript line.
type lineKind string

// The kinds of line the scanner reads. Every other kind is passed over.
const (
	kindAssistant  lineKind = "assistant"
	kindUser       lineKind = "user"
	kindSystem     lineKind = "system"
	kindAttachment lineKind = "attachment"
	kindTitle      lineKind = "ai-title"
	kindCost       lineKind = "cost-state"
)

// systemSubtype is the "subtype" of a system line.
type systemSubtype string

// The system lines that become records. stop_hook_summary is deliberately
// absent: its hooks are already reported one by one as attachments.
const (
	subtypeTurnDuration    systemSubtype = "turn_duration"
	subtypeCompactBoundary systemSubtype = "compact_boundary"
)

// blockKind is the "type" of one message content block.
type blockKind string

// The content blocks the scanner looks at.
const (
	blockToolUse    blockKind = "tool_use"
	blockToolResult blockKind = "tool_result"
	blockText       blockKind = "text"
)

// Hook attachments are recognised by their type prefix; the outcome is read
// from the rest of the type name.
const (
	hookTypePrefix      = "hook_"
	hookTypeSuccess     = "hook_success"
	hookMarkNonBlocking = "non_blocking_error"
	hookMarkCancelled   = "cancelled"
	hookMarkBlocking    = "blocking_error"
)

// jsonTrue is the raw form of a set boolean member.
const jsonTrue = "true"

// fields holds the raw top-level members of one line that the scanner reads.
// Adding a member means adding it here and to set.
type fields struct {
	kind, sessionID, timestamp, uuid               []byte
	cwd, entrypoint, version, gitBranch            []byte
	title, agentType, skill, plugin                []byte
	message, attachment                            []byte
	toolUseResult, sandboxViolations               []byte
	isMeta, promptSource, isCompactSummary         []byte
	subtype, durationMs, messageCount, compactMeta []byte
	cost, apiDuration, toolDuration                []byte
	linesAdded, linesRemoved                       []byte
}

// set records one top-level member. It is the visit function of walkObject.
func (f *fields) set(key, value []byte) {
	switch string(key) {
	case "type":
		f.kind = value
	case "sessionId":
		f.sessionID = value
	case "timestamp":
		f.timestamp = value
	case "uuid":
		f.uuid = value
	case "cwd":
		f.cwd = value
	case "entrypoint":
		f.entrypoint = value
	case "version":
		f.version = value
	case "gitBranch":
		f.gitBranch = value
	case "aiTitle":
		f.title = value
	case "attributionAgent":
		f.agentType = value
	case "attributionSkill":
		f.skill = value
	case "attributionPlugin":
		f.plugin = value
	case "message":
		f.message = value
	case "attachment":
		f.attachment = value
	case "toolUseResult":
		f.toolUseResult = value
	case "sandboxViolations":
		f.sandboxViolations = value
	case "isMeta":
		f.isMeta = value
	case "promptSource":
		f.promptSource = value
	case "isCompactSummary":
		f.isCompactSummary = value
	case "subtype":
		f.subtype = value
	case "durationMs":
		f.durationMs = value
	case "messageCount":
		f.messageCount = value
	case "compactMetadata":
		f.compactMeta = value
	case "totalCostUSD":
		f.cost = value
	case "totalAPIDuration":
		f.apiDuration = value
	case "totalToolDuration":
		f.toolDuration = value
	case "totalLinesAdded":
		f.linesAdded = value
	case "totalLinesRemoved":
		f.linesRemoved = value
	}
}

// parser turns one line of a known kind into records. It reports false when
// the line could not be read as that kind, which the caller counts as a
// skipped line.
type parser func(s *scanner, f *fields, at time.Time) bool

// parserOf returns the parser of a line kind, or nil for a kind the scanner
// does not read. This switch is the one list of the kinds that are read.
func parserOf(kind []byte) parser {
	switch lineKind(kind) {
	case kindAssistant:
		return (*scanner).assistant
	case kindUser:
		return (*scanner).user
	case kindSystem:
		return (*scanner).system
	case kindAttachment:
		return (*scanner).attachment
	case kindTitle:
		return (*scanner).title
	case kindCost:
		return (*scanner).cost
	}
	return nil
}

// assistantMessage is the part of an assistant message the scanner decodes.
// Text and thinking blocks have no field here, so their content is never
// materialised.
type assistantMessage struct {
	ID         string           `json:"id"`
	Model      string           `json:"model"`
	StopReason string           `json:"stop_reason"`
	Usage      tokenUsage       `json:"usage"`
	Content    []assistantBlock `json:"content"`
}

type tokenUsage struct {
	Input       number `json:"input_tokens"`
	Output      number `json:"output_tokens"`
	CacheRead   number `json:"cache_read_input_tokens"`
	CacheCreate number `json:"cache_creation_input_tokens"`
	Details     struct {
		Thinking number `json:"thinking_tokens"`
	} `json:"output_tokens_details"`
}

type assistantBlock struct {
	Type  blockKind `json:"type"`
	ID    string    `json:"id"`
	Name  string    `json:"name"`
	Input toolInput `json:"input"`
}

// toolInput names the only tool arguments that are decoded. They are used to
// derive a label and are dropped with the line.
type toolInput struct {
	Skill        string `json:"skill"`
	SubagentType string `json:"subagent_type"`
	Command      string `json:"command"`
}

// assistant emits the model call of the line and one tool call per tool_use
// block. The same message ID arrives once per content block, so the model
// call is merged into the one already seen in this batch.
func (s *scanner) assistant(f *fields, at time.Time) bool {
	var message assistantMessage
	if !decode(f.message, &message) || message.ID == "" {
		return false
	}
	key := label(message.ID)
	s.modelCall(key, at, &message, f)
	complete := true
	for i := range message.Content {
		block := &message.Content[i]
		if block.Type != blockToolUse {
			continue
		}
		if block.ID == "" {
			complete = false
			continue
		}
		call := usage.ToolCall{Key: label(block.ID), SessionID: s.session, AgentID: s.in.AgentID, At: at, MessageKey: key}
		classify(&call, block.Name, block.Input)
		s.batch.ToolCalls = append(s.batch.ToolCalls, call)
	}
	return complete
}

// modelCall creates or completes the model call with the given key. The time
// is that of the first line. Token counts are the same on every line of a
// message; taking the largest keeps the result independent of line order if
// a client ever writes a partial count first. The stop reason and the
// attribution appear only on some of the lines, so the latest one wins.
func (s *scanner) modelCall(key string, at time.Time, message *assistantMessage, f *fields) {
	i, seen := s.calls[key]
	if !seen {
		i = len(s.batch.ModelCalls)
		s.calls[key] = i
		s.batch.ModelCalls = append(s.batch.ModelCalls, usage.ModelCall{Key: key, SessionID: s.session, AgentID: s.in.AgentID, At: at})
	}
	call := &s.batch.ModelCalls[i]
	if call.At.IsZero() {
		call.At = at
	}
	if call.Model == "" {
		call.Model = label(message.Model)
	}
	call.Input = max(call.Input, int64(message.Usage.Input))
	call.Output = max(call.Output, int64(message.Usage.Output))
	call.CacheRead = max(call.CacheRead, int64(message.Usage.CacheRead))
	call.CacheCreate = max(call.CacheCreate, int64(message.Usage.CacheCreate))
	call.Thinking = max(call.Thinking, int64(message.Usage.Details.Thinking))
	if message.StopReason != "" {
		call.StopReason = label(message.StopReason)
	}
	if v := jsonString(f.skill); v != "" {
		call.Skill = label(v)
	}
	if v := jsonString(f.plugin); v != "" {
		call.Plugin = label(v)
	}
}

// userBlock is one content block of a user message. The content of a tool
// result has no field here and is never materialised.
type userBlock struct {
	Type      blockKind `json:"type"`
	ToolUseID string    `json:"tool_use_id"`
	IsError   bool      `json:"is_error"`
	Text      string    `json:"text"`
}

// user emits tool results, or a prompt when the line carries none. The text
// of a prompt is decoded only to be measured.
func (s *scanner) user(f *fields, at time.Time) bool {
	content := member(f.message, "content")
	switch {
	case isString(content):
		if !s.promptLine(f) {
			return true
		}
		var text string
		if !decode(content, &text) {
			return false
		}
		return s.prompt(f, at, utf8.RuneCountInString(text))
	case len(content) > 0 && content[0] == '[':
		var blocks []userBlock
		if !decode(content, &blocks) {
			return false
		}
		return s.userBlocks(f, at, blocks)
	}
	return false
}

func (s *scanner) userBlocks(f *fields, at time.Time, blocks []userBlock) bool {
	complete, results, chars, hasText := true, 0, 0, false
	for i := range blocks {
		block := &blocks[i]
		switch block.Type {
		case blockToolResult:
			results++
			if block.ToolUseID == "" {
				complete = false
				continue
			}
			s.batch.ToolResults = append(s.batch.ToolResults, s.toolResult(f, at, block))
		case blockText:
			hasText = true
			chars += utf8.RuneCountInString(block.Text)
		}
	}
	if results == 0 && hasText && s.promptLine(f) {
		return s.prompt(f, at, chars) && complete
	}
	return complete
}

// toolResult builds the result of one tool_result block. Interruption, the
// reported duration and the sandbox violations are properties of the line.
// toolUseResult is an object for most tools but a string or a list for some,
// and only the object form carries the two members read here.
func (s *scanner) toolResult(f *fields, at time.Time, block *userBlock) usage.ToolResult {
	result := usage.ToolResult{
		Key:               label(block.ToolUseID),
		SessionID:         s.session,
		At:                at,
		IsError:           block.IsError,
		SandboxViolations: arrayLen(f.sandboxViolations),
	}
	walkObject(f.toolUseResult, func(key, value []byte) {
		switch string(key) {
		case "interrupted":
			result.Interrupted = string(value) == jsonTrue
		case "durationMs":
			result.DurationMs = count(value)
		}
	})
	return result
}

// promptSourceSystem marks a user line the client wrote on its own, such as
// a notification or a queued reminder.
const promptSourceSystem = "system"

// promptLine reports whether a user line may be a prompt at all: only the
// root transcript has user-authored turns, and meta lines, client-written
// turns and compaction summaries are not started by a person.
func (s *scanner) promptLine(f *fields) bool {
	return s.in.AgentID == "" && string(f.isMeta) != jsonTrue &&
		string(f.isCompactSummary) != jsonTrue && jsonString(f.promptSource) != promptSourceSystem
}

func (s *scanner) prompt(f *fields, at time.Time, chars int) bool {
	key := jsonString(f.uuid)
	if key == "" {
		return false
	}
	s.batch.Prompts = append(s.batch.Prompts, usage.Prompt{
		Key:       label(key),
		SessionID: s.session,
		AgentID:   s.in.AgentID,
		At:        at,
		Source:    label(jsonString(f.promptSource)),
		Chars:     chars,
	})
	return true
}

// system emits a turn end or a compaction. Other subtypes carry nothing the
// pipeline counts.
func (s *scanner) system(f *fields, at time.Time) bool {
	switch systemSubtype(stringBody(f.subtype)) {
	case subtypeTurnDuration:
		// TurnEnd has no agent, so a marker in a sub-agent transcript
		// would be booked on the root session.
		if s.in.AgentID != "" {
			return true
		}
		key := jsonString(f.uuid)
		if key == "" {
			return false
		}
		s.batch.Turns = append(s.batch.Turns, usage.TurnEnd{
			Key:        label(key),
			SessionID:  s.session,
			At:         at,
			DurationMs: count(f.durationMs),
			Messages:   clampInt(count(f.messageCount)),
		})
	case subtypeCompactBoundary:
		key := jsonString(f.uuid)
		if key == "" {
			return false
		}
		compaction := usage.Compaction{Key: label(key), SessionID: s.session, AgentID: s.in.AgentID, At: at}
		walkObject(f.compactMeta, func(key, value []byte) {
			switch string(key) {
			case "trigger":
				compaction.Trigger = label(jsonString(value))
			case "preTokens":
				compaction.PreTokens = count(value)
			}
		})
		s.batch.Compactions = append(s.batch.Compactions, compaction)
	}
	return true
}

// hookAttachment is the part of a hook attachment the scanner decodes. The
// command and its output have no field here.
type hookAttachment struct {
	Event      string `json:"hookEvent"`
	Name       string `json:"hookName"`
	ToolUseID  string `json:"toolUseID"`
	ExitCode   number `json:"exitCode"`
	DurationMs number `json:"durationMs"`
}

// attachment emits a hook run. Most attachments are not hooks, so the type
// is looked up before anything is decoded.
func (s *scanner) attachment(f *fields, at time.Time) bool {
	kind := jsonString(member(f.attachment, "type"))
	if !strings.HasPrefix(kind, hookTypePrefix) {
		return true
	}
	key := jsonString(f.uuid)
	var hook hookAttachment
	if key == "" || !decode(f.attachment, &hook) {
		return false
	}
	s.batch.Hooks = append(s.batch.Hooks, usage.HookRun{
		Key:        label(key),
		SessionID:  s.session,
		AgentID:    s.in.AgentID,
		At:         at,
		Event:      label(hook.Event),
		Name:       label(hook.Name),
		ToolUseKey: label(hook.ToolUseID),
		Status:     hookStatus(kind),
		ExitCode:   clampInt(int64(hook.ExitCode)),
		DurationMs: max(int64(hook.DurationMs), 0),
	})
	return true
}

// hookStatus maps a hook attachment type to an outcome. The non-blocking
// check comes first because its marker contains the blocking one.
func hookStatus(kind string) usage.HookStatus {
	switch {
	case kind == hookTypeSuccess:
		return usage.HookSuccess
	case strings.Contains(kind, hookMarkNonBlocking), strings.Contains(kind, hookMarkCancelled):
		return usage.HookError
	case strings.Contains(kind, hookMarkBlocking):
		return usage.HookBlocked
	}
	return usage.HookUnknown
}

// title remembers the generated session title.
func (s *scanner) title(f *fields, _ time.Time) bool {
	latest(&s.rawTitle, f.title)
	return true
}

// cost records the running cost of the session. A line without a numeric
// total is not an error and not a zero: the cost stays unknown. The line has
// no time of its own, so it takes the latest one seen before it.
func (s *scanner) cost(f *fields, at time.Time) bool {
	if s.in.AgentID != "" {
		return true
	}
	total, ok := float(f.cost)
	if !ok {
		return true
	}
	if at.IsZero() {
		at = s.last
	}
	s.batch.Cost = &usage.CostState{
		SessionID:      s.session,
		At:             at,
		TotalCostUSD:   total,
		APIDurationMs:  count(f.apiDuration),
		ToolDurationMs: count(f.toolDuration),
		LinesAdded:     count(f.linesAdded),
		LinesRemoved:   count(f.linesRemoved),
	}
	return true
}

// clampInt narrows n to an int that fits every platform.
func clampInt(n int64) int {
	return int(min(max(n, math.MinInt32), math.MaxInt32))
}

// label cuts a stored name to usage.MaxLabelLen runes.
func label(s string) string {
	return cutRunes(s, usage.MaxLabelLen)
}

// cutRunes returns the first n runes of s.
func cutRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	seen := 0
	for i := range s {
		if seen == n {
			return s[:i]
		}
		seen++
	}
	return s
}
