// Deterministic mock of UsageApi for the development preview and UI tests.
// All names and numbers are synthetic.
import type { UsageApi } from './api'
import type {
  Bin,
  Coverage,
  ExtEvent,
  ExtSourceRow,
  GroupUsage,
  HeatCell,
  HookUsage,
  Insight,
  ModelUsage,
  ProjectUsage,
  Query,
  Range,
  SessionRow,
  Status,
  TimelineItem,
  TokenTotals,
  ToolUsage,
} from './types'

const HOUR = 3_600_000
const DAY = 24 * HOUR

/** Small seeded generator so every reload shows the same picture. */
function seeded(seed: number) {
  let s = seed >>> 0
  return () => {
    s = (Math.imul(s, 1664525) + 1013904223) >>> 0
    return s / 2 ** 32
  }
}
const iso = (ms: number) => new Date(ms).toISOString()
const tokens = (r: () => number, scale: number): TokenTotals => ({
  input: Math.round(r() * 4_000 * scale),
  output: Math.round(r() * 9_000 * scale),
  cacheRead: Math.round(r() * 260_000 * scale),
  cacheCreate: Math.round(r() * 38_000 * scale),
})
const add = (a: TokenTotals, b: TokenTotals): TokenTotals => ({
  input: a.input + b.input,
  output: a.output + b.output,
  cacheRead: a.cacheRead + b.cacheRead,
  cacheCreate: a.cacheCreate + b.cacheCreate,
})
const zero: TokenTotals = { input: 0, output: 0, cacheRead: 0, cacheCreate: 0 }

const projectNames = ['sample-api', 'sample-web', 'infra-notes', 'data-tools', 'docs-site']
const modelNames = ['model-large', 'model-medium', 'model-small']
const titlesPool = [
  '결제 재시도 로직 정리',
  '빌드 캐시 문제 조사',
  '대시보드 차트 추가',
  '마이그레이션 스크립트 검토',
  '테스트 실패 원인 찾기',
  '설정 화면 리팩토링',
  '로그 수집기 연결',
  'API 문서 초안',
]

let state = {
  consent: true,
  windowDays: 30,
  extension: true,
  excluded: ['docs-site'] as string[],
  version: 1,
}

function coverage(): Coverage {
  return {
    state: state.consent ? 'partial' : 'disabled',
    filesTracked: 412,
    filesUnreadable: 1,
    linesSkipped: 3,
    windowFrom: iso(Date.now() - state.windowDays * DAY),
    lastScanAt: iso(Date.now() - 4000),
    reasons: ['unreadable_files', 'skipped_lines'],
  }
}

function status(): Status {
  return {
    consent: state.consent,
    root: '~/.claude/projects',
    windowDays: state.windowDays,
    scanning: false,
    filesTotal: 412,
    filesDone: 412,
    coverage: coverage(),
    excluded: state.excluded,
    extension: { enabled: state.extension, url: 'http://127.0.0.1:43210/v1/events' },
    dataVersion: state.version,
  }
}

function sessionsIn(range: Range, project?: string): SessionRow[] {
  const r = seeded(7)
  const from = Date.parse(range.from),
    until = Date.parse(range.until)
  const rows: SessionRow[] = []
  for (let i = 0; i < 64; i++) {
    const last = until - Math.floor(r() * 30 * DAY) - i * 1000
    const projectKey = projectNames[Math.floor(r() * projectNames.length)]!
    const scale = 0.4 + r() * 6
    const toolCalls = Math.round(8 + r() * 140)
    const row: SessionRow = {
      id: 'sess-' + String(i).padStart(4, '0'),
      title: titlesPool[i % titlesPool.length]! + (i >= titlesPool.length ? ' ' + (i + 1) : ''),
      projectKey,
      projectPath: '~/work/' + projectKey,
      entrypoint: r() > 0.3 ? 'cli' : 'sdk-cli',
      gitBranch: r() > 0.5 ? 'main' : 'feature/sample-' + (i % 5),
      startedAt: iso(last - Math.floor((0.2 + r() * 3) * HOUR)),
      lastAt: iso(last),
      prompts: Math.round(2 + r() * 30),
      modelCalls: Math.round(10 + r() * 160),
      toolCalls,
      toolErrors: Math.round(toolCalls * r() * 0.18),
      subagents: Math.floor(r() * 5),
      compactions: r() > 0.8 ? 1 + Math.floor(r() * 2) : 0,
      tokens: tokens(r, scale),
      costUsd: r() > 0.25 ? Number((scale * (0.4 + r() * 2.5)).toFixed(2)) : null,
      models: r() > 0.6 ? modelNames.slice(0, 2) : [modelNames[0]!],
    }
    if (last >= from && last < until && (!project || project === projectKey)) rows.push(row)
  }
  return rows.sort((a, b) => Date.parse(b.lastAt) - Date.parse(a.lastAt))
}

function toolRows(scale: number): ToolUsage[] {
  const r = seeded(11)
  const base: [string, ToolUsage['category'], string?, string?][] = [
    ['Bash', 'builtin'],
    ['Read', 'builtin'],
    ['Edit', 'builtin'],
    ['Grep', 'builtin'],
    ['Write', 'builtin'],
    ['Agent', 'agent'],
    ['Skill', 'skill'],
    ['mcp__issues__search', 'mcp', 'issues'],
    ['mcp__plugin_devkit_chain__status', 'mcp', 'chain', 'devkit'],
    ['WebFetch', 'builtin'],
  ]
  return base.map(([name, category, mcpServer, plugin], i) => {
    const calls = Math.round((900 / (i + 1)) * scale * (0.6 + r()))
    return {
      name,
      category,
      mcpServer,
      plugin,
      calls,
      errors: Math.round(calls * (name === 'Bash' ? 0.14 : r() * 0.06)),
      pending: i === 0 ? 2 : 0,
      sessions: Math.max(1, Math.round(calls / (6 + r() * 20))),
      p50Ms: Math.round(40 + r() * 900),
      p95Ms: Math.round(1200 + r() * 9000),
    }
  })
}

function groups(names: string[], seed: number, withTokens = false): GroupUsage[] {
  const r = seeded(seed)
  return names.map((name, i) => {
    const calls = Math.round(260 / (i + 1) + r() * 20)
    return {
      name,
      calls,
      errors: Math.round(calls * r() * 0.08),
      sessions: Math.max(1, Math.round(calls / 5)),
      tokens: withTokens ? tokens(r, 3 / (i + 1)) : undefined,
      avgMs: Math.round(120 + r() * 2400),
    }
  })
}

const hookRows: HookUsage[] = [
  { event: 'PreToolUse', name: 'PreToolUse:Bash', runs: 640, failures: 4, avgMs: 38, maxMs: 410 },
  { event: 'UserPromptSubmit', name: 'UserPromptSubmit', runs: 212, failures: 0, avgMs: 55, maxMs: 300 },
  { event: 'Stop', name: 'Stop', runs: 198, failures: 9, avgMs: 1240, maxMs: 5200 },
  { event: 'SessionStart', name: 'SessionStart:startup', runs: 64, failures: 1, avgMs: 210, maxMs: 900 },
]

const extSources: ExtSourceRow[] = [
  { source: 'deploy-script', events: 148, errors: 12, linked: 120, unlinked: 28, rejected: 2, lastAt: iso(Date.now() - 20 * 60_000) },
  { source: 'chain-plugin', events: 96, errors: 3, linked: 96, unlinked: 0, rejected: 0, lastAt: iso(Date.now() - 3 * HOUR) },
]

function extEvents(): ExtEvent[] {
  const r = seeded(23)
  return Array.from({ length: 60 }, (_, i): ExtEvent => {
    const source = extSources[i % 2]!.source
    const failed = r() > 0.88
    const at = Date.now() - i * 11 * 60_000
    return {
      id: 'ext-' + i,
      source,
      kind: i % 5 === 0 ? 'log' : 'span',
      name: source === 'deploy-script' ? ['build', 'upload', 'verify'][i % 3]! : ['rpc.call', 'node.start'][i % 2]!,
      at: iso(at),
      receivedAt: iso(at + 12),
      durationMs: Math.round(30 + r() * 4000),
      status: failed ? 'error' : 'ok',
      level: failed ? 'error' : 'info',
      sessionId: i % 4 === 3 ? undefined : 'sess-' + String(i % 6).padStart(4, '0'),
      toolUseId: i % 4 === 3 ? undefined : 'toolu_mock_' + (i % 9),
      message: failed ? 'exit status 1: step failed' : undefined,
      attributes: { step: String(i % 3), region: 'local' },
    }
  })
}

const insightItems: Insight[] = [
  {
    id: 'tool-error-rate:Bash',
    ruleId: 'tool-error-rate',
    severity: 'warn',
    category: 'reliability',
    title: 'Bash 호출의 14%가 실패했습니다',
    detail: '같은 기간 다른 도구의 실패율은 6% 이하입니다. 실패가 몰린 세션을 먼저 확인해 보세요.',
    evidence: [{ label: '실패한 Bash 호출', value: 126, of: 900, unit: '회' }],
    sessionIds: ['sess-0001', 'sess-0004'],
    recommendation: {
      kind: 'setting',
      title: '자주 쓰는 명령을 허용 목록에 추가',
      action: '권한 거부로 실패한 명령이 많다면 프로젝트 설정의 허용 목록에 읽기 전용 명령을 등록합니다.',
      target: 'permissions.allow',
    },
  },
  {
    id: 'slow-hook:Stop',
    ruleId: 'slow-hook',
    severity: 'warn',
    category: 'workflow',
    title: 'Stop 훅이 평균 1.2초 걸립니다',
    detail: '턴이 끝날 때마다 실행되므로 지연이 그대로 대기 시간이 됩니다.',
    evidence: [
      { label: '평균 실행 시간', value: 1240, unit: 'ms' },
      { label: '실행 횟수', value: 198, unit: '회' },
    ],
    recommendation: {
      kind: 'hook',
      title: '훅 명령을 가볍게',
      action: '무거운 검사는 변경된 파일에만 돌리거나 비동기 작업으로 옮깁니다.',
    },
  },
  {
    id: 'low-cache-hit',
    ruleId: 'low-cache-hit',
    severity: 'info',
    category: 'cost',
    title: '캐시 적중률이 81%입니다',
    detail: '긴 세션에서 맥락이 자주 바뀌면 캐시가 다시 만들어집니다.',
    evidence: [{ label: '캐시에서 읽은 입력 토큰', value: 81, unit: '%' }],
  },
]

function series(range: Range, binHours: number, rows: SessionRow[]): Bin[] {
  const r = seeded(31)
  const from = Date.parse(range.from),
    until = Date.parse(range.until)
  const width = binHours * HOUR
  const bins: Bin[] = []
  for (let t = Math.floor(from / width) * width; t < until; t += width) {
    const active = rows.filter((s) => {
      const at = Date.parse(s.lastAt)
      return at >= t && at < t + width
    })
    const scale = active.length ? 1 + active.length : r() > 0.7 ? 0.5 : 0
    const calls = Math.round(scale * (20 + r() * 60))
    bins.push({
      from: iso(t),
      tokens: scale ? tokens(r, scale) : zero,
      sessions: active.length,
      prompts: Math.round(scale * (2 + r() * 6)),
      toolCalls: calls,
      toolErrors: Math.round(calls * r() * 0.12),
    })
  }
  return bins
}

function heatmap(): HeatCell[] {
  const r = seeded(41)
  const cells: HeatCell[] = []
  for (let weekday = 0; weekday < 7; weekday++)
    for (let hour = 0; hour < 24; hour++) {
      const work = weekday > 0 && weekday < 6 && hour >= 9 && hour <= 19
      cells.push({ weekday, hour, prompts: Math.round(r() * (work ? 18 : 2)) })
    }
  return cells
}

function timelineItems(sessionId: string): TimelineItem[] {
  const start = Date.now() - 2 * HOUR
  const at = (i: number) => iso(start + i * 9000)
  const call = { input: 12, output: 420, cacheRead: 48_000, cacheCreate: 2100 }
  return [
    { id: 't0', at: at(0), kind: 'hook', hook: { event: 'SessionStart', name: 'SessionStart:startup', status: 'success', exitCode: 0, durationMs: 180 } },
    { id: 't1', at: at(1), kind: 'prompt', text: '빌드가 왜 실패하는지 찾아 줘. 로그는 build.log 에 있어.' },
    { id: 't2', at: at(2), kind: 'thinking', model: 'model-large', tokens: { input: 0, output: 150, cacheRead: 0, cacheCreate: 0 } },
    { id: 't3', at: at(2), kind: 'assistant', model: 'model-large', tokens: call, text: '로그부터 확인하겠습니다.' },
    { id: 't4', at: at(3), kind: 'tool_call', tool: { useId: 'toolu_mock_0', name: 'Bash', category: 'builtin', input: '{\n  "command": "tail -n 40 build.log",\n  "description": "Show the end of the build log"\n}', isError: false } },
    { id: 't5', at: at(3), kind: 'hook', hook: { event: 'PreToolUse', name: 'PreToolUse:Bash', status: 'success', exitCode: 0, durationMs: 31 } },
    { id: 't6', at: at(4), kind: 'ext', ext: extEvents()[1] },
    { id: 't7', at: at(4), kind: 'tool_result', text: 'error: cannot find module "config"\nDB_PASSWORD=[가림]', masked: 1, tool: { useId: 'toolu_mock_0', name: 'Bash', category: 'builtin', isError: true, durationMs: 840 } },
    { id: 't8', at: at(5), kind: 'assistant', model: 'model-large', tokens: { ...call, output: 610 }, text: '`config` 모듈 경로가 바뀐 것이 원인입니다. import 경로를 고치겠습니다.' },
    { id: 't9', at: at(6), kind: 'tool_call', tool: { useId: 'toolu_mock_1', name: 'mcp__issues__search', category: 'mcp', input: '{\n  "query": "config module path"\n}', isError: false } },
    { id: 't10', at: at(7), kind: 'tool_result', text: '관련 이슈 2건', tool: { useId: 'toolu_mock_1', name: 'mcp__issues__search', category: 'mcp', isError: false, durationMs: 1320 } },
    { id: 't11', at: at(8), kind: 'system', label: '턴 종료 · 54.0초' },
    { id: 't12', at: at(9), kind: 'system', label: '맥락 압축 (auto)' },
  ].map((item) => ({ ...item, id: sessionId + ':' + item.id }) as TimelineItem)
}

const delay = <T>(value: T): Promise<T> => new Promise((resolve) => setTimeout(() => resolve(value), 80))
const bump = () => {
  state = { ...state, version: state.version + 1 }
  return delay(status())
}
const page = <T>(items: T[], limit = 50, cursor?: string) => {
  const start = cursor ? Number(cursor) : 0
  const end = start + limit
  return { items: items.slice(start, end), total: items.length, nextCursor: end < items.length ? String(end) : undefined }
}

function overviewOf(q: Query) {
  const rows = sessionsIn(q.range, q.project)
  const total = rows.reduce((sum, s) => add(sum, s.tokens), zero)
  const span = Date.parse(q.range.until) - Date.parse(q.range.from)
  const costs = rows.filter((s) => s.costUsd != null)
  const models: ModelUsage[] = modelNames.map((model, i) => ({
    model,
    calls: Math.round(rows.reduce((n, s) => n + s.modelCalls, 0) / (i * 3 + 1.4)),
    sessions: Math.round(rows.length / (i + 1)),
    tokens: { input: Math.round(total.input / (i * 3 + 1.4)), output: Math.round(total.output / (i * 3 + 1.4)), cacheRead: Math.round(total.cacheRead / (i * 3 + 1.4)), cacheCreate: Math.round(total.cacheCreate / (i * 3 + 1.4)) },
  }))
  const projects: ProjectUsage[] = projectNames.map((key) => {
    const own = rows.filter((s) => s.projectKey === key)
    return {
      key,
      path: '~/work/' + key,
      sessions: own.length,
      prompts: own.reduce((n, s) => n + s.prompts, 0),
      tokens: own.reduce((sum, s) => add(sum, s.tokens), zero),
      lastAt: own[0]?.lastAt ?? iso(0),
      excluded: state.excluded.includes(key),
    }
  })
  const input = total.input + total.cacheRead + total.cacheCreate
  return {
    coverage: coverage(),
    range: q.range,
    sessions: rows.length,
    prompts: rows.reduce((n, s) => n + s.prompts, 0),
    modelCalls: rows.reduce((n, s) => n + s.modelCalls, 0),
    toolCalls: rows.reduce((n, s) => n + s.toolCalls, 0),
    toolErrors: rows.reduce((n, s) => n + s.toolErrors, 0),
    subagents: rows.reduce((n, s) => n + s.subagents, 0),
    compactions: rows.reduce((n, s) => n + s.compactions, 0),
    activeDays: new Set(rows.map((s) => s.lastAt.slice(0, 10))).size,
    tokens: total,
    costUsd: costs.length ? costs.reduce((n, s) => n + (s.costUsd ?? 0), 0) : null,
    costSessions: costs.length,
    cacheHitRatio: input ? total.cacheRead / input : null,
    series: series(q.range, q.binHours || (span <= 2 * DAY ? 1 : 24), rows),
    models,
    projects: projects.sort((a, b) => b.sessions - a.sessions),
    tools: toolRows(Math.max(0.2, rows.length / 20)),
    heatmap: heatmap(),
  }
}

export const mockApi: UsageApi = {
  status: () => delay(status()),
  overview: (q) => delay(overviewOf(q)),
  sessions: (q) => {
    let rows = sessionsIn(q.range, q.project)
    const needle = q.search?.toLowerCase()
    if (needle) rows = rows.filter((s) => (s.title + s.projectPath).toLowerCase().includes(needle))
    const sum = (s: SessionRow) => s.tokens.input + s.tokens.output + s.tokens.cacheRead + s.tokens.cacheCreate
    if (q.sort === 'tokens') rows.sort((a, b) => sum(b) - sum(a))
    if (q.sort === 'errors') rows.sort((a, b) => b.toolErrors - a.toolErrors)
    if (q.sort === 'cost') rows.sort((a, b) => (b.costUsd ?? -1) - (a.costUsd ?? -1))
    return delay({ coverage: coverage(), ...page(rows, q.limit, q.cursor) })
  },
  session: (sessionId) => {
    const all = sessionsIn({ from: iso(0), until: iso(Date.now() + DAY) })
    const session = all.find((s) => s.id === sessionId) ?? all[0]!
    const r = seeded(53)
    const start = Date.parse(session.startedAt)
    let context = 18_000
    return delay({
      session,
      source: sessionId.endsWith('3') ? 'missing' : 'available',
      models: modelNames.slice(0, session.models.length).map((model, i) => ({ model, calls: Math.round(session.modelCalls / (i + 1)), sessions: 1, tokens: session.tokens })),
      tools: toolRows(0.15),
      mcpServers: groups(['issues', 'chain'], 61),
      skills: groups(['code-review', 'deploy-check'], 67, true),
      plugins: groups(['devkit'], 71, true),
      hooks: hookRows,
      agents: [
        { agentId: '', agentType: '', modelCalls: session.modelCalls, toolCalls: session.toolCalls, toolErrors: session.toolErrors, tokens: session.tokens, startedAt: session.startedAt, lastAt: session.lastAt },
        ...Array.from({ length: session.subagents }, (_, i) => ({ agentId: 'agent-' + i, agentType: i % 2 ? 'Explore' : 'general-purpose', modelCalls: 12 + i * 4, toolCalls: 20 + i * 6, toolErrors: i % 2, tokens: tokens(r, 0.6), startedAt: session.startedAt, lastAt: session.lastAt })),
      ],
      turns: Array.from({ length: 48 }, (_, i) => {
        context = i === 30 ? 22_000 : context + Math.round(r() * 9000)
        return { at: iso(start + i * 70_000), model: modelNames[0]!, context, output: Math.round(200 + r() * 2400), toolCalls: Math.floor(r() * 4) }
      }),
      turnsTruncated: false,
      extSources: groups(['deploy-script'], 73),
    })
  },
  timeline: (r) => {
    if (r.sessionId.endsWith('3')) return delay({ source: 'missing', items: [], skipped: 0 })
    const items = timelineItems(r.sessionId)
    const sliced = page(items, r.limit ?? 8, r.cursor)
    return delay({ source: 'available', items: sliced.items, nextCursor: sliced.nextCursor, skipped: 1 })
  },
  tools: (q) =>
    delay({
      coverage: coverage(),
      range: q.range,
      tools: toolRows(1),
      mcpServers: groups(['issues', 'chain', 'docs'], 61),
      skills: groups(['code-review', 'deploy-check', 'release-notes'], 67, true),
      plugins: groups(['devkit', 'notes'], 71, true),
      subagents: groups(['general-purpose', 'Explore', 'Plan'], 79),
      programs: groups(['git', 'go', 'npm', 'grep', 'curl', 'cat'], 83),
      hooks: hookRows,
    }),
  projects: (q) => delay({ coverage: coverage(), items: overviewOf(q).projects }),
  insights: () =>
    delay({ coverage: coverage(), generatedAt: iso(Date.now()), items: insightItems, rulesFired: 3, rulesPassed: 7, rulesSkipped: 2 }),
  extensions: () => delay({ endpoint: status().extension, sources: extSources, rejected: 2 }),
  extEvents: (q) => {
    let items = extEvents()
    if (q.source) items = items.filter((e) => e.source === q.source)
    if (q.sessionId) items = items.filter((e) => e.sessionId === q.sessionId)
    if (q.toolUseId) items = items.filter((e) => e.toolUseId === q.toolUseId)
    if (q.level) items = items.filter((e) => e.level === q.level)
    return delay(page(items, q.limit, q.cursor))
  },
  setExcluded: (project, excluded) => {
    state = { ...state, excluded: excluded ? [...new Set([...state.excluded, project])] : state.excluded.filter((p) => p !== project) }
    return bump()
  },
  setWindowDays: (days) => {
    state = { ...state, windowDays: days }
    return bump()
  },
  rescan: bump,
  clearData: bump,
  setExtensionEnabled: (enabled) => {
    state = { ...state, extension: enabled }
    return bump()
  },
  enableWatch: () => {
    state = { ...state, consent: true }
    return bump().then(() => true)
  },
  disableWatch: () => {
    state = { ...state, consent: false }
    return bump().then(() => undefined)
  },
  copyExtensionSettings: () => delay(undefined),
}
