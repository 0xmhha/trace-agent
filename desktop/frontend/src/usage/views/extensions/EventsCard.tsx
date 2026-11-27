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

const COLUMN_COUNT = 7
// Row positions make a valid element ID whatever characters an event ID holds.
const DETAIL_ID_PREFIX = 'u-ext-detail-'

function detailFields(event: ExtEvent): [label: string, value: string][] {
  const fields: [string, string | undefined][] = [
    ['발생 시각', fmtPrecise(event.at)],
    ['받은 시각', fmtPrecise(event.receivedAt)],
    ['수준', event.level],
    ['값', event.value == null ? undefined : event.value + (event.unit ? ' ' + event.unit : '')],
    ['세션 ID', event.sessionId],
    ['도구 호출 ID', event.toolUseId],
    ['trace ID', event.traceId],
    ['span ID', event.spanId],
    ['상위 span ID', event.parentSpanId],
  ]
  return fields.filter((field): field is [string, string] => Boolean(field[1]))
}

/** Message, attributes and identifiers of one event, all as plain text. */
function EventDetail({ event }: { event: ExtEvent }) {
  const attributes = Object.entries(event.attributes ?? {}).sort(([a], [b]) => a.localeCompare(b))
  return (
    <div className="u-ext-detail-body">
      <section>
        <h3>메시지</h3>
        {event.message ? (
          <pre className="u-ext-text">{event.message}</pre>
        ) : (
          <p className="u-tone-muted">메시지가 없습니다.</p>
        )}
      </section>
      <section>
        <h3>속성</h3>
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
          <p className="u-tone-muted">속성이 없습니다.</p>
        )}
      </section>
      <section>
        <h3>식별 정보</h3>
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
    <Card title="이벤트" hint="최근 것부터 보여 줍니다. 이름을 누르면 메시지와 속성을 펼칩니다.">
      <div className="u-ext-filters">
        <label>
          출처
          <select
            value={filter.source}
            onChange={(e) => onFilter({ ...filter, source: e.target.value })}
          >
            <option value="">전체</option>
            {sources.map((source) => (
              <option key={source} value={source}>
                {source}
              </option>
            ))}
          </select>
        </label>
        <label>
          수준
          <select
            value={filter.level}
            onChange={(e) => onFilter({ ...filter, level: e.target.value as ExtLevel | '' })}
          >
            <option value="">전체</option>
            {levels.map((level) => (
              <option key={level} value={level}>
                {level}
              </option>
            ))}
          </select>
        </label>
        {filtered && (
          <button type="button" onClick={() => onFilter(noFilter)}>
            필터 지우기
          </button>
        )}
        <span className="u-ext-count" role="status">
          {!pages.loaded
            ? pages.error
              ? ''
              : '읽는 중'
            : pages.items.length < pages.total
              ? `${fmtInt(pages.total)}건 중 ${fmtInt(pages.items.length)}건`
              : `${fmtInt(pages.total)}건`}
        </span>
      </div>
      <ErrorNote error={pages.error} retry={pages.reload} />
      {!pages.loaded ? (
        !pages.error && (
          <Empty>
            <span role="status">이벤트를 읽는 중입니다.</span>
          </Empty>
        )
      ) : pages.items.length === 0 ? (
        <Empty>
          {filtered
            ? '조건에 맞는 이벤트가 없습니다. 필터를 지우면 전체를 볼 수 있습니다.'
            : p.endpointOn
              ? '이 기간에 받은 이벤트가 없습니다.'
              : '이 기간에 받은 이벤트가 없습니다. 수신기가 꺼져 있어 새 이벤트를 받지 않습니다.'}
        </Empty>
      ) : (
        <div className="u-table-wrap">
          <table
            className={'u-table u-ext-events' + (pages.loading ? ' u-ext-stale' : '')}
            aria-label="이벤트 목록"
            aria-busy={pages.loading}
          >
            <thead>
              <tr>
                <th scope="col">시각</th>
                <th scope="col">출처</th>
                <th scope="col" className="u-ext-col-kind">
                  종류
                </th>
                <th scope="col">이름</th>
                <th scope="col">상태</th>
                <th scope="col" className="num">
                  소요
                </th>
                <th scope="col">세션</th>
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
                            세션 보기
                          </button>
                        ) : event.sessionId ? (
                          <span title={event.sessionId}>연계됨</span>
                        ) : (
                          <span className="u-tone-muted">미연계</span>
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
            {pages.loadingMore ? '읽는 중…' : '더 보기'}
          </button>
        </div>
      )}
    </Card>
  )
}
