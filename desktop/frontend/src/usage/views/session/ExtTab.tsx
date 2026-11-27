import { useMemo } from 'react'
import { t } from '../../../i18n'
import type { UsageApi } from '../../api'
import type { ExtPage, GroupUsage, TimelineItem } from '../../types'
import { Card, DataTable, Empty, ErrorNote, fmtInt, fmtMs, type Column } from '../../ui'
import { describeError } from './errors'
import { ALL_TIME, countVars } from './format'
import { TimelineRow } from './TimelineRow'
import { usePaged } from './usePaged'

function sourceColumns(): Column<GroupUsage>[] {
  return [
    { key: 'name', label: t('session.colSource'), render: (g) => <code className="u-sd-name">{g.name}</code> },
    { key: 'calls', label: t('session.colEvents'), align: 'right', render: (g) => fmtInt(g.calls) },
    {
      key: 'errors',
      label: t('session.colErrors'),
      align: 'right',
      render: (g) => (g.errors > 0 ? <b className="u-tone-bad">{fmtInt(g.errors)}</b> : fmtInt(g.errors)),
    },
    { key: 'avg', label: t('session.colAvg'), align: 'right', render: (g) => fmtMs(g.avgMs) },
  ]
}

/**
 * The extension events tab. It lists every event that names this session,
 * also the ones the timeline could not place next to a tool call.
 */
export function ExtTab(p: { api: UsageApi; sessionId: string; sources: GroupUsage[]; refresh: number }) {
  const { api, sessionId } = p
  const paged = usePaged<ExtPage>(
    (cursor) => api.extEvents({ range: ALL_TIME, sessionId, cursor }),
    [api, sessionId],
    p.refresh,
    'head',
  )
  // The events are drawn with the timeline's row so both tabs read the same.
  const items = useMemo(
    () =>
      paged.pages.flatMap((page) =>
        page.items.map((ext): TimelineItem => ({ id: ext.id, at: ext.at, kind: 'ext', ext })),
      ),
    [paged.pages],
  )
  const total = paged.pages.at(-1)?.total ?? 0
  return (
    <>
      <Card title={t('session.sourcesTitle')}>
        {p.sources.length ? (
          <DataTable columns={sourceColumns()} rows={p.sources} rowKey={(g) => g.name} />
        ) : (
          <Empty>{t('session.noExtSources')}</Empty>
        )}
      </Card>
      <Card
        title={t('session.eventsTitle')}
        hint={t('session.eventsHint')}
        action={
          <button className="u-sd-small" onClick={paged.reload} disabled={paged.loading}>
            {t('session.reload')}
          </button>
        }
      >
        <ErrorNote error={describeError(paged.error)} retry={paged.retry} />
        {paged.loading && !items.length ? (
          <p className="u-empty" role="status">
            {t('session.readingExt')}
          </p>
        ) : items.length ? (
          <ol className="u-sd-events" aria-label={t('session.extListLabel')}>
            {items.map((item) => (
              <TimelineRow key={item.id} item={item} agentId="" fullTime />
            ))}
          </ol>
        ) : (
          !paged.error && <Empty>{t('session.noExtEvents')}</Empty>
        )}
        {items.length > 0 && (
          <div className="u-sd-more">
            <span className="u-hint" role="status">
              {paged.loadingMore
                ? t('session.readingOlder')
                : t('session.extLoaded', { ...countVars(items.length), total: fmtInt(total) })}
            </span>
            {paged.nextCursor && (
              <button onClick={paged.more} disabled={paged.loadingMore}>
                {t('session.more')}
              </button>
            )}
          </div>
        )}
      </Card>
    </>
  )
}
