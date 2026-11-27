import { useMemo } from 'react'
import { t, type MessageKey } from '../../../i18n'
import type { UsageApi } from '../../api'
import type { AgentRow, SourceState, TimelineKind, TimelinePage } from '../../types'
import { Card, Empty, ErrorNote, fmtInt } from '../../ui'
import { describeError } from './errors'
import { agentLabel, countVars, kindLabels, kindOrder, labelOf, rootAgentLabel } from './format'
import type { SessionView } from './route'
import { TimelineRow, isErrorItem } from './TimelineRow'
import { usePaged } from './usePaged'

const sourceEmpty: Record<Exclude<SourceState, 'available'>, MessageKey> = {
  missing: 'session.sourceEmptyMissing',
  unreadable: 'session.sourceEmptyUnreadable',
}

/** The conversation tab: one transcript stream as a paged, filterable timeline. */
export function Conversation(p: {
  api: UsageApi
  sessionId: string
  agents: AgentRow[]
  view: SessionView
  onView: (view: SessionView) => void
  refresh: number
}) {
  const { api, sessionId, agents, view, onView } = p
  const { agentId, hidden, errorsOnly } = view
  const paged = usePaged<TimelinePage>(
    (cursor) => api.timeline({ sessionId, agentId: agentId || undefined, cursor }),
    [api, sessionId, agentId],
    p.refresh,
    'tail',
  )
  const { items, skipped, counts } = useMemo(() => {
    const all = paged.pages.flatMap((page) => page.items)
    const perKind = new Map<TimelineKind, number>()
    for (const item of all) perKind.set(item.kind, (perKind.get(item.kind) ?? 0) + 1)
    return {
      items: all,
      skipped: paged.pages.reduce((sum, page) => sum + page.skipped, 0),
      counts: perKind,
    }
  }, [paged.pages])
  const source: SourceState = paged.pages.at(-1)?.source ?? 'available'

  // The position in the loaded list is part of the key, so a row keeps its
  // open or closed state when a filter hides its neighbours.
  const shown = items
    .map((item, index) => ({ item, index }))
    .filter(({ item }) => !hidden.has(item.kind) && (!errorsOnly || isErrorItem(item)))
  const filtered = hidden.size > 0 || errorsOnly
  const hasItems = items.length > 0
  const known = agents.some((agent) => agent.agentId === agentId)
  const subagents = agents.filter((agent) => agent.agentId)

  const toggleKind = (kind: TimelineKind) => {
    const next = new Set(hidden)
    if (!next.delete(kind)) next.add(kind)
    onView({ ...view, hidden: next })
  }

  return (
    <Card>
      <div className="u-sd-controls">
        <label className="u-sd-control">
          {t('session.agentFilter')}
          <select value={agentId} onChange={(e) => onView({ ...view, agentId: e.target.value })}>
            <option value="">{rootAgentLabel()}</option>
            {subagents.map((agent) => (
              <option key={agent.agentId} value={agent.agentId}>
                {agentLabel(agent)}
              </option>
            ))}
            {agentId && !known && (
              <option value={agentId}>{t('session.unknownAgent', { id: agentId })}</option>
            )}
          </select>
        </label>
        {hasItems && (
          <label className="u-sd-check">
            <input
              type="checkbox"
              checked={errorsOnly}
              onChange={(e) => onView({ ...view, errorsOnly: e.target.checked })}
            />
            {t('session.errorsOnly')}
          </label>
        )}
        {hasItems && filtered && (
          <button
            className="u-sd-small"
            onClick={() => onView({ ...view, hidden: new Set(), errorsOnly: false })}
          >
            {t('session.clearFilters')}
          </button>
        )}
      </div>
      {/* Filters and the masking notice only matter once there is text to show. */}
      {hasItems && (
        <>
          <fieldset className="u-sd-kinds">
            <legend>{t('session.kindsLegend')}</legend>
            {kindOrder.map((kind) => (
              <label key={kind} className="u-sd-check">
                <input type="checkbox" checked={!hidden.has(kind)} onChange={() => toggleKind(kind)} />
                {labelOf(kindLabels, kind)}
                <span className="u-count">{fmtInt(counts.get(kind) ?? 0)}</span>
              </label>
            ))}
          </fieldset>
          <p className="u-note u-sd-mask-note">{t('session.maskNotice')}</p>
        </>
      )}
      {skipped > 0 && (
        <p className="u-note u-note-warn" role="status">
          {t('session.skippedLines', countVars(skipped))}
        </p>
      )}
      <ErrorNote error={describeError(paged.error)} retry={paged.retry} />

      {paged.loading && !hasItems ? (
        <p className="u-empty" role="status">
          {t('session.readingConversation')}
        </p>
      ) : !hasItems ? (
        source !== 'available' ? (
          <Empty>{t(sourceEmpty[source])}</Empty>
        ) : (
          !paged.error && <Empty>{t('session.noItems')}</Empty>
        )
      ) : !shown.length ? (
        <Empty>
          {t(paged.nextCursor ? 'session.noneMatchMore' : 'session.noneMatch', countVars(items.length))}
        </Empty>
      ) : (
        <ol className="u-sd-timeline" aria-label={t('session.timelineLabel')}>
          {shown.map(({ item, index }) => (
            <TimelineRow key={index + ':' + item.id} item={item} agentId={agentId} />
          ))}
        </ol>
      )}

      {hasItems && (
        <div className="u-sd-more">
          <span className="u-hint" role="status">
            {paged.loadingMore
              ? t('session.readingMore')
              : filtered
                ? t('session.shownOfLoaded', { loaded: fmtInt(items.length), shown: fmtInt(shown.length) })
                : t('session.loadedItems', countVars(items.length))}
            {!paged.loadingMore && !paged.nextCursor && ' ' + t('session.endOfRecord')}
          </span>
          {paged.nextCursor && (
            <button onClick={paged.more} disabled={paged.loadingMore}>
              {t('session.more')}
            </button>
          )}
        </div>
      )}
    </Card>
  )
}
