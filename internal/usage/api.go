package usage

import (
	"context"
	"errors"
	"time"
)

// Service method names accepted by the UI dispatch, with the JSON request
// and response type of each:
//
//	status               -            Status
//	overview             Query        Overview
//	sessions             SessionQuery SessionPage
//	session              SessionRef   SessionDetail
//	timeline             TimelineRequest TimelinePage
//	tools                Query        Tools
//	projects             Query        Projects
//	insights             Query        InsightReport
//	extensions           Query        ExtOverview
//	extEvents            ExtQuery     ExtPage
//	setExcluded          SetExcluded  Status
//	setWindowDays        SetWindow    Status
//	rescan               -            Status
//	clearData            -            Status
//	setExtensionEnabled  SetEnabled   Status
//
// A method without a request type accepts "{}" or an empty body. rescan
// re-reads every tracked file from the start and keeps stored rows. clearData
// withdraws consent and then deletes all usage rows, extension events,
// rejection counters and file cursors; other settings are kept, and nothing
// is collected again until the user grants consent anew. Without consent,
// session and timeline return ErrNoConsent: the original transcripts are not
// read.
//
// Every slice in a response is non-nil, so that it encodes as [] and never
// as null. The service enforces this with NonNil before encoding.
const (
	MethodStatus       = "status"
	MethodOverview     = "overview"
	MethodSessions     = "sessions"
	MethodSession      = "session"
	MethodTimeline     = "timeline"
	MethodTools        = "tools"
	MethodProjects     = "projects"
	MethodInsights     = "insights"
	MethodExtensions   = "extensions"
	MethodExtEvents    = "extEvents"
	MethodSetExcluded  = "setExcluded"
	MethodSetWindow    = "setWindowDays"
	MethodRescan       = "rescan"
	MethodClearData    = "clearData"
	MethodSetExtension = "setExtensionEnabled"
)

// Limits shared by every implementation.
const (
	// DefaultWindowDays bounds the first scan to recent transcripts.
	DefaultWindowDays = 30
	// MaxWindowDays is the widest history window the UI may request.
	MaxWindowDays = 365
	// MaxLineBytes is the longest transcript line the scanner parses. Longer
	// lines are skipped, never treated as the end of the file. The longest
	// line seen in a sample of 91,000 real lines was 0.2 MiB.
	MaxLineBytes = 16 << 20
	// MaxPageSize caps list responses.
	MaxPageSize = 200
	// DefaultPageSize is used when a request leaves the limit at zero.
	DefaultPageSize = 50
	// MaxTimelineText caps one timeline text field after masking.
	MaxTimelineText = 16 << 10
	// MaxMaskInput caps the text handed to Mask for one timeline field. Text
	// is cut to this length first, masked, then cut to MaxTimelineText, so a
	// huge tool result never costs more than a bounded masking pass.
	MaxMaskInput = 2 * MaxTimelineText
	// MaxLabelLen caps every label stored in the usage database: titles,
	// paths, branch names, model IDs, tool, skill, plugin, server and hook
	// names. Longer values are cut at this many characters.
	MaxLabelLen = 200
	// ScanChunkBytes is the amount of one transcript the service hands to a
	// single Scan and Apply, so that a large file never holds one long write
	// transaction.
	ScanChunkBytes = 8 << 20
	// ExtRetentionDays is how long extension events are kept.
	ExtRetentionDays = 30
	// MaxExtEvents caps the stored extension events; the oldest go first.
	MaxExtEvents = 200_000
	// MaxTimelineReads is the number of timeline pages read concurrently.
	MaxTimelineReads = 2
)

// CoverageState tells the reader how much of the requested range was read.
// "No data" and "could not read" must never look the same.
type CoverageState string

// Coverage states.
const (
	CoverageComplete CoverageState = "complete"
	CoveragePartial  CoverageState = "partial"
	CoverageScanning CoverageState = "scanning"
	CoverageDisabled CoverageState = "disabled"
)

// Coverage reasons reported with a partial state. Excluding a project is the
// user's choice and is not a reason.
const (
	ReasonUnreadableFiles = "unreadable_files"
	ReasonSkippedLines    = "skipped_lines"
	ReasonOutsideWindow   = "outside_window"
)

// Coverage accompanies every range aggregate: Overview, Tools, Projects,
// SessionPage and InsightReport. The store leaves the field zero in those
// responses; the service fills it from Store.Coverage for the request's query
// and then overlays the scanning and disabled states. SessionDetail and
// TimelinePage report Source and Skipped instead, and the extension
// responses report Rejected.
type Coverage struct {
	State           CoverageState `json:"state"`
	FilesTracked    int           `json:"filesTracked"`
	FilesUnreadable int           `json:"filesUnreadable"`
	LinesSkipped    int64         `json:"linesSkipped"`
	WindowFrom      time.Time     `json:"windowFrom"`
	LastScanAt      time.Time     `json:"lastScanAt"`
	Reasons         []string      `json:"reasons"`
}

// Range is a half-open time interval [From, Until).
type Range struct {
	From  time.Time `json:"from"`
	Until time.Time `json:"until"`
}

// Query selects the data behind an aggregate view. Project is a project key
// or empty for all projects. BinHours is the series bucket width; zero lets
// the store choose 1 for ranges up to two days and 24 otherwise.
type Query struct {
	Range    Range  `json:"range"`
	Project  string `json:"project,omitempty"`
	BinHours int    `json:"binHours,omitempty"`
	// Locale selects the language of generated text (insight titles,
	// details and recommendations). Empty or unknown means LocaleDefault.
	Locale Locale `json:"locale,omitempty"`
}

// Locale is a user-interface language code.
type Locale string

// Supported locales. Adding one means a column in every message table that
// produces user-facing text (internal/usage/insight, desktop dialogs) and a
// row in the frontend LOCALES list.
const (
	LocaleEnglish Locale = "en"
	LocaleKorean  Locale = "ko"
	// LocaleDefault is used for an empty or unsupported code.
	LocaleDefault = LocaleEnglish
)

// Locales lists the supported locales, default first.
func Locales() []Locale { return []Locale{LocaleEnglish, LocaleKorean} }

// NormalizeLocale returns l when it is supported and LocaleDefault otherwise.
func NormalizeLocale(l Locale) Locale {
	for _, known := range Locales() {
		if l == known {
			return l
		}
	}
	return LocaleDefault
}

// TokenTotals splits token counts the way the model API reports them.
type TokenTotals struct {
	Input       int64 `json:"input"`
	Output      int64 `json:"output"`
	CacheRead   int64 `json:"cacheRead"`
	CacheCreate int64 `json:"cacheCreate"`
}

// Bin is one bucket of the overview time series.
type Bin struct {
	From       time.Time   `json:"from"`
	Tokens     TokenTotals `json:"tokens"`
	Sessions   int64       `json:"sessions"`
	Prompts    int64       `json:"prompts"`
	ToolCalls  int64       `json:"toolCalls"`
	ToolErrors int64       `json:"toolErrors"`
}

// ModelUsage aggregates model calls by model ID.
type ModelUsage struct {
	Model    string      `json:"model"`
	Calls    int64       `json:"calls"`
	Sessions int64       `json:"sessions"`
	Tokens   TokenTotals `json:"tokens"`
}

// ProjectUsage aggregates activity by project.
type ProjectUsage struct {
	Key      string      `json:"key"`
	Path     string      `json:"path"`
	Sessions int64       `json:"sessions"`
	Prompts  int64       `json:"prompts"`
	Tokens   TokenTotals `json:"tokens"`
	LastAt   time.Time   `json:"lastAt"`
	Excluded bool        `json:"excluded"`
}

// ToolUsage aggregates tool calls by tool name. Durations are nil when no
// call in the range has a closed result.
type ToolUsage struct {
	Name      string       `json:"name"`
	Category  ToolCategory `json:"category"`
	MCPServer string       `json:"mcpServer,omitempty"`
	Plugin    string       `json:"plugin,omitempty"`
	Calls     int64        `json:"calls"`
	Errors    int64        `json:"errors"`
	Pending   int64        `json:"pending"`
	Sessions  int64        `json:"sessions"`
	P50Ms     *float64     `json:"p50Ms"`
	P95Ms     *float64     `json:"p95Ms"`
}

// GroupUsage aggregates calls by a grouping label such as an MCP server, a
// skill, a plugin, a sub-agent type or a Bash program. Tokens is set only for
// groups the transcript attributes model calls to (skills and plugins).
type GroupUsage struct {
	Name     string       `json:"name"`
	Calls    int64        `json:"calls"`
	Errors   int64        `json:"errors"`
	Sessions int64        `json:"sessions"`
	Tokens   *TokenTotals `json:"tokens,omitempty"`
	AvgMs    *float64     `json:"avgMs"`
}

// HookUsage aggregates hook runs by event and name.
type HookUsage struct {
	Event    string   `json:"event"`
	Name     string   `json:"name"`
	Runs     int64    `json:"runs"`
	Failures int64    `json:"failures"`
	AvgMs    *float64 `json:"avgMs"`
	MaxMs    *float64 `json:"maxMs"`
}

// HeatCell counts prompts by local weekday (0 is Sunday) and hour.
type HeatCell struct {
	Weekday int   `json:"weekday"`
	Hour    int   `json:"hour"`
	Prompts int64 `json:"prompts"`
}

// Overview is the dashboard payload. CostUSD is the sum of client-reported
// session costs and is nil when no session in the range reported one;
// CostSessions is the number of sessions that did. CacheHitRatio is
// cacheRead / (input + cacheRead + cacheCreate) and nil when the denominator
// is zero.
type Overview struct {
	Coverage      Coverage       `json:"coverage"`
	Range         Range          `json:"range"`
	Sessions      int64          `json:"sessions"`
	Prompts       int64          `json:"prompts"`
	ModelCalls    int64          `json:"modelCalls"`
	ToolCalls     int64          `json:"toolCalls"`
	ToolErrors    int64          `json:"toolErrors"`
	Subagents     int64          `json:"subagents"`
	Compactions   int64          `json:"compactions"`
	ActiveDays    int64          `json:"activeDays"`
	Tokens        TokenTotals    `json:"tokens"`
	CostUSD       *float64       `json:"costUsd"`
	CostSessions  int64          `json:"costSessions"`
	CacheHitRatio *float64       `json:"cacheHitRatio"`
	Series        []Bin          `json:"series"`
	Models        []ModelUsage   `json:"models"`
	Projects      []ProjectUsage `json:"projects"`
	Tools         []ToolUsage    `json:"tools"`
	Heatmap       []HeatCell     `json:"heatmap"`
}

// Tools is the tools-and-extensions payload.
type Tools struct {
	Coverage   Coverage     `json:"coverage"`
	Range      Range        `json:"range"`
	Tools      []ToolUsage  `json:"tools"`
	MCPServers []GroupUsage `json:"mcpServers"`
	Skills     []GroupUsage `json:"skills"`
	Plugins    []GroupUsage `json:"plugins"`
	Subagents  []GroupUsage `json:"subagents"`
	Programs   []GroupUsage `json:"programs"`
	Hooks      []HookUsage  `json:"hooks"`
}

// Projects is the per-project payload.
type Projects struct {
	Coverage Coverage       `json:"coverage"`
	Items    []ProjectUsage `json:"items"`
}

// SessionSort orders the session list.
type SessionSort string

// Session list orders.
const (
	SortLast   SessionSort = "last"
	SortTokens SessionSort = "tokens"
	SortErrors SessionSort = "errors"
	SortCost   SessionSort = "cost"
)

// SessionQuery selects a page of sessions. Search matches a substring of the
// title or project path.
type SessionQuery struct {
	Range   Range       `json:"range"`
	Project string      `json:"project,omitempty"`
	Sort    SessionSort `json:"sort,omitempty"`
	Search  string      `json:"search,omitempty"`
	Limit   int         `json:"limit,omitempty"`
	Cursor  string      `json:"cursor,omitempty"`
}

// SessionRow summarizes one root session including its sub-agents.
type SessionRow struct {
	ID          string      `json:"id"`
	Title       string      `json:"title"`
	ProjectKey  string      `json:"projectKey"`
	ProjectPath string      `json:"projectPath"`
	Entrypoint  string      `json:"entrypoint"`
	GitBranch   string      `json:"gitBranch"`
	StartedAt   time.Time   `json:"startedAt"`
	LastAt      time.Time   `json:"lastAt"`
	Prompts     int64       `json:"prompts"`
	ModelCalls  int64       `json:"modelCalls"`
	ToolCalls   int64       `json:"toolCalls"`
	ToolErrors  int64       `json:"toolErrors"`
	Subagents   int64       `json:"subagents"`
	Compactions int64       `json:"compactions"`
	Tokens      TokenTotals `json:"tokens"`
	CostUSD     *float64    `json:"costUsd"`
	Models      []string    `json:"models"`
}

// SessionPage is one page of the session list.
type SessionPage struct {
	Coverage   Coverage     `json:"coverage"`
	Items      []SessionRow `json:"items"`
	Total      int          `json:"total"`
	NextCursor string       `json:"nextCursor,omitempty"`
}

// AgentRow summarizes one transcript stream of a session. AgentID is empty
// for the root agent.
type AgentRow struct {
	AgentID    string      `json:"agentId"`
	AgentType  string      `json:"agentType"`
	ModelCalls int64       `json:"modelCalls"`
	ToolCalls  int64       `json:"toolCalls"`
	ToolErrors int64       `json:"toolErrors"`
	Tokens     TokenTotals `json:"tokens"`
	StartedAt  time.Time   `json:"startedAt"`
	LastAt     time.Time   `json:"lastAt"`
}

// TurnPoint is one model call of the root agent, in order, for the token-flow
// chart. Context is input + cacheRead + cacheCreate of that call.
type TurnPoint struct {
	At        time.Time `json:"at"`
	Model     string    `json:"model"`
	Context   int64     `json:"context"`
	Output    int64     `json:"output"`
	ToolCalls int64     `json:"toolCalls"`
}

// SourceState tells whether the original transcript can still be read.
type SourceState string

// Transcript availability.
const (
	SourceAvailable  SourceState = "available"
	SourceMissing    SourceState = "missing"
	SourceUnreadable SourceState = "unreadable"
)

// SessionRef identifies one session in a request.
type SessionRef struct {
	SessionID string `json:"sessionId"`
}

// SessionDetail is the session page payload. Turns holds at most
// MaxTurnPoints points; TurnsTruncated reports that later calls were dropped.
type SessionDetail struct {
	Session        SessionRow   `json:"session"`
	Source         SourceState  `json:"source"`
	Models         []ModelUsage `json:"models"`
	Tools          []ToolUsage  `json:"tools"`
	MCPServers     []GroupUsage `json:"mcpServers"`
	Skills         []GroupUsage `json:"skills"`
	Plugins        []GroupUsage `json:"plugins"`
	Hooks          []HookUsage  `json:"hooks"`
	Agents         []AgentRow   `json:"agents"`
	Turns          []TurnPoint  `json:"turns"`
	TurnsTruncated bool         `json:"turnsTruncated"`
	ExtSources     []GroupUsage `json:"extSources"`
}

// MaxTurnPoints caps SessionDetail.Turns.
const MaxTurnPoints = 2000

// TimelineKind is the type of one timeline item.
type TimelineKind string

// Timeline item kinds.
const (
	ItemPrompt     TimelineKind = "prompt"
	ItemAssistant  TimelineKind = "assistant"
	ItemThinking   TimelineKind = "thinking"
	ItemToolCall   TimelineKind = "tool_call"
	ItemToolResult TimelineKind = "tool_result"
	ItemHook       TimelineKind = "hook"
	ItemSystem     TimelineKind = "system"
	ItemExt        TimelineKind = "ext"
)

// TimelineRequest selects a page of one transcript stream. AgentID is empty
// for the root transcript.
type TimelineRequest struct {
	SessionID string `json:"sessionId"`
	AgentID   string `json:"agentId,omitempty"`
	Cursor    string `json:"cursor,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

// TimelineTool describes a tool call or its result. Input is the masked,
// truncated JSON of the call arguments and is empty on a result item.
type TimelineTool struct {
	UseID      string       `json:"useId"`
	Name       string       `json:"name"`
	Category   ToolCategory `json:"category"`
	Input      string       `json:"input,omitempty"`
	IsError    bool         `json:"isError"`
	DurationMs *int64       `json:"durationMs,omitempty"`
}

// TimelineHook describes one hook run.
type TimelineHook struct {
	Event      string     `json:"event"`
	Name       string     `json:"name"`
	Status     HookStatus `json:"status"`
	ExitCode   int        `json:"exitCode"`
	DurationMs int64      `json:"durationMs"`
}

// TimelineItem is one display row of a session timeline. Text is masked and
// capped at MaxTimelineText; Masked counts the replaced secrets and Truncated
// reports that the original was longer. An assistant or tool_call item that
// is the first block of a model call carries that call's usage in Tokens and
// its model in Model. A thinking item carries no text; its Tokens holds the
// call's thinking tokens in Output and zero elsewhere. A tool_result item
// repeats the Name and Category of its call and reports DurationMs; the
// reader keeps the open calls it needs for that in the page cursor.
type TimelineItem struct {
	ID        string        `json:"id"`
	At        time.Time     `json:"at"`
	Kind      TimelineKind  `json:"kind"`
	AgentID   string        `json:"agentId,omitempty"`
	Text      string        `json:"text,omitempty"`
	Truncated bool          `json:"truncated,omitempty"`
	Masked    int           `json:"masked,omitempty"`
	Label     string        `json:"label,omitempty"`
	Model     string        `json:"model,omitempty"`
	Tokens    *TokenTotals  `json:"tokens,omitempty"`
	Tool      *TimelineTool `json:"tool,omitempty"`
	Hook      *TimelineHook `json:"hook,omitempty"`
	Ext       *ExtEvent     `json:"ext,omitempty"`
	// System describes a system item in structured form so that the page can
	// word it in the user's language. Label carries an English fallback.
	System *TimelineSystem `json:"system,omitempty"`
}

// SystemKind names what a system timeline item marks.
type SystemKind string

// System item kinds.
const (
	SystemTurnEnd    SystemKind = "turn_end"
	SystemCompaction SystemKind = "compaction"
)

// TimelineSystem is the structured form of a system item. DurationMs is set
// for a turn end that reported one; Trigger for a compaction that named one.
type TimelineSystem struct {
	Kind       SystemKind `json:"kind"`
	DurationMs *int64     `json:"durationMs,omitempty"`
	Trigger    string     `json:"trigger,omitempty"`
}

// TimelinePage is one page of a session timeline. Skipped counts lines in the
// page's byte range that could not be parsed.
type TimelinePage struct {
	Source     SourceState    `json:"source"`
	Items      []TimelineItem `json:"items"`
	NextCursor string         `json:"nextCursor,omitempty"`
	Skipped    int            `json:"skipped"`
}

// Severity ranks an insight.
type Severity string

// Insight severities.
const (
	SeverityInfo     Severity = "info"
	SeverityWarn     Severity = "warn"
	SeverityCritical Severity = "critical"
)

// InsightCategory groups insights on the page.
type InsightCategory string

// Insight categories.
const (
	CategoryCost        InsightCategory = "cost"
	CategoryReliability InsightCategory = "reliability"
	CategoryWorkflow    InsightCategory = "workflow"
	CategoryExtension   InsightCategory = "extension"
)

// RecommendationKind names what a recommendation asks the user to adopt.
type RecommendationKind string

// Recommendation kinds.
const (
	RecommendSkill   RecommendationKind = "skill"
	RecommendPlugin  RecommendationKind = "plugin"
	RecommendMCP     RecommendationKind = "mcp"
	RecommendHook    RecommendationKind = "hook"
	RecommendSetting RecommendationKind = "setting"
	RecommendHabit   RecommendationKind = "habit"
)

// Evidence is one number behind an insight. Of is the denominator when Value
// is a count out of a total.
type Evidence struct {
	Label string   `json:"label"`
	Value float64  `json:"value"`
	Unit  string   `json:"unit,omitempty"`
	Of    *float64 `json:"of,omitempty"`
}

// Recommendation is a concrete next step attached to an insight.
type Recommendation struct {
	Kind   RecommendationKind `json:"kind"`
	Title  string             `json:"title"`
	Action string             `json:"action"`
	Target string             `json:"target,omitempty"`
}

// Insight is one finding produced by a local rule.
type Insight struct {
	ID             string          `json:"id"`
	RuleID         string          `json:"ruleId"`
	Severity       Severity        `json:"severity"`
	Category       InsightCategory `json:"category"`
	Title          string          `json:"title"`
	Detail         string          `json:"detail"`
	Evidence       []Evidence      `json:"evidence"`
	SessionIDs     []string        `json:"sessionIds,omitempty"`
	Recommendation *Recommendation `json:"recommendation,omitempty"`
}

// InsightInput is everything a rule may read. Sessions holds the heaviest
// sessions of the range by tokens, at most MaxInsightSessions. ExtRejected is
// the total number of refused extension inputs, including those that could
// not be attributed to a sender.
type InsightInput struct {
	// Locale selects the language of the generated text.
	Locale      Locale
	Range       Range
	Overview    Overview
	Tools       Tools
	Sessions    []SessionRow
	Ext         []ExtSourceRow
	ExtRejected int64
}

// MaxInsightSessions caps InsightInput.Sessions.
const MaxInsightSessions = 200

// InsightReport is the insights page payload. A rule either fires, passes, or
// is skipped because the range holds too little data to judge; the three are
// counted separately.
type InsightReport struct {
	Coverage     Coverage  `json:"coverage"`
	GeneratedAt  time.Time `json:"generatedAt"`
	Items        []Insight `json:"items"`
	RulesFired   int       `json:"rulesFired"`
	RulesPassed  int       `json:"rulesPassed"`
	RulesSkipped int       `json:"rulesSkipped"`
}

// ExtSourceRow aggregates extension events of a range by sender. Linked
// counts events that carry a session ID and Unlinked those that do not.
// Rejected is the sender's cumulative count of refused inputs; refusals are
// not timestamped, so it does not follow the range.
type ExtSourceRow struct {
	Source   string    `json:"source"`
	Events   int64     `json:"events"`
	Errors   int64     `json:"errors"`
	Linked   int64     `json:"linked"`
	Unlinked int64     `json:"unlinked"`
	Rejected int64     `json:"rejected"`
	LastAt   time.Time `json:"lastAt"`
}

// ExtEndpoint describes the local event endpoint without its credential.
type ExtEndpoint struct {
	Enabled bool   `json:"enabled"`
	URL     string `json:"url"`
}

// ExtOverview is the extension page payload.
type ExtOverview struct {
	Endpoint ExtEndpoint    `json:"endpoint"`
	Sources  []ExtSourceRow `json:"sources"`
	Rejected int64          `json:"rejected"`
}

// ExtQuery selects a page of extension events. A zero Range bound is
// unbounded on that side, so a zero Range selects all time; this applies to
// ExtQuery only.
type ExtQuery struct {
	Range     Range    `json:"range"`
	Source    string   `json:"source,omitempty"`
	SessionID string   `json:"sessionId,omitempty"`
	ToolUseID string   `json:"toolUseId,omitempty"`
	Level     ExtLevel `json:"level,omitempty"`
	Limit     int      `json:"limit,omitempty"`
	Cursor    string   `json:"cursor,omitempty"`
}

// ExtPage is one page of extension events, newest first.
type ExtPage struct {
	Items      []ExtEvent `json:"items"`
	Total      int        `json:"total"`
	NextCursor string     `json:"nextCursor,omitempty"`
}

// Status is the watch state shown on the settings page and polled by every
// page. Coverage describes the whole collection window, not a page's range.
// Scanning is true from the moment a full pass becomes necessary (first run,
// consent granted, rescan, clearData, a wider window, a re-included project)
// until that pass completes; aggregate responses report CoverageScanning for
// the same period. DataVersion increases only when stored rows change, so
// pages refetch only when it moves.
type Status struct {
	Consent     bool        `json:"consent"`
	Root        string      `json:"root"`
	WindowDays  int         `json:"windowDays"`
	Scanning    bool        `json:"scanning"`
	FilesTotal  int         `json:"filesTotal"`
	FilesDone   int         `json:"filesDone"`
	Coverage    Coverage    `json:"coverage"`
	Excluded    []string    `json:"excluded"`
	Extension   ExtEndpoint `json:"extension"`
	DataVersion int64       `json:"dataVersion"`
}

// SetExcluded is the setExcluded request.
type SetExcluded struct {
	Project  string `json:"project"`
	Excluded bool   `json:"excluded"`
}

// SetWindow is the setWindowDays request.
type SetWindow struct {
	Days int `json:"days"`
}

// SetEnabled is the setExtensionEnabled request.
type SetEnabled struct {
	Enabled bool `json:"enabled"`
}

// Settings is the persisted configuration of the pipeline. ExtPort is zero
// until the endpoint has bound a port once.
type Settings struct {
	Consent    bool
	WindowDays int
	ExtEnabled bool
	ExtPort    int
	Excluded   []string
}

// ErrNotFound reports that a requested session or stream is not stored.
var ErrNotFound = errors.New("usage: not found")

// ErrBadRequest reports request parameters outside the documented limits.
var ErrBadRequest = errors.New("usage: bad request")

// ErrNoConsent reports a request that needs transcript access while
// collection is not allowed.
var ErrNoConsent = errors.New("usage: collection not allowed")

// ErrStale reports a batch that no longer matches the stored state: consent
// was withdrawn, its project was excluded, or its BaseOffset is neither zero
// nor the stored cursor offset. Nothing of a stale batch is stored.
var ErrStale = errors.New("usage: stale batch")

// ExtSink receives validated extension events and rejection counts. The
// usage store implements it; the extension endpoint depends only on this.
type ExtSink interface {
	// AppendExt stores events. Events with an ID already stored are ignored.
	AppendExt(ctx context.Context, events []ExtEvent) error
	// CountRejected records refused inputs so they stay visible. Source is
	// empty when the input could not be attributed to a sender.
	CountRejected(ctx context.Context, source string, n int) error
}
