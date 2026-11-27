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

const count = (n: number) => fmtInt(n) + '건'

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
    <Card title="출처별 현황" hint="선택한 기간에 이벤트를 보낸 도구입니다.">
      <ErrorNote error={overview.error} retry={overview.reload} />
      {data && (
        <div className="u-stats u-ext-stats">
          <Stat label="출처" value={fmtInt(rows.length) + '개'} />
          <Stat label="이벤트" value={count(events)} />
          <Stat
            label="오류"
            value={count(errors)}
            hint={'오류율 ' + fmtPct(ratio(errors, events))}
            tone={errors > 0 ? 'bad' : 'plain'}
          />
          <Stat label="미연계" value={count(unlinked)} hint="세션 ID 없이 온 이벤트" />
          <Stat
            label="거부된 입력"
            value={count(rejected)}
            hint="기간과 무관한 누적"
            tone={rejected > 0 ? 'warn' : 'plain'}
          />
        </div>
      )}
      {rejected > 0 && (
        <div className="u-note u-note-warn">
          받지 않고 거부한 입력이 {count(rejected)} 있습니다. 대개 전송 형식이 맞지 않을 때
          생깁니다. 필수 필드와 값의 형식을 확인해 주세요. 출처를 알 수 없는 입력도 이 수에
          들어갑니다.
        </div>
      )}
      {!data ? (
        !overview.error && (
          <Empty>
            <span role="status">출처를 읽는 중입니다.</span>
          </Empty>
        )
      ) : rows.length === 0 ? (
        <Empty>
          {p.endpointOn
            ? '이 기간에 받은 이벤트가 없습니다. 아래 연결 방법대로 보내면 여기에 나타납니다.'
            : '이 기간에 받은 이벤트가 없습니다. 수신기가 꺼져 있어 새 이벤트를 받지 않습니다.'}
        </Empty>
      ) : (
        <div className="u-table-wrap">
          <table className="u-table u-ext-sources" aria-label="출처별 현황">
            <thead>
              <tr>
                <th scope="col">출처</th>
                <th scope="col" className="num">
                  이벤트
                </th>
                <th scope="col" className="num">
                  오류
                </th>
                <th scope="col" className="num">
                  연계 / 미연계
                </th>
                <th scope="col" className="num">
                  거부된 입력
                </th>
                <th scope="col">마지막 수신</th>
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
                        title={picked ? '이 출처 필터 해제' : '이 출처의 이벤트만 보기'}
                      >
                        {row.source}
                      </button>
                      {picked && <Pill>선택됨</Pill>}
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
