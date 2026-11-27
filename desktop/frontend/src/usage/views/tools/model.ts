import type { ToolCategory, Tools } from '../../types'
import type { Tone } from '../../ui'

/** Tabs whose rows are GroupUsage. */
export type GroupTabId = 'mcpServers' | 'skills' | 'plugins' | 'subagents' | 'programs'
/** Tab ids equal the keys of the Tools payload, so a tab's rows are data[id]. */
export type TabId = 'tools' | 'hooks' | GroupTabId

interface TabBase {
  label: string
  /** One line telling the reader what the numbers of the tab count. */
  note: string
}
export type TabMeta =
  | (TabBase & { id: 'tools' })
  | (TabBase & { id: 'hooks' })
  | (TabBase & {
      id: GroupTabId
      /** Header of the name column. */
      nameLabel: string
      /** Whether the transcript attributes model calls, and so tokens, to this kind of group. */
      tokens: boolean
    })

/** Every tab of the page, in display order. A new tab is added here only. */
export const tabs: readonly TabMeta[] = [
  {
    id: 'tools',
    label: '도구',
    note: '도구 이름별 호출 수입니다. 실패율은 실패한 호출을 전체 호출로 나눈 값이고, p50과 p95는 결과가 돌아온 호출만으로 구한 소요 시간입니다.',
  },
  {
    id: 'mcpServers',
    label: 'MCP 서버',
    nameLabel: '서버',
    tokens: false,
    note: 'MCP 서버별로 그 서버가 제공한 도구의 호출을 합쳤습니다. 평균 시간은 결과가 돌아온 호출만으로 구합니다.',
  },
  {
    id: 'skills',
    label: '스킬',
    nameLabel: '스킬',
    tokens: true,
    note: 'Skill 도구로 불러온 스킬별 호출 수입니다. 토큰은 세션 기록이 그 스킬에 귀속한 모델 호출의 합이고, 귀속 기록이 없으면 비어 있습니다.',
  },
  {
    id: 'plugins',
    label: '플러그인',
    nameLabel: '플러그인',
    tokens: true,
    note: '플러그인이 제공한 스킬과 MCP 도구의 호출을 플러그인별로 합쳤습니다. 토큰은 세션 기록이 그 플러그인에 귀속한 모델 호출의 합입니다.',
  },
  {
    id: 'hooks',
    label: '훅',
    note: '훅 이벤트와 이름별 실행 횟수입니다. 훅은 도구 호출이나 턴마다 실행되므로 실행 시간이 그대로 대기 시간이 됩니다.',
  },
  {
    id: 'subagents',
    label: '하위 에이전트',
    nameLabel: '종류',
    tokens: false,
    note: 'Agent 도구로 시작한 하위 에이전트를 종류별로 센 수치입니다. 실패율은 실패로 끝난 실행의 비율입니다.',
  },
  {
    id: 'programs',
    label: 'Bash 프로그램',
    nameLabel: '프로그램',
    tokens: false,
    note: 'Bash 명령에서 프로그램 이름만 뽑아 센 수치입니다. 명령의 인자는 저장하지 않으므로 여기에도 나오지 않습니다.',
  },
]

// Fails to compile when a tab id stops being a list of the payload.
type TabRows = { [K in TabId]: Tools[K] extends unknown[] ? true : never }
const _tabRows: TabRows = {
  tools: true,
  hooks: true,
  mcpServers: true,
  skills: true,
  plugins: true,
  subagents: true,
  programs: true,
}
void _tabRows

export const DEFAULT_TAB: TabId = 'tools'

export interface CategoryMeta {
  id: ToolCategory
  label: string
  /** Series colour. It follows the category, never its rank, so a filter does not repaint. */
  color: string
}
/** Tool categories in the fixed order that assigns the series colours. */
export const categories: readonly CategoryMeta[] = [
  { id: 'builtin', label: '기본 도구', color: 'var(--u-s1)' },
  { id: 'mcp', label: 'MCP', color: 'var(--u-s2)' },
  { id: 'skill', label: '스킬', color: 'var(--u-s3)' },
  { id: 'agent', label: '하위 에이전트', color: 'var(--u-s4)' },
]
export function categoryMeta(id: ToolCategory): CategoryMeta {
  return categories.find((c) => c.id === id) ?? categories[0]!
}

// route.tab carries the tab and, for the tool tab, its category filter as
// "tools:mcp", so both survive a reload and the back button.
const TAB_SEPARATOR = ':'

export interface TabState {
  tab: TabId
  category: ToolCategory | null
}
export function parseTab(raw: string | undefined): TabState {
  const [id = '', filter = ''] = (raw ?? '').split(TAB_SEPARATOR)
  const meta = tabs.find((t) => t.id === id)
  if (!meta) return { tab: DEFAULT_TAB, category: null }
  const category =
    meta.id === 'tools' ? (categories.find((c) => c.id === filter)?.id ?? null) : null
  return { tab: meta.id, category }
}
/** Inverse of parseTab. The default state is undefined so the address stays short. */
export function formatTab(state: TabState): string | undefined {
  if (state.tab !== 'tools' || !state.category)
    return state.tab === DEFAULT_TAB ? undefined : state.tab
  return state.tab + TAB_SEPARATOR + state.category
}

/** Failure rates at or above these are shown in the warning and the error tone. */
export const FAILURE_WARN_RATIO = 0.05
export const FAILURE_BAD_RATIO = 0.15
export function failureTone(rate: number | null): Tone {
  if (rate == null) return 'muted'
  if (rate >= FAILURE_BAD_RATIO) return 'bad'
  if (rate >= FAILURE_WARN_RATIO) return 'warn'
  return 'plain'
}

/** A hook adds its duration to every tool call or turn it runs on. */
export const HOOK_AVG_WARN_MS = 500
export const HOOK_AVG_BAD_MS = 1000
export const HOOK_MAX_WARN_MS = 2000
export const HOOK_MAX_BAD_MS = 5000
export function slowTone(ms: number | null, warnAt: number, badAt: number): Tone {
  if (ms == null) return 'muted'
  if (ms >= badAt) return 'bad'
  if (ms >= warnAt) return 'warn'
  return 'plain'
}

/** 0.05 → "5%", for threshold text. */
export function thresholdPct(ratio: number): string {
  return Math.round(ratio * 100) + '%'
}
