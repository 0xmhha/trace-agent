package timeline

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// lineType is the "type" of a transcript line.
type lineType string

// Transcript line types that produce timeline items.
const (
	lineUser       lineType = "user"
	lineAssistant  lineType = "assistant"
	lineAttachment lineType = "attachment"
	lineSystem     lineType = "system"
)

// blockType is the "type" of a content block inside a message.
type blockType string

// Content block types the timeline reads.
const (
	blockText             blockType = "text"
	blockThinking         blockType = "thinking"
	blockRedactedThinking blockType = "redacted_thinking"
	blockToolUse          blockType = "tool_use"
	blockToolResult       blockType = "tool_result"
)

// systemSubtype is the "subtype" of a system line.
type systemSubtype string

// System line subtypes shown on the timeline.
const (
	subtypeTurnDuration    systemSubtype = "turn_duration"
	subtypeCompactBoundary systemSubtype = "compact_boundary"
)

// Hook attachment types. The status of a hook run is read from the type name.
const (
	hookTypePrefix       = "hook_"
	hookTypeSuccess      = "hook_success"
	hookMarkNonBlocking  = "non_blocking_error"
	hookMarkCancelled    = "cancelled"
	hookMarkBlockingFail = "blocking_error"
)

// Labels of system items. They are user-facing text.
const (
	labelTurnEnd         = "턴 종료"
	labelTurnEndSeconds  = "턴 종료 · %.1f초"
	labelCompact         = "맥락 압축"
	labelCompactTrigger  = "맥락 압축 (%s)"
	millisecondsInSecond = 1000
)

// textSeparator joins the text blocks of one message or tool result.
const textSeparator = "\n"

// rawLine holds the fields of a transcript line the timeline reads. Every
// field stays raw until the line type is known: a line type the timeline
// does not show may use the same field name with another value type and
// must not count as unreadable.
type rawLine struct {
	Type            json.RawMessage `json:"type"`
	UUID            json.RawMessage `json:"uuid"`
	SessionID       json.RawMessage `json:"sessionId"`
	AgentID         json.RawMessage `json:"agentId"`
	Timestamp       json.RawMessage `json:"timestamp"`
	IsMeta          json.RawMessage `json:"isMeta"`
	Subtype         json.RawMessage `json:"subtype"`
	DurationMs      json.RawMessage `json:"durationMs"`
	Message         json.RawMessage `json:"message"`
	ToolUseResult   json.RawMessage `json:"toolUseResult"`
	Attachment      json.RawMessage `json:"attachment"`
	CompactMetadata json.RawMessage `json:"compactMetadata"`
}

type messageBody struct {
	ID      string          `json:"id"`
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"`
	Usage   *callUsage      `json:"usage"`
}

type callUsage struct {
	Input       int64 `json:"input_tokens"`
	Output      int64 `json:"output_tokens"`
	CacheRead   int64 `json:"cache_read_input_tokens"`
	CacheCreate int64 `json:"cache_creation_input_tokens"`
	Details     *struct {
		Thinking *int64 `json:"thinking_tokens"`
	} `json:"output_tokens_details"`
}

// contentBlock is one element of message.content. The thinking text is left
// out on purpose: it is never decoded, so it cannot reach an item.
type contentBlock struct {
	Type      blockType       `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

type hookAttachment struct {
	Type       string  `json:"type"`
	HookEvent  string  `json:"hookEvent"`
	HookName   string  `json:"hookName"`
	ExitCode   float64 `json:"exitCode"`
	DurationMs float64 `json:"durationMs"`
}

// converter turns transcript lines of one stream into timeline items.
type converter struct {
	sessionID string
	agentID   string
}

// lineContext is what every item of one line shares.
type lineContext struct {
	base    string
	at      time.Time
	agentID string
}

func (c lineContext) item(kind usage.TimelineKind, index int) usage.TimelineItem {
	return usage.TimelineItem{
		ID:      c.base + ":" + strconv.Itoa(index),
		At:      c.at,
		Kind:    kind,
		AgentID: c.agentID,
	}
}

// line converts one transcript line that starts at byte offset start. ok is
// false when the line cannot be parsed. A line that parses but is of no
// interest to the timeline returns no items and ok set.
func (c converter) line(data []byte, start int64, st *state) (items []usage.TimelineItem, ok bool) {
	if blank(data) {
		return nil, true
	}
	var raw rawLine
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, false
	}
	// The file name owns the stream. A line that names another session was
	// copied from a different transcript and is not part of this one.
	if sid := rawString(raw.SessionID); c.sessionID != "" && sid != "" && sid != c.sessionID {
		return nil, true
	}
	ctx := lineContext{
		base:    itemBase(rawString(raw.UUID), start),
		at:      parseTime(rawString(raw.Timestamp)),
		agentID: c.agentID,
	}
	if id := rawString(raw.AgentID); id != "" {
		ctx.agentID = label(id)
	}

	var err error
	switch lineType(rawString(raw.Type)) {
	case lineUser:
		items, err = userItems(ctx, raw, st)
	case lineAssistant:
		items, err = assistantItems(ctx, raw, st)
	case lineAttachment:
		items, err = hookItems(ctx, raw)
	case lineSystem:
		items = systemItems(ctx, raw)
	}
	if err != nil {
		return nil, false
	}
	return items, true
}

// userItems returns the prompt or the tool results of a user line.
func userItems(ctx lineContext, raw rawLine, st *state) ([]usage.TimelineItem, error) {
	if string(raw.IsMeta) == "true" {
		return nil, nil
	}
	_, blocks, err := decodeMessage(raw.Message)
	if err != nil {
		return nil, err
	}
	results := 0
	for _, b := range blocks {
		if b.Type == blockToolResult {
			results++
		}
	}
	if results == 0 {
		return promptFromBlocks(ctx, blocks), nil
	}
	// toolUseResult describes the line as a whole. With several results on
	// one line it cannot be attributed to one of them.
	var reported int64
	if results == 1 {
		reported = reportedDuration(raw.ToolUseResult)
	}
	items := make([]usage.TimelineItem, 0, results)
	for i, b := range blocks {
		if b.Type == blockToolResult {
			items = append(items, resultItem(ctx, i, b, reported, st))
		}
	}
	return items, nil
}

func promptItem(ctx lineContext, index int, text string) usage.TimelineItem {
	it := ctx.item(usage.ItemPrompt, index)
	it.Text, it.Truncated, it.Masked = displayText(text)
	return it
}

// promptFromBlocks joins the text blocks of a user message into one prompt.
// A message without any text block is not a prompt.
func promptFromBlocks(ctx lineContext, blocks []contentBlock) []usage.TimelineItem {
	var joined cappedText
	first := -1
	for i, b := range blocks {
		if b.Type != blockText {
			continue
		}
		if first < 0 {
			first = i
		}
		joined.add(b.Text)
	}
	if first < 0 {
		return nil
	}
	return []usage.TimelineItem{promptItem(ctx, first, joined.String())}
}

func resultItem(ctx lineContext, index int, b contentBlock, reported int64, st *state) usage.TimelineItem {
	it := ctx.item(usage.ItemToolResult, index)
	it.Text, it.Truncated, it.Masked = displayText(resultText(b.Content))
	tool := &usage.TimelineTool{UseID: label(b.ToolUseID), IsError: b.IsError}
	call, known := st.closeTool(b.ToolUseID)
	if known {
		tool.Name, tool.Category = call.Name, call.Category
	}
	switch at := unixMillis(ctx.at); {
	case reported > 0:
		tool.DurationMs = &reported
	case known && call.AtMs > 0 && at >= call.AtMs:
		elapsed := at - call.AtMs
		tool.DurationMs = &elapsed
	}
	it.Tool = tool
	return it
}

// assistantItems returns one item per content block of an assistant line.
func assistantItems(ctx lineContext, raw rawLine, st *state) ([]usage.TimelineItem, error) {
	msg, blocks, err := decodeMessage(raw.Message)
	if err != nil {
		return nil, err
	}
	// The same message ID repeats on one line per content block. Without a
	// usable ID every line is taken as a call of its own.
	callID := msg.ID
	if len(callID) > maxIDLen {
		callID = ""
	}
	if callID == "" || callID != st.call.ID {
		st.call = callProgress{ID: callID}
	}
	model := label(msg.Model)

	var items []usage.TimelineItem
	for i, b := range blocks {
		switch b.Type {
		case blockThinking, blockRedactedThinking:
			it := ctx.item(usage.ItemThinking, i)
			it.Model = model
			if thinking, ok := msg.thinkingTokens(); ok && !st.call.Thinking {
				it.Tokens = &usage.TokenTotals{Output: thinking}
				st.call.Thinking = true
			}
			items = append(items, it)
		case blockText:
			if b.Text == "" {
				continue
			}
			it := ctx.item(usage.ItemAssistant, i)
			it.Text, it.Truncated, it.Masked = displayText(b.Text)
			msg.attachUsage(&it, model, st)
			items = append(items, it)
		case blockToolUse:
			it := ctx.item(usage.ItemToolCall, i)
			name := label(b.Name)
			tool := &usage.TimelineTool{UseID: label(b.ID), Name: name, Category: categoryOf(name)}
			tool.Input, it.Truncated, it.Masked = maskInput(b.Input)
			it.Tool = tool
			msg.attachUsage(&it, model, st)
			st.openTool(openCall{ID: b.ID, Name: name, Category: tool.Category, AtMs: unixMillis(ctx.at)})
			items = append(items, it)
		}
	}
	return items, nil
}

// attachUsage puts the model and the token usage of the call on its first
// assistant or tool_call item. Later items of the same call stay bare, so a
// reader that sums the items does not count a call twice.
func (m messageBody) attachUsage(it *usage.TimelineItem, model string, st *state) {
	if st.call.Usage {
		return
	}
	st.call.Usage = true
	it.Model = model
	if m.Usage == nil {
		return
	}
	it.Tokens = &usage.TokenTotals{
		Input:       m.Usage.Input,
		Output:      m.Usage.Output,
		CacheRead:   m.Usage.CacheRead,
		CacheCreate: m.Usage.CacheCreate,
	}
}

// thinkingTokens returns the thinking tokens of the call. ok is false when
// the transcript does not report them, which is not the same as zero.
func (m messageBody) thinkingTokens() (int64, bool) {
	if m.Usage == nil || m.Usage.Details == nil || m.Usage.Details.Thinking == nil {
		return 0, false
	}
	return *m.Usage.Details.Thinking, true
}

// hookItems returns the hook run of an attachment line, if it is one.
func hookItems(ctx lineContext, raw rawLine) ([]usage.TimelineItem, error) {
	// Other attachment kinds are not shown and may shape the same fields
	// differently, so the hook fields are decoded only once the type says hook.
	if !strings.HasPrefix(attachmentType(raw.Attachment), hookTypePrefix) {
		return nil, nil
	}
	var hook hookAttachment
	if err := json.Unmarshal(raw.Attachment, &hook); err != nil {
		return nil, fmt.Errorf("decode hook attachment: %w", err)
	}
	it := ctx.item(usage.ItemHook, 0)
	it.Hook = &usage.TimelineHook{
		Event:      label(hook.HookEvent),
		Name:       label(hook.HookName),
		Status:     hookStatus(hook.Type),
		ExitCode:   int(clampFloat(hook.ExitCode, math.MinInt32, math.MaxInt32)),
		DurationMs: int64(clampFloat(hook.DurationMs, 0, maxDurationMs)),
	}
	return []usage.TimelineItem{it}, nil
}

// attachmentType returns attachment.type, or an empty string when the
// attachment is not an object with a string type.
func attachmentType(attachment json.RawMessage) string {
	var probe struct {
		Type json.RawMessage `json:"type"`
	}
	if len(attachment) == 0 || attachment[0] != '{' {
		return ""
	}
	if err := json.Unmarshal(attachment, &probe); err != nil {
		return ""
	}
	return rawString(probe.Type)
}

// hookStatus classifies a hook run by its attachment type. "non_blocking_error"
// contains "blocking_error", so the order of the checks matters.
func hookStatus(attachmentType string) usage.HookStatus {
	switch {
	case attachmentType == hookTypeSuccess:
		return usage.HookSuccess
	case strings.Contains(attachmentType, hookMarkNonBlocking), strings.Contains(attachmentType, hookMarkCancelled):
		return usage.HookError
	case strings.Contains(attachmentType, hookMarkBlockingFail):
		return usage.HookBlocked
	}
	return usage.HookUnknown
}

// systemItems returns the turn end or the compaction marker of a system line.
func systemItems(ctx lineContext, raw rawLine) []usage.TimelineItem {
	var text string
	switch systemSubtype(rawString(raw.Subtype)) {
	case subtypeTurnDuration:
		text = labelTurnEnd
		if ms, ok := rawFloat(raw.DurationMs); ok {
			text = fmt.Sprintf(labelTurnEndSeconds, clampFloat(ms, 0, maxDurationMs)/millisecondsInSecond)
		}
	case subtypeCompactBoundary:
		text = labelCompact
		var meta struct {
			Trigger json.RawMessage `json:"trigger"`
		}
		if len(raw.CompactMetadata) > 0 && json.Unmarshal(raw.CompactMetadata, &meta) == nil {
			if trigger := rawString(meta.Trigger); trigger != "" {
				text = fmt.Sprintf(labelCompactTrigger, label(trigger))
			}
		}
	default:
		return nil
	}
	it := ctx.item(usage.ItemSystem, 0)
	it.Label = text
	return []usage.TimelineItem{it}
}

// decodeMessage decodes the message of a user or assistant line. Content
// that is a plain string is returned as one text block, so callers handle a
// single shape. blocks is nil when the content is absent.
func decodeMessage(raw json.RawMessage) (messageBody, []contentBlock, error) {
	var msg messageBody
	if len(raw) == 0 {
		return msg, nil, nil
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		return msg, nil, fmt.Errorf("decode message: %w", err)
	}
	if text, isString := rawStringValue(msg.Content); isString {
		return msg, []contentBlock{{Type: blockText, Text: text}}, nil
	}
	if len(msg.Content) == 0 || msg.Content[0] != '[' {
		return msg, nil, nil
	}
	var blocks []contentBlock
	if err := json.Unmarshal(msg.Content, &blocks); err != nil {
		return msg, nil, fmt.Errorf("decode message content: %w", err)
	}
	return msg, blocks, nil
}

// resultText returns the text of a tool result, whose content is a string or
// a list of blocks. Blocks that are not text, such as images, add nothing.
func resultText(content json.RawMessage) string {
	if text, isString := rawStringValue(content); isString {
		return text
	}
	if len(content) == 0 || content[0] != '[' {
		return ""
	}
	var parts []struct {
		Type json.RawMessage `json:"type"`
		Text json.RawMessage `json:"text"`
	}
	if err := json.Unmarshal(content, &parts); err != nil {
		return ""
	}
	var joined cappedText
	for _, p := range parts {
		if blockType(rawString(p.Type)) == blockText {
			joined.add(rawString(p.Text))
		}
	}
	return joined.String()
}

// reportedDuration returns toolUseResult.durationMs, or zero when the line
// does not report one. toolUseResult is an object, a list or a string
// depending on the tool.
func reportedDuration(toolUseResult json.RawMessage) int64 {
	if len(toolUseResult) == 0 || toolUseResult[0] != '{' {
		return 0
	}
	var result struct {
		DurationMs json.RawMessage `json:"durationMs"`
	}
	if err := json.Unmarshal(toolUseResult, &result); err != nil {
		return 0
	}
	ms, ok := rawFloat(result.DurationMs)
	if !ok {
		return 0
	}
	return int64(clampFloat(ms, 0, maxDurationMs))
}

// categoryOf classifies a tool by its name, following the rule of the design
// document: MCP tools by prefix, Skill, Agent and Task by name, the rest
// built in.
func categoryOf(name string) usage.ToolCategory {
	switch {
	case strings.HasPrefix(name, usage.MCPToolPrefix):
		return usage.ToolMCP
	case name == usage.ToolNameSkill:
		return usage.ToolSkill
	case name == usage.ToolNameAgent, name == usage.ToolNameTask:
		return usage.ToolAgent
	}
	return usage.ToolBuiltin
}

func knownCategory(category usage.ToolCategory) bool {
	switch category {
	case usage.ToolBuiltin, usage.ToolMCP, usage.ToolSkill, usage.ToolAgent:
		return true
	}
	return false
}

// offsetIDPrefix marks an item ID built from a byte offset instead of a uuid.
const offsetIDPrefix = "@"

// itemBase returns the stream-unique prefix of the item IDs of one line: the
// line's uuid, or its byte offset when the line has no usable one. A uuid
// that looks like an offset ID is not used, so the two kinds cannot collide.
func itemBase(uuid string, start int64) string {
	if uuid != "" && len(uuid) <= maxIDLen && !strings.HasPrefix(uuid, offsetIDPrefix) {
		return uuid
	}
	return offsetIDPrefix + strconv.FormatInt(start, 10)
}
