import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { t } from '../../../i18n'
import { rejectionMessage } from './errors'

interface PageLike {
  nextCursor?: string
}
interface Entry<P> {
  /** Cursor that produced the page, kept so the page can be read again. */
  cursor: string | undefined
  page: P
}
type FailedStep = 'first' | 'more'

/** Keeps a bridge code as it is, so describeError can still recognise it. */
function failureText(reason: unknown): string {
  return rejectionMessage(reason) || t('errors.generic')
}

/**
 * How a list follows new data without throwing away what the user loaded.
 * A tail list (oldest first) grows at its end, so only its last page is read
 * again, and only once the end has been reached. A head list (newest first)
 * grows at its start, so its first page is read again while it is the only
 * one loaded; with more pages loaded the user reloads by hand.
 */
export type Follow = 'tail' | 'head'

export interface Paged<P> {
  pages: P[]
  nextCursor: string | undefined
  /** The first page of the current selection is being read. */
  loading: boolean
  loadingMore: boolean
  error: string
  /** Repeats the request that failed. */
  retry: () => void
  more: () => void
  /** Reads from the start again and keeps the current pages visible meanwhile. */
  reload: () => void
}

/**
 * Cursor-paged loading with a "more" step. Changing deps starts over with an
 * empty list; a change of refresh follows new data as described on Follow.
 * Responses that belong to an older selection are dropped.
 */
export function usePaged<P extends PageLike>(
  fetchPage: (cursor: string | undefined) => Promise<P>,
  deps: unknown[],
  refresh: number,
  follow: Follow,
): Paged<P> {
  const [entries, setEntries] = useState<Entry<P>[]>([]),
    [loading, setLoading] = useState(true),
    [loadingMore, setLoadingMore] = useState(false),
    [failure, setFailure] = useState<{ message: string; step: FailedStep } | null>(null)
  const generation = useRef(0),
    appending = useRef(false),
    fetchRef = useRef(fetchPage),
    entriesRef = useRef(entries),
    seenRefresh = useRef(refresh)
  // Declared first so the effects below always see the latest render.
  useEffect(() => {
    fetchRef.current = fetchPage
    entriesRef.current = entries
  })

  const first = useCallback((clear: boolean) => {
    const id = ++generation.current
    appending.current = false
    if (clear) setEntries([])
    setLoading(true)
    setLoadingMore(false)
    setFailure(null)
    fetchRef
      .current(undefined)
      .then((page) => {
        if (id === generation.current) setEntries([{ cursor: undefined, page }])
      })
      .catch((reason: unknown) => {
        if (id === generation.current) setFailure({ message: failureText(reason), step: 'first' })
      })
      .finally(() => {
        if (id === generation.current) setLoading(false)
      })
  }, [])

  useEffect(() => {
    first(true)
    return () => {
      generation.current++
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps)

  const more = useCallback(() => {
    const cursor = entriesRef.current.at(-1)?.page.nextCursor
    if (!cursor || appending.current) return
    const id = generation.current
    appending.current = true
    setLoadingMore(true)
    setFailure(null)
    fetchRef
      .current(cursor)
      .then((page) => {
        if (id === generation.current) setEntries((prev) => [...prev, { cursor, page }])
      })
      .catch((reason: unknown) => {
        if (id === generation.current) setFailure({ message: failureText(reason), step: 'more' })
      })
      .finally(() => {
        if (id !== generation.current) return
        appending.current = false
        setLoadingMore(false)
      })
  }, [])

  useEffect(() => {
    if (seenRefresh.current === refresh) return
    seenRefresh.current = refresh
    const current = entriesRef.current
    if (!current.length || appending.current) return
    const index = follow === 'tail' ? current.length - 1 : 0
    const target = current[index]
    if (!target) return
    if (follow === 'tail' && target.page.nextCursor) return
    if (follow === 'head' && current.length > 1) return
    const id = generation.current
    fetchRef
      .current(target.cursor)
      .then((page) => {
        if (id !== generation.current) return
        setEntries((prev) =>
          prev.length === current.length
            ? prev.map((entry, i) => (i === index ? { cursor: entry.cursor, page } : entry))
            : prev,
        )
      })
      .catch((reason: unknown) => {
        if (id === generation.current) setFailure({ message: failureText(reason), step: 'first' })
      })
  }, [refresh, follow])

  const reload = useCallback(() => first(false), [first])
  // Stable while nothing was loaded, so callers can memoize on it.
  const pages = useMemo(() => entries.map((entry) => entry.page), [entries])
  return {
    pages,
    nextCursor: entries.at(-1)?.page.nextCursor,
    loading,
    loadingMore,
    error: failure?.message ?? '',
    retry: failure?.step === 'more' ? more : reload,
    more,
    reload,
  }
}
