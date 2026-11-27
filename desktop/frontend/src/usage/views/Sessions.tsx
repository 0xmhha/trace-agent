import { useEffect, useId, useRef, useState } from 'react'
import { errorText, t } from '../../i18n'
import type { ViewProps } from '../index'
import type { Coverage, SessionSort } from '../types'
import { Card, CoverageNote, Empty, ErrorNote, fmtInt } from '../ui'
import { SessionTable } from './sessions/SessionTable'
import { SplitLegend } from './sessions/SplitBar'
import {
  MAX_SEARCH_LEN,
  encodeListState,
  normalizeSearch,
  parseListState,
  sortOptions,
} from './sessions/listState'
import { useSessionPages } from './sessions/useSessionPages'
import './sessions.css'

/** Pause after the last keystroke before the search is applied. */
const SEARCH_DEBOUNCE_MS = 350

/** Says why a search found nothing without hiding that the data may be incomplete. */
function searchEmptyText(search: string, coverage: Coverage | undefined): string | undefined {
  const state = coverage?.state ?? 'complete'
  if (!search || state === 'disabled') return undefined
  if (state === 'scanning') return t('sessions.searchEmptyScanning', { search })
  if (state === 'partial') return t('sessions.searchEmptyPartial', { search })
  return t('sessions.searchEmpty', { search })
}

/** Error text of the list; codes from the desktop bridge are translated, other messages shown as they are. */
function listError(message: string): string {
  return message && errorText(message)
}

/** Session list of the selected period: search, sort, paging, and a row per session. */
export function Sessions({ api, route, navigate, range, refresh }: ViewProps) {
  const { sort, search } = parseListState(route.tab)
  const searchId = useId()
  const sortId = useId()

  // The box shows what is being typed; the route holds what was applied.
  const [draft, setDraft] = useState(search)
  const applied = useRef(search)
  useEffect(() => {
    // Back, forward or a pasted address changed the search from outside.
    if (search === applied.current) return
    applied.current = search
    setDraft(search)
  }, [search])
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const apply = (next: { sort: SessionSort; search: string }) => {
    // An earlier keystroke must not fire later and undo this change.
    clearTimeout(timer.current)
    applied.current = normalizeSearch(next.search)
    navigate({ ...route, tab: encodeListState(next) })
  }
  useEffect(() => {
    const next = normalizeSearch(draft)
    if (next === search) return
    timer.current = setTimeout(() => apply({ sort, search: next }), SEARCH_DEBOUNCE_MS)
    return () => clearTimeout(timer.current)
    // apply only closes over route and navigate, which are listed here.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [draft, search, sort, route, navigate])
  const clearSearch = () => {
    setDraft('')
    apply({ sort, search: '' })
  }

  const list = useSessionPages(api, { range, project: route.project, sort, search }, refresh)
  const open = (sessionId: string) => navigate({ view: 'session', sessionId })

  return (
    <>
      <CoverageNote coverage={list.coverage} />
      <div className="u-sess-controls">
        <span className="u-sess-field">
          <label htmlFor={searchId}>{t('sessions.searchLabel')}</label>
          <input
            id={searchId}
            className="u-sess-search"
            type="search"
            value={draft}
            maxLength={MAX_SEARCH_LEN}
            placeholder={t('sessions.searchPlaceholder')}
            autoComplete="off"
            spellCheck={false}
            onChange={(e) => setDraft(e.target.value)}
          />
        </span>
        <span className="u-sess-field">
          <label htmlFor={sortId}>{t('sessions.sortLabel')}</label>
          <select
            id={sortId}
            value={sort}
            onChange={(e) => apply({ sort: e.target.value as SessionSort, search: draft })}
          >
            {sortOptions.map((o) => (
              <option key={o.id} value={o.id}>
                {t(o.labelKey)}
              </option>
            ))}
          </select>
        </span>
        <p className="u-sess-count" role="status">
          {list.ready && !list.stale ? (
            <>
              {t(search ? 'sessions.countMatching' : 'sessions.countInPeriod')}{' '}
              <b>{t('sessions.countValue', { count: list.total, n: fmtInt(list.total) })}</b>
            </>
          ) : (
            list.loading && t('sessions.loading')
          )}
        </p>
      </div>
      <ErrorNote error={listError(list.error)} retry={list.reload} />
      {list.ready && (
        <Card className="u-sess-card">
          {list.rows.length === 0 ? (
            <>
              <Empty coverage={list.coverage}>{searchEmptyText(search, list.coverage)}</Empty>
              {search && (
                <button type="button" onClick={clearSearch}>
                  {t('sessions.clearSearch')}
                </button>
              )}
            </>
          ) : (
            <>
              <div className="u-sess-legend-row">
                <span>{t('sessions.tokenBar')}</span>
                <SplitLegend />
              </div>
              <SessionTable rows={list.rows} sort={sort} busy={list.stale} onOpen={open} />
              <div className="u-sess-pager">
                <span>
                  {t('sessions.shown', { shown: fmtInt(list.rows.length), total: fmtInt(list.total) })}
                </span>
                {list.hasMore && (
                  <button
                    type="button"
                    onClick={list.more}
                    disabled={list.loading || list.moreLoading}
                  >
                    {list.moreLoading ? t('sessions.moreLoading') : t('sessions.more')}
                  </button>
                )}
              </div>
              <ErrorNote error={listError(list.moreError)} retry={list.more} />
            </>
          )}
        </Card>
      )}
    </>
  )
}
