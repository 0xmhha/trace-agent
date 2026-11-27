import { useCallback, useEffect, useRef, useState } from 'react'
import type { UsageApi } from '../../api'
import type { ExtEvent, ExtPage, ExtQuery, Range } from '../../types'
import { EVENT_PAGE_MAX, EVENT_PAGE_SIZE, type EventFilter } from './model'
import { errorText } from '../../../i18n'

export interface EventPages {
  items: ExtEvent[]
  total: number
  hasMore: boolean
  /** False until the first response arrived. */
  loaded: boolean
  loading: boolean
  loadingMore: boolean
  error: string
  reload: () => void
  more: () => void
}

interface Loaded {
  items: ExtEvent[]
  total: number
  nextCursor?: string
}

function appendPage(shown: Loaded, page: ExtPage): Loaded {
  // New events arriving between two requests shift the pages, so a row can
  // come back twice. Rows are keyed by ID; keep the first copy.
  const seen = new Set(shown.items.map((e) => e.id))
  return {
    items: [...shown.items, ...page.items.filter((e) => !seen.has(e.id))],
    total: page.total,
    nextCursor: page.nextCursor,
  }
}

/**
 * Event list that grows with the show-more button. Rows already on screen stay while a
 * newer request is in flight. A refresh of the same filter reloads as many
 * rows as were shown (up to the page cap) so the list does not collapse under
 * the reader; a changed filter starts again from one page.
 */
export function useEventPages(
  api: UsageApi,
  range: Range,
  filter: EventFilter,
  refresh: number,
): EventPages {
  const [shown, setShown] = useState<Loaded | null>(null),
    [error, setError] = useState(''),
    [loading, setLoading] = useState(true),
    [loadingMore, setLoadingMore] = useState(false),
    [tick, setTick] = useState(0)
  const generation = useRef(0)
  // Read by the next reload to decide how many rows to ask for.
  const shownCount = useRef(0)
  useEffect(() => {
    shownCount.current = shown?.items.length ?? 0
  }, [shown])
  const filterKey = filter.level + '@' + filter.source
  const lastFilterKey = useRef(filterKey)

  const { source, level } = filter
  const query = useCallback(
    (limit: number, cursor?: string): ExtQuery => ({
      range,
      source: source || undefined,
      level: level || undefined,
      limit,
      cursor,
    }),
    [range, source, level],
  )

  useEffect(() => {
    const id = ++generation.current
    const sameFilter = lastFilterKey.current === filterKey
    lastFilterKey.current = filterKey
    const limit = sameFilter
      ? Math.min(Math.max(shownCount.current, EVENT_PAGE_SIZE), EVENT_PAGE_MAX)
      : EVENT_PAGE_SIZE
    setLoading(true)
    setLoadingMore(false)
    api
      .extEvents(query(limit))
      .then((page) => {
        if (id !== generation.current) return
        setShown({ items: page.items, total: page.total, nextCursor: page.nextCursor })
        setError('')
      })
      .catch((reason: unknown) => {
        if (id !== generation.current) return
        setError(errorText(reason, 'extensions.events.loadFailed'))
      })
      .finally(() => {
        if (id === generation.current) setLoading(false)
      })
  }, [api, query, refresh, tick])

  const cursor = shown?.nextCursor
  const more = useCallback(() => {
    if (!cursor) return
    const id = generation.current
    setLoadingMore(true)
    api
      .extEvents(query(EVENT_PAGE_SIZE, cursor))
      .then((page) => {
        if (id !== generation.current) return
        setShown((prev) => appendPage(prev ?? { items: [], total: 0 }, page))
        setError('')
      })
      .catch((reason: unknown) => {
        if (id !== generation.current) return
        setError(errorText(reason, 'extensions.events.moreFailed'))
      })
      .finally(() => {
        if (id === generation.current) setLoadingMore(false)
      })
  }, [api, query, cursor])

  const reload = useCallback(() => setTick((n) => n + 1), [])

  return {
    items: shown?.items ?? [],
    total: shown?.total ?? 0,
    hasMore: Boolean(cursor),
    loaded: shown !== null,
    loading,
    loadingMore,
    error,
    reload,
    more,
  }
}
