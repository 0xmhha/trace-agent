import { Fragment, useState } from 'react'
import type { ExtEvent, ExtLevel } from '../../types'
import { Card, Empty, ErrorNote, Pill, fmtDateTime, fmtInt, fmtMs } from '../../ui'
import {
  canOpenSession,
  fmtPrecise,
  levels,
  noFilter,
  statusPill,
  type EventFilter,
} from './model'
import type { EventPages } from './useEventPages'
import { t } from '../../../i18n'

const COLUMN_COUNT = 7
// Row positions make a valid element ID whatever characters an event ID holds.
const DETAIL_ID_PREFIX = 'u-ext-detail-'

function detailFields(event: ExtEvent): [label: string, value: string][] {
  const fields: [string, string | undefined][] = [
    [t('extensions.field.at'), fmtPrecise(event.at)],
    [t('extensions.field.receivedAt'), fmtPrecise(event.receivedAt)],
    [t('extensions.field.level'), event.level],
    [
      t('extensions.field.value'),
      event.value == null ? undefined : event.value + (event.unit ? ' ' + event.unit : ''),
    ],
    [t('extensions.field.sessionId'), event.sessionId],
    [t('extensions.field.toolUseId'), event.toolUseId],
    [t('extensions.field.traceId'), event.traceId],
    [t('extensions.field.spanId'), event.spanId],
    [t('extensions.field.parentSpanId'), event.parentSpanId],
  ]
  return fields.filter((field): field is [string, string] => Boolean(field[1]))
}

/** Message, attributes and identifiers of one event, all as plain text. */
function EventDetail({ event }: { event: ExtEvent }) {
  const attributes = Object.entries(event.attributes ?? {}).sort(([a], [b]) => a.localeCompare(b))
  return (
    <div className="u-ext-detail-body">
      <section>
        <h3>{t('extensions.detail.message')}</h3>
        {event.message ? (
          <pre className="u-ext-text">{event.message}</pre>
        ) : (
          <p className="u-tone-muted">{t('extensions.detail.noMessage')}</p>
        )}
      </section>
      <section>
        <h3>{t('extensions.detail.attributes')}</h3>
        {attributes.length ? (
          <dl className="u-ext-fields">
            {attributes.map(([key, value]) => (
              <Fragment key={key}>
                <dt>{key}</dt>
                <dd>{value}</dd>
              </Fragment>
            ))}
          </dl>
        ) : (
          <p className="u-tone-muted">{t('extensions.detail.noAttributes')}</p>
        )}
      </section>
      <section>
        <h3>{t('extensions.detail.ids')}</h3>
        <dl className="u-ext-fields">
          {detailFields(event).map(([label, value]) => (
            <Fragment key={label}>
              <dt>{label}</dt>
              <dd>{value}</dd>
            </Fragment>
          ))}
        </dl>
      </section>
    </div>
  )
}

/** Filterable, pageable list of received events, newest first. */
export function EventsCard(p: {
  pages: EventPages
  filter: EventFilter
  onFilter: (filter: EventFilter) => void
  /** Senders offered by the source filter. */
  sources: string[]
  endpointOn: boolean
  onSession: (sessionId: string) => void
}) {
  const { pages, filter, onFilter } = p
  const [open, setOpen] = useState<ReadonlySet<string>>(new Set())
  const toggle = (id: string) =>
    setOpen((prev) => {
      const next = new Set(prev)
      if (!next.delete(id)) next.add(id)
      return next
    })
  const filtered = filter.source !== '' || filter.level !== ''
  // A source kept in the address can be missing from this range's senders.
  const sources =
    filter.source && !p.sources.includes(filter.source) ? [filter.source, ...p.sources] : p.sources

  return (
    <Card title={t('extensions.events.title')} hint={t('extensions.events.hint')}>
      <div className="u-ext-filters">
        <label>
          {t('extensions.filter.source')}
          <select
            value={filter.source}
            onChange={(e) => onFilter({ ...filter, source: e.target.value })}
          >
            <option value="">{t('extensions.filter.all')}</option>
            {sources.map((source) => (
              <option key={source} value={source}>
                {source}
              </option>
            ))}
          </select>
        </label>
        <label>
          {t('extensions.filter.level')}
          <select
            value={filter.level}
            onChange={(e) => onFilter({ ...filter, level: e.target.value as ExtLevel | '' })}
          >
            <option value="">{t('extensions.filter.all')}</option>
            {levels.map((level) => (
              <option key={level} value={level}>
                {level}
              </option>
            ))}
          </select>
        </label>
        {filtered && (
          <button type="button" onClick={() => onFilter(noFilter)}>
            {t('extensions.filter.clear')}
          </button>
        )}
        <span className="u-ext-count" role="status">
          {!pages.loaded
            ? pages.error
              ? ''
              : t('extensions.count.loading')
            : pages.items.length < pages.total
              ? t('extensions.count.partial', {
                  total: fmtInt(pages.total),
                  shown: fmtInt(pages.items.length),
                })
              : t('extensions.count.total', { count: pages.total, total: fmtInt(pages.total) })}
        </span>
      </div>
      <ErrorNote error={pages.error} retry={pages.reload} />
      {!pages.loaded ? (
        !pages.error && (
          <Empty>
            <span role="status">{t('extensions.events.loading')}</span>
          </Empty>
        )
      ) : pages.items.length === 0 ? (
        <Empty>
          {filtered
            ? t('extensions.events.noMatch')
            : p.endpointOn
              ? t('extensions.events.none')
              : t('extensions.events.noneOff')}
        </Empty>
      ) : (
        <div className="u-table-wrap">
          <table
            className={'u-table u-ext-events' + (pages.loading ? ' u-ext-stale' : '')}
            aria-label={t('extensions.events.aria')}
            aria-busy={pages.loading}
          >
            <thead>
              <tr>
                <th scope="col">{t('extensions.col.time')}</th>
                <th scope="col">{t('extensions.col.source')}</th>
                <th scope="col" className="u-ext-col-kind">
                  {t('extensions.col.kind')}
                </th>
                <th scope="col">{t('extensions.col.name')}</th>
                <th scope="col">{t('extensions.col.status')}</th>
                <th scope="col" className="num">
                  {t('extensions.col.duration')}
                </th>
                <th scope="col">{t('extensions.col.session')}</th>
              </tr>
            </thead>
            <tbody>
              {pages.items.map((event, index) => {
                const expanded = open.has(event.id)
                const detailId = DETAIL_ID_PREFIX + index
                const pill = statusPill(event.status)
                const sessionId = canOpenSession(event.sessionId) ? event.sessionId : null
                return (
                  <Fragment key={event.id}>
                    <tr className={expanded ? 'u-ext-open' : ''}>
                      <td className="u-ext-nowrap">{fmtDateTime(event.at)}</td>
                      <td>{event.source}</td>
                      <td className="u-ext-col-kind">{event.kind}</td>
                      <td>
                        <button
                          type="button"
                          className="u-ext-expander"
                          aria-expanded={expanded}
                          aria-controls={expanded ? detailId : undefined}
                          onClick={() => toggle(event.id)}
                        >
                          <span className="u-ext-chevron" aria-hidden="true">
                            {expanded ? '▾' : '▸'}
                          </span>
                          <span>
                            {event.name}
                            <span className="u-ext-inline-kind"> · {event.kind}</span>
                          </span>
                        </button>
                      </td>
                      <td>
                        <Pill tone={pill.tone}>{pill.label}</Pill>
                      </td>
                      <td className="num">{fmtMs(event.durationMs)}</td>
                      <td className="u-ext-nowrap">
                        {sessionId ? (
                          <button
                            type="button"
                            className="u-ext-link"
                            title={sessionId}
                            onClick={() => p.onSession(sessionId)}
                          >
                            {t('extensions.session.open')}
                          </button>
                        ) : event.sessionId ? (
                          <span title={event.sessionId}>{t('extensions.session.linked')}</span>
                        ) : (
                          <span className="u-tone-muted">{t('extensions.session.unlinked')}</span>
                        )}
                      </td>
                    </tr>
                    {expanded && (
                      <tr className="u-ext-detail" id={detailId}>
                        <td colSpan={COLUMN_COUNT}>
                          <EventDetail event={event} />
                        </td>
                      </tr>
                    )}
                  </Fragment>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
      {pages.hasMore && (
        <div className="u-ext-more">
          <button type="button" disabled={pages.loadingMore} onClick={pages.more}>
            {pages.loadingMore ? t('extensions.more.loading') : t('extensions.more')}
          </button>
        </div>
      )}
    </Card>
  )
}
