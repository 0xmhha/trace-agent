import type { ExtOverview, ExtSourceRow } from '../../types'
import {
  Card,
  Empty,
  ErrorNote,
  Pill,
  Stat,
  fmtDateTime,
  fmtInt,
  fmtPct,
  ratio,
  type Loaded,
} from '../../ui'
import { t } from '../../../i18n'

const count = (n: number) => t('extensions.amount', { n: fmtInt(n) })

function sum(rows: ExtSourceRow[], pick: (row: ExtSourceRow) => number): number {
  return rows.reduce((total, row) => total + pick(row), 0)
}

/**
 * Per-sender totals of the range. Picking a row narrows the event list to
 * that sender; picking it again clears the filter.
 */
export function SourcesCard(p: {
  overview: Loaded<ExtOverview>
  endpointOn: boolean
  selected: string
  onSelect: (source: string) => void
}) {
  const { overview, selected } = p
  const data = overview.data
  const rows = data?.sources ?? []
  const events = sum(rows, (r) => r.events),
    errors = sum(rows, (r) => r.errors),
    unlinked = sum(rows, (r) => r.unlinked),
    rejected = data?.rejected ?? 0

  return (
    <Card title={t('extensions.sources.title')} hint={t('extensions.sources.hint')}>
      <ErrorNote error={overview.error} retry={overview.reload} />
      {data && (
        <div className="u-stats u-ext-stats">
          <Stat
            label={t('extensions.stat.sources')}
            value={t('extensions.stat.sourcesValue', { n: fmtInt(rows.length) })}
          />
          <Stat label={t('extensions.stat.events')} value={count(events)} />
          <Stat
            label={t('extensions.stat.errors')}
            value={count(errors)}
            hint={t('extensions.stat.errorRate', { rate: fmtPct(ratio(errors, events)) })}
            tone={errors > 0 ? 'bad' : 'plain'}
          />
          <Stat
            label={t('extensions.stat.unlinked')}
            value={count(unlinked)}
            hint={t('extensions.stat.unlinkedHint')}
          />
          <Stat
            label={t('extensions.stat.rejected')}
            value={count(rejected)}
            hint={t('extensions.stat.rejectedHint')}
            tone={rejected > 0 ? 'warn' : 'plain'}
          />
        </div>
      )}
      {rejected > 0 && (
        <div className="u-note u-note-warn">
          {t('extensions.rejectedNote', { count: rejected, n: fmtInt(rejected) })}
        </div>
      )}
      {!data ? (
        !overview.error && (
          <Empty>
            <span role="status">{t('extensions.sources.loading')}</span>
          </Empty>
        )
      ) : rows.length === 0 ? (
        <Empty>
          {p.endpointOn
            ? t('extensions.sources.none')
            : t('extensions.events.noneOff')}
        </Empty>
      ) : (
        <div className="u-table-wrap">
          <table className="u-table u-ext-sources" aria-label={t('extensions.sources.title')}>
            <thead>
              <tr>
                <th scope="col">{t('extensions.col.source')}</th>
                <th scope="col" className="num">
                  {t('extensions.col.events')}
                </th>
                <th scope="col" className="num">
                  {t('extensions.col.errors')}
                </th>
                <th scope="col" className="num">
                  {t('extensions.col.linked')}
                </th>
                <th scope="col" className="num">
                  {t('extensions.col.rejected')}
                </th>
                <th scope="col">{t('extensions.col.lastAt')}</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => {
                const picked = row.source === selected
                return (
                  <tr
                    key={row.source}
                    className={'clickable' + (picked ? ' u-ext-picked' : '')}
                    onClick={() => p.onSelect(picked ? '' : row.source)}
                  >
                    <td>
                      {/* The click bubbles to the row, so keyboard and pointer share one handler. */}
                      <button
                        type="button"
                        className="u-ext-rowbtn"
                        aria-pressed={picked}
                        title={picked ? t('extensions.source.unpick') : t('extensions.source.pick')}
                      >
                        {row.source}
                      </button>
                      {picked && <Pill>{t('extensions.source.picked')}</Pill>}
                    </td>
                    <td className="num">{count(row.events)}</td>
                    <td className={'num' + (row.errors > 0 ? ' u-tone-bad' : '')}>
                      {count(row.errors)}
                      <span className="u-ext-sub">{fmtPct(ratio(row.errors, row.events))}</span>
                    </td>
                    <td className="num">
                      {fmtInt(row.linked)} / {count(row.unlinked)}
                    </td>
                    <td className={'num' + (row.rejected > 0 ? ' u-tone-warn' : '')}>
                      {count(row.rejected)}
                    </td>
                    <td>{fmtDateTime(row.lastAt)}</td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  )
}
