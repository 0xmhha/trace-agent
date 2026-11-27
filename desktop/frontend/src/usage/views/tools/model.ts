import type { ToolCategory, Tools } from '../../types'
import type { Tone } from '../../ui'
import { formatNumber, t, type MessageKey } from '../../../i18n'

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

/**
 * Every tab of the page, in display order. A new tab is added here only.
 * A function, so the labels follow the current locale.
 */
export function tabs(): readonly TabMeta[] {
  return [
    { id: 'tools', label: t('tools.tab.tools'), note: t('tools.tab.tools.note') },
    {
      id: 'mcpServers',
      label: t('tools.tab.mcpServers'),
      nameLabel: t('tools.tab.mcpServers.name'),
      tokens: false,
      note: t('tools.tab.mcpServers.note'),
    },
    {
      id: 'skills',
      label: t('tools.tab.skills'),
      nameLabel: t('tools.tab.skills.name'),
      tokens: true,
      note: t('tools.tab.skills.note'),
    },
    {
      id: 'plugins',
      label: t('tools.tab.plugins'),
      nameLabel: t('tools.tab.plugins.name'),
      tokens: true,
      note: t('tools.tab.plugins.note'),
    },
    { id: 'hooks', label: t('tools.tab.hooks'), note: t('tools.tab.hooks.note') },
    {
      id: 'subagents',
      label: t('tools.tab.subagents'),
      nameLabel: t('tools.tab.subagents.name'),
      tokens: false,
      note: t('tools.tab.subagents.note'),
    },
    {
      id: 'programs',
      label: t('tools.tab.programs'),
      nameLabel: t('tools.tab.programs.name'),
      tokens: false,
      note: t('tools.tab.programs.note'),
    },
  ]
}

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
  /** Message key of the category name; read it with t() at render. */
  labelKey: MessageKey
  /** Series colour. It follows the category, never its rank, so a filter does not repaint. */
  color: string
}
/** Tool categories in the fixed order that assigns the series colours. */
export const categories: readonly CategoryMeta[] = [
  { id: 'builtin', labelKey: 'tools.category.builtin', color: 'var(--u-s1)' },
  { id: 'mcp', labelKey: 'tools.category.mcp', color: 'var(--u-s2)' },
  { id: 'skill', labelKey: 'tools.category.skill', color: 'var(--u-s3)' },
  { id: 'agent', labelKey: 'tools.category.agent', color: 'var(--u-s4)' },
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
  const meta = tabs().find((tab) => tab.id === id)
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
  return formatNumber(ratio, { style: 'percent', maximumFractionDigits: 0 })
}
