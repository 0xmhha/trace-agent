import type { Filter, Fixed, Row } from './data'
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
  notice?: string
  play?: boolean
  after?: string
  caption?: string
  scroll?: number
}
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
        notice:
          '작업 주소를 확인할 수 없습니다. 진행 중인 작업을 선택해 주세요.',
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
