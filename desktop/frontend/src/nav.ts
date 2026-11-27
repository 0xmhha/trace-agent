import type { Filter, Fixed, Row } from './data'
import { t, type MessageKey } from './i18n'
export type Selection = {
  kind: string
  task?: { taskId: string }
  entity?: { taskId: string; id: string }
  sourceId?: string
  evidenceId?: string
  [key: string]: unknown
}
export type Route = {
  selection: Selection
  filter?: Filter
  hours?: number
  fixed?: Fixed
  all?: Fixed
  scene?: string
  lane?: string
  explicit?: boolean
  /**
   * Notice above the screen. It is a message key, so that an entry kept in
   * the browser history is shown in the locale of the moment.
   */
  notice?: MessageKey
  play?: boolean
  after?: string
  caption?: string
  scroll?: number
}
/** One entry of the trace group in the sidebar. */
export interface TraceNavEntry {
  kind: string
  label: string
  icon: string
  /** The entry that follows the tasks in progress instead of a fixed list. */
  live?: boolean
}
/**
 * Sidebar entries of the trace group, in display order. The labels are
 * getters so that they follow the current locale.
 */
export const traceNav: readonly TraceNavEntry[] = [
  {
    kind: 'TASKS',
    get label() {
      return t('shell.navLive')
    },
    icon: '⌁',
    live: true,
  },
  {
    kind: 'OVERVIEW',
    get label() {
      return t('shell.navOverview')
    },
    icon: '▥',
  },
  {
    kind: 'TASKS',
    get label() {
      return t('shell.navTasks')
    },
    icon: '☷',
  },
  {
    kind: 'MODULE_GROUPS',
    get label() {
      return t('shell.navModules')
    },
    icon: '◫',
  },
  {
    kind: 'SOURCES',
    get label() {
      return t('shell.navSources')
    },
    icon: '⚙',
  },
]
/** A task sequence belongs to the live entry even though its kind differs. */
export function isLive(s: Selection): boolean {
  return s.activeTasksOnly === true || s.kind === 'SEQUENCE'
}
/** Selection a sidebar entry opens. */
export function navSelection(e: TraceNavEntry): Selection {
  return { kind: e.kind, ...(e.live ? { activeTasksOnly: true } : {}) }
}
/** Whether a sidebar entry is the one the selection belongs to. */
export function navSelected(e: TraceNavEntry, s: Selection): boolean {
  return e.live ? isLive(s) : !isLive(s) && s.kind === e.kind
}
const BAD_TASK_ADDRESS_NOTICE: MessageKey = 'shell.badTaskAddress'
export const task = (taskId: string) => ({ taskId })
export const entity = (taskId: string, id: string) => ({ taskId, id })
export function interval(hours: number): Filter {
  const until = new Date(),
    from = new Date(until.getTime() - hours * 3600000)
  return { interval: { from: from.toISOString(), until: until.toISOString() } }
}
export function initial(): Route {
  const state = history.state as Route | null
  if (state?.selection?.kind) return state
  const [kind, id] = location.hash.slice(1).split('/')
  if (kind === 'live' && id) {
    if (!/^[a-zA-Z0-9._-]{1,128}$/.test(id)) {
      return {
        selection: { kind: 'TASKS', activeTasksOnly: true },
        notice: BAD_TASK_ADDRESS_NOTICE,
      }
    }
    return {
      explicit: true,
      selection: { kind: 'SEQUENCE', task: task(id), sequenceWindow: 'LATEST' },
    }
  }
  return {
    selection: {
      kind:
        kind === 'overview'
          ? 'OVERVIEW'
          : kind === 'sources'
            ? 'SOURCES'
            : 'TASKS',
      ...(kind === 'overview' || kind === 'sources' || kind === 'tasks'
        ? {}
        : { activeTasksOnly: true }),
    },
  }
}
export function url(r: Route) {
  const s = r.selection
  return (
    '#' +
    (s.kind === 'SEQUENCE' ? 'live' : s.kind.toLowerCase()) +
    (s.task ? '/' + encodeURIComponent(s.task.taskId) : '')
  )
}
export type Go = (
  selection: Selection,
  options?: {
    cohort?: string
    scope?: Fixed
    caption?: string
    live?: boolean
    all?: boolean
    scene?: string
    lane?: string
  },
) => void
export type Query = (name: string, variables: Row) => Promise<Row>
