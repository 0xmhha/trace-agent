import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { UsageApi } from '../../api'
import type { Coverage, Range, SessionQuery, SessionRow, SessionSort } from '../../types'
import { useLoad } from '../../ui'

/** Rows asked for per page. */
export const PAGE_SIZE = 50
/** Mirrors usage.MaxPageSize: the store never returns more rows per request. */
const MAX_PAGE_SIZE = 200
/** Upper bound on follow-up requests of one reload, so a cursor that never ends cannot loop. */
const MAX_REFILL_REQUESTS = 25

/** What the list asks for, without paging. */
export interface SessionListQuery {
  range: Range
  project?: string
  sort: SessionSort
  search: string
}

/** Rows from the start of the list, loaded together. */
interface Head {
  /** Identifies the question the rows answer; see questionKey. */
  key: string
  items: SessionRow[]
  total: number
  coverage: Coverage
  nextCursor?: string
}

/** Pages appended after a head. They are dropped as soon as the head is replaced. */
interface Tail {
  head: Head
  items: SessionRow[]
  total: number
  nextCursor?: string
}

export interface SessionPages {
  rows: SessionRow[]
  total: number
  coverage: Coverage | undefined
  /** False until the first response arrived. */
  ready: boolean
  loading: boolean
  /** True while the visible rows still answer the previous sort, search or filter. */
  stale: boolean
  error: string
  reload: () => void
  hasMore: boolean
  more: () => void
  moreLoading: boolean
  moreError: string
}

const idle = { loading: false, error: '' }

/**
 * The same question asked again after new data arrived keeps its key, so the
 * reload can restore every row the user had already paged in. The range moves
 * with each refresh; only its length is part of the question.
 */
function questionKey(q: SessionListQuery): string {
  const span = Date.parse(q.range.until) - Date.parse(q.range.from)
  return JSON.stringify([q.project ?? '', q.sort, q.search, span])
}

function withoutSeen(seen: SessionRow[], incoming: SessionRow[]): SessionRow[] {
  // A session can move between pages while new activity arrives.
  const ids = new Set(seen.map((row) => row.id))
  return incoming.filter((row) => {
    if (ids.has(row.id)) return false
    ids.add(row.id)
    return true
  })
}

async function loadHead(
  api: UsageApi,
  base: SessionQuery,
  key: string,
  want: number,
): Promise<Head> {
  const first = await api.sessions({ ...base, limit: Math.min(want, MAX_PAGE_SIZE) })
  const items = withoutSeen([], first.items)
  let cursor = first.nextCursor
  for (let i = 0; cursor && items.length < want && i < MAX_REFILL_REQUESTS; i++) {
    const limit = Math.min(want - items.length, MAX_PAGE_SIZE)
    const next = await api.sessions({ ...base, limit, cursor })
    if (!next.items.length) break
    items.push(...withoutSeen(items, next.items))
    cursor = next.nextCursor
  }
  return { key, items, total: first.total, coverage: first.coverage, nextCursor: cursor }
}

/**
 * Loads the session list page by page. A changed sort, search, project or
 * period starts again from the first page. New data for the same question
 * reloads as many rows as are on screen, so the list does not jump back.
 */
export function useSessionPages(
  api: UsageApi,
  query: SessionListQuery,
  refresh: number,
): SessionPages {
  const { range, project, sort, search } = query
  const key = questionKey(query)
  const base = useMemo<SessionQuery>(
    () => ({ range, project, sort, search: search || undefined }),
    [range, project, sort, search],
  )

  // What is on screen now, for the next reload of the same question.
  const shown = useRef({ key: '', count: 0 })
  const appending = useRef<Promise<void> | null>(null)
  const generation = useRef(0)

  const head = useLoad(async () => {
    // A page still being appended is part of what this reload has to cover.
    await appending.current
    const want =
      shown.current.key === key ? Math.max(PAGE_SIZE, shown.current.count) : PAGE_SIZE
    return loadHead(api, base, key, want)
  }, [api, range, project, sort, search, refresh])

  const [tail, setTail] = useState<Tail | null>(null)
  const [moreState, setMoreState] = useState(idle)
  const data = head.data
  const live = tail && tail.head === data ? tail : null
  const rows = useMemo(
    () => (data ? (live ? [...data.items, ...live.items] : data.items) : []),
    [data, live],
  )
  const cursor = live ? live.nextCursor : data?.nextCursor

  useEffect(() => {
    shown.current = { key: data?.key ?? '', count: rows.length }
  }, [data, rows])
  useEffect(() => {
    // Whatever was being appended answers an older list.
    generation.current++
    setMoreState(idle)
  }, [data, key])

  const more = useCallback(() => {
    if (!data || !cursor || appending.current) return
    const id = ++generation.current
    const before = live?.items ?? []
    setMoreState({ loading: true, error: '' })
    appending.current = api
      .sessions({ ...base, limit: PAGE_SIZE, cursor })
      .then((page) => {
        if (id !== generation.current) return
        const items = [...before, ...withoutSeen([...data.items, ...before], page.items)]
        shown.current = { key: data.key, count: data.items.length + items.length }
        setTail({ head: data, items, total: page.total, nextCursor: page.nextCursor })
        setMoreState(idle)
      })
      .catch((reason: unknown) => {
        if (id !== generation.current) return
        setMoreState({
          loading: false,
          error: reason instanceof Error ? reason.message : '다음 쪽을 읽지 못했습니다.',
        })
      })
      .finally(() => {
        appending.current = null
      })
  }, [api, base, cursor, data, live])

  return {
    rows,
    // Rows can arrive between the count and a later page; never show "60 / 58".
    total: Math.max(live?.total ?? data?.total ?? 0, rows.length),
    coverage: data?.coverage,
    ready: data !== null,
    loading: head.loading,
    stale: head.loading && data !== null && data.key !== key,
    error: head.error,
    reload: head.reload,
    hasMore: Boolean(cursor),
    more,
    moreLoading: moreState.loading,
    moreError: moreState.error,
  }
}
