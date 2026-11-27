// Mirror of internal/usage/api.go and record.go. Keep field names identical
// to the Go JSON tags; the Go file is the source of truth.

export type ToolCategory = 'builtin' | 'mcp' | 'skill' | 'agent'
export type HookStatus = 'success' | 'error' | 'blocked' | 'unknown'
export type CoverageState = 'complete' | 'partial' | 'scanning' | 'disabled'
export type SourceState = 'available' | 'missing' | 'unreadable'
export type Severity = 'info' | 'warn' | 'critical'
export type InsightCategory = 'cost' | 'reliability' | 'workflow' | 'extension'
export type RecommendationKind =
  | 'skill'
  | 'plugin'
  | 'mcp'
  | 'hook'
  | 'setting'
  | 'habit'
export type SessionSort = 'last' | 'tokens' | 'errors' | 'cost'
export type ExtKind = 'span' | 'log' | 'metric'
export type ExtStatus = 'ok' | 'error' | 'unknown'
export type ExtLevel = 'debug' | 'info' | 'warn' | 'error'
export type TimelineKind =
  | 'prompt'
  | 'assistant'
  | 'thinking'
  | 'tool_call'
  | 'tool_result'
  | 'hook'
  | 'system'
  | 'ext'

/** RFC 3339 timestamp. */
export type Instant = string

export interface Coverage {
  state: CoverageState
  filesTracked: number
  filesUnreadable: number
  linesSkipped: number
  windowFrom: Instant
  lastScanAt: Instant
  reasons: string[]
}
export interface Range {
  from: Instant
  until: Instant
}
export interface Query {
  range: Range
  project?: string
  binHours?: number
  /** Language of generated text such as insights; see i18n LOCALES. */
  locale?: string
}
export interface TokenTotals {
  input: number
  output: number
  cacheRead: number
  cacheCreate: number
}
export interface Bin {
  from: Instant
  tokens: TokenTotals
  sessions: number
  prompts: number
  toolCalls: number
  toolErrors: number
}
export interface ModelUsage {
  model: string
  calls: number
  sessions: number
  tokens: TokenTotals
}
export interface ProjectUsage {
  key: string
  path: string
  sessions: number
  prompts: number
  tokens: TokenTotals
  lastAt: Instant
  excluded: boolean
}
export interface ToolUsage {
  name: string
  category: ToolCategory
  mcpServer?: string
  plugin?: string
  calls: number
  errors: number
  pending: number
  sessions: number
  p50Ms: number | null
  p95Ms: number | null
}
export interface GroupUsage {
  name: string
  calls: number
  errors: number
  sessions: number
  tokens?: TokenTotals
  avgMs: number | null
}
export interface HookUsage {
  event: string
  name: string
  runs: number
  failures: number
  avgMs: number | null
  maxMs: number | null
}
export interface HeatCell {
  weekday: number
  hour: number
  prompts: number
}
export interface Overview {
  coverage: Coverage
  range: Range
  sessions: number
  prompts: number
  modelCalls: number
  toolCalls: number
  toolErrors: number
  subagents: number
  compactions: number
  activeDays: number
  tokens: TokenTotals
  costUsd: number | null
  costSessions: number
  cacheHitRatio: number | null
  series: Bin[]
  models: ModelUsage[]
  projects: ProjectUsage[]
  tools: ToolUsage[]
  heatmap: HeatCell[]
}
export interface Tools {
  coverage: Coverage
  range: Range
  tools: ToolUsage[]
  mcpServers: GroupUsage[]
  skills: GroupUsage[]
  plugins: GroupUsage[]
  subagents: GroupUsage[]
  programs: GroupUsage[]
  hooks: HookUsage[]
}
export interface Projects {
  coverage: Coverage
  items: ProjectUsage[]
}
export interface SessionQuery {
  range: Range
  project?: string
  sort?: SessionSort
  search?: string
  limit?: number
  cursor?: string
}
export interface SessionRow {
  id: string
  title: string
  projectKey: string
  projectPath: string
  entrypoint: string
  gitBranch: string
  startedAt: Instant
  lastAt: Instant
  prompts: number
  modelCalls: number
  toolCalls: number
  toolErrors: number
  subagents: number
  compactions: number
  tokens: TokenTotals
  costUsd: number | null
  models: string[]
}
export interface SessionPage {
  coverage: Coverage
  items: SessionRow[]
  total: number
  nextCursor?: string
}
export interface AgentRow {
  agentId: string
  agentType: string
  modelCalls: number
  toolCalls: number
  toolErrors: number
  tokens: TokenTotals
  startedAt: Instant
  lastAt: Instant
}
export interface TurnPoint {
  at: Instant
  model: string
  context: number
  output: number
  toolCalls: number
}
export interface SessionDetail {
  session: SessionRow
  source: SourceState
  models: ModelUsage[]
  tools: ToolUsage[]
  mcpServers: GroupUsage[]
  skills: GroupUsage[]
  plugins: GroupUsage[]
  hooks: HookUsage[]
  agents: AgentRow[]
  turns: TurnPoint[]
  turnsTruncated: boolean
  extSources: GroupUsage[]
}
export interface TimelineRequest {
  sessionId: string
  agentId?: string
  cursor?: string
  limit?: number
}
export interface TimelineTool {
  useId: string
  name: string
  category: ToolCategory
  input?: string
  isError: boolean
  durationMs?: number
}
export interface TimelineHook {
  event: string
  name: string
  status: HookStatus
  exitCode: number
  durationMs: number
}
export interface ExtEvent {
  id: string
  source: string
  kind: ExtKind
  name: string
  at: Instant
  receivedAt: Instant
  durationMs?: number
  status: ExtStatus
  level: ExtLevel
  sessionId?: string
  toolUseId?: string
  traceId?: string
  spanId?: string
  parentSpanId?: string
  message?: string
  attributes?: Record<string, string>
  value?: number
  unit?: string
}
export type SystemKind = 'turn_end' | 'compaction'
export interface TimelineSystem {
  kind: SystemKind
  durationMs?: number
  trigger?: string
}
export interface TimelineItem {
  /** Structured form of a system item; label is an English fallback. */
  system?: TimelineSystem
  id: string
  at: Instant
  kind: TimelineKind
  agentId?: string
  text?: string
  truncated?: boolean
  masked?: number
  label?: string
  model?: string
  tokens?: TokenTotals
  tool?: TimelineTool
  hook?: TimelineHook
  ext?: ExtEvent
}
export interface TimelinePage {
  source: SourceState
  items: TimelineItem[]
  nextCursor?: string
  skipped: number
}
export interface Evidence {
  label: string
  value: number
  unit?: string
  of?: number
}
export interface Recommendation {
  kind: RecommendationKind
  title: string
  action: string
  target?: string
}
export interface Insight {
  id: string
  ruleId: string
  severity: Severity
  category: InsightCategory
  title: string
  detail: string
  evidence: Evidence[]
  sessionIds?: string[]
  recommendation?: Recommendation
}
export interface InsightReport {
  coverage: Coverage
  generatedAt: Instant
  items: Insight[]
  rulesFired: number
  rulesPassed: number
  rulesSkipped: number
}
export interface ExtSourceRow {
  source: string
  events: number
  errors: number
  linked: number
  unlinked: number
  rejected: number
  lastAt: Instant
}
export interface ExtEndpoint {
  enabled: boolean
  url: string
}
export interface ExtOverview {
  endpoint: ExtEndpoint
  sources: ExtSourceRow[]
  rejected: number
}
export interface ExtQuery {
  range: Range
  source?: string
  sessionId?: string
  toolUseId?: string
  level?: ExtLevel
  limit?: number
  cursor?: string
}
export interface ExtPage {
  items: ExtEvent[]
  total: number
  nextCursor?: string
}
export interface Status {
  consent: boolean
  root: string
  windowDays: number
  scanning: boolean
  filesTotal: number
  filesDone: number
  coverage: Coverage
  excluded: string[]
  extension: ExtEndpoint
  dataVersion: number
}
