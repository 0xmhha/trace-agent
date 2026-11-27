package usage

import "time"

// ToolCategory classifies a tool call by where the tool comes from.
type ToolCategory string

// Tool categories.
const (
	ToolBuiltin ToolCategory = "builtin"
	ToolMCP     ToolCategory = "mcp"
	ToolSkill   ToolCategory = "skill"
	ToolAgent   ToolCategory = "agent"
)

// HookStatus is the outcome of one hook execution.
type HookStatus string

// Hook outcomes. HookUnknown is used when the transcript reports a hook run
// whose outcome the parser cannot classify.
const (
	HookSuccess HookStatus = "success"
	HookError   HookStatus = "error"
	HookBlocked HookStatus = "blocked"
	HookUnknown HookStatus = "unknown"
)

// Tool names with a dedicated category or a derived Target.
const (
	ToolNameSkill = "Skill"
	ToolNameAgent = "Agent"
	ToolNameTask  = "Task"
	ToolNameBash  = "Bash"
	// MCPToolPrefix starts every MCP tool name: mcp__<server>__<tool>.
	MCPToolPrefix = "mcp__"
	// MCPPluginPrefix starts the server part of a plugin-provided MCP tool:
	// mcp__plugin_<plugin>_<server>__<tool>.
	MCPPluginPrefix = "plugin_"
)

// ScanInput tells the scanner which transcript stream it is reading.
// SessionID and AgentID are derived from the file name by the caller; lines
// that name a different session (history copied from another transcript) are
// passed over and counted in Batch.Foreign. An empty SessionID disables that
// filter. Base is the absolute offset the reader is positioned at. MaxBytes
// stops the scan at the first line boundary at or after that many bytes;
// zero or less means until EOF.
type ScanInput struct {
	SessionID string
	AgentID   string
	Base      int64
	MaxBytes  int64
}

// SessionPatch carries the latest non-empty session attributes seen in a batch.
// SessionID and AgentID identify the transcript stream; AgentID is empty for
// the root transcript and set for a sub-agent transcript. FirstAt and LastAt
// are the earliest and latest line timestamps of the batch and are zero when
// no line in the batch carried one; a zero time never overwrites a stored one.
// Title is passed through Mask, and every label is cut to MaxLabelLen.
type SessionPatch struct {
	SessionID     string
	AgentID       string
	AgentType     string
	ProjectPath   string
	Title         string
	Entrypoint    string
	ClientVersion string
	GitBranch     string
	FirstAt       time.Time
	LastAt        time.Time
}

// ModelCall is one model response. Key is the response message ID; the same
// message appears on several transcript lines and must be stored once.
type ModelCall struct {
	Key         string
	SessionID   string
	AgentID     string
	At          time.Time
	Model       string
	Input       int64
	Output      int64
	CacheRead   int64
	CacheCreate int64
	Thinking    int64
	StopReason  string
	Skill       string
	Plugin      string
}

// ToolCall is one tool invocation proposed by the model. Key is the tool-use
// ID and MessageKey the Key of the ModelCall that proposed it. Target holds only a low-sensitivity label: the program name for Bash
// and nothing for other tools. Arguments are never stored.
type ToolCall struct {
	Key          string
	SessionID    string
	AgentID      string
	At           time.Time
	Name         string
	Category     ToolCategory
	MCPServer    string
	MCPTool      string
	Plugin       string
	Skill        string
	SubagentType string
	Target       string
	MessageKey   string
}

// ToolResult closes the ToolCall with the same SessionID and Key; the agent
// is the one of that call. DurationMs is zero when the transcript does not
// report one; readers then derive it from the two timestamps.
type ToolResult struct {
	Key               string
	SessionID         string
	At                time.Time
	IsError           bool
	Interrupted       bool
	DurationMs        int64
	SandboxViolations int
}

// Prompt is one user-authored turn start. Chars is the text length; the text
// itself is not stored.
type Prompt struct {
	Key       string
	SessionID string
	AgentID   string
	At        time.Time
	Source    string
	Chars     int
}

// HookRun is one hook execution reported in the transcript.
type HookRun struct {
	Key        string
	SessionID  string
	AgentID    string
	At         time.Time
	Event      string
	Name       string
	ToolUseKey string
	Status     HookStatus
	ExitCode   int
	DurationMs int64
}

// TurnEnd is a turn-duration marker written when the client finishes a turn.
// Only the root transcript carries them.
type TurnEnd struct {
	Key        string
	SessionID  string
	At         time.Time
	DurationMs int64
	Messages   int
}

// Compaction marks a context compaction boundary.
type Compaction struct {
	Key       string
	SessionID string
	AgentID   string
	At        time.Time
	Trigger   string
	PreTokens int64
}

// CostState is the client-reported running cost of a session. The latest
// value per session wins. The pipeline never estimates cost from tokens: a
// CostState exists only for a line that carries a numeric total cost, so a
// missing cost stays unknown instead of becoming zero. At is the line
// timestamp, or the latest timestamp seen earlier in the batch when the line
// has none. Only the root transcript contributes one.
type CostState struct {
	SessionID      string
	At             time.Time
	TotalCostUSD   float64
	APIDurationMs  int64
	ToolDurationMs int64
	LinesAdded     int64
	LinesRemoved   int64
}

// Batch is the result of scanning a contiguous range of one transcript file.
// Every record carries a natural key, so storing the same record twice is
// idempotent. BaseOffset is the absolute offset the scan started at and
// NextOffset the offset after the last complete line. Skipped counts lines
// passed over because they were malformed, or oversized and of a record type
// the scanner reads; an oversized line of a type it does not read is not a
// loss and is not counted. Foreign counts lines that belong to another
// session.
type Batch struct {
	BaseOffset  int64
	Session     SessionPatch
	ModelCalls  []ModelCall
	ToolCalls   []ToolCall
	ToolResults []ToolResult
	Prompts     []Prompt
	Hooks       []HookRun
	Turns       []TurnEnd
	Compactions []Compaction
	Cost        *CostState
	Lines       int
	Skipped     int
	Foreign     int
	NextOffset  int64
}

// FileRef identifies one transcript file under the watched root. Path is
// relative to the root and uses forward slashes. ProjectKey is the first
// path segment.
type FileRef struct {
	Path       string
	ProjectKey string
	Size       int64
	ModTime    time.Time
}

// FileCursor is the stored read position of one transcript file.
type FileCursor struct {
	Offset  int64
	Size    int64
	ModTime time.Time
	Skipped int64
}

// ExtKind is the shape of an extension event.
type ExtKind string

// Extension event kinds.
const (
	ExtSpan   ExtKind = "span"
	ExtLog    ExtKind = "log"
	ExtMetric ExtKind = "metric"
)

// ExtStatus is the outcome reported by an extension event.
type ExtStatus string

// Extension event outcomes.
const (
	ExtOK      ExtStatus = "ok"
	ExtError   ExtStatus = "error"
	ExtUnknown ExtStatus = "unknown"
)

// ExtLevel is the severity of an extension event.
type ExtLevel string

// Extension event severities.
const (
	ExtDebug ExtLevel = "debug"
	ExtInfo  ExtLevel = "info"
	ExtWarn  ExtLevel = "warn"
	ExtFail  ExtLevel = "error"
)

// ExtEvent is one validated, masked event sent by a third-party tool. ID is
// assigned by the receiver. SessionID and ToolUseID link the event to a
// session and a tool call when the sender knows them.
type ExtEvent struct {
	ID           string            `json:"id"`
	Source       string            `json:"source"`
	Kind         ExtKind           `json:"kind"`
	Name         string            `json:"name"`
	At           time.Time         `json:"at"`
	ReceivedAt   time.Time         `json:"receivedAt"`
	DurationMs   *float64          `json:"durationMs,omitempty"`
	Status       ExtStatus         `json:"status"`
	Level        ExtLevel          `json:"level"`
	SessionID    string            `json:"sessionId,omitempty"`
	ToolUseID    string            `json:"toolUseId,omitempty"`
	TraceID      string            `json:"traceId,omitempty"`
	SpanID       string            `json:"spanId,omitempty"`
	ParentSpanID string            `json:"parentSpanId,omitempty"`
	Message      string            `json:"message,omitempty"`
	Attributes   map[string]string `json:"attributes,omitempty"`
	Value        *float64          `json:"value,omitempty"`
	Unit         string            `json:"unit,omitempty"`
}
