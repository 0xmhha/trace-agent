import { useMemo } from 'react'
import type { UsageApi } from '../../api'
import type { ExtPage, GroupUsage, TimelineItem } from '../../types'
import { Card, DataTable, Empty, ErrorNote, fmtInt, fmtMs, type Column } from '../../ui'
import { describeError } from './errors'
import { ALL_TIME } from './format'
import { TimelineRow } from './TimelineRow'
import { usePaged } from './usePaged'

const sourceColumns: Column<GroupUsage>[] = [
  { key: 'name', label: '출처', render: (g) => <code className="u-sd-name">{g.name}</code> },
  { key: 'calls', label: '이벤트', align: 'right', render: (g) => fmtInt(g.calls) },
  {
    key: 'errors',
    label: '오류',
    align: 'right',
    render: (g) => (g.errors > 0 ? <b className="u-tone-bad">{fmtInt(g.errors)}</b> : fmtInt(g.errors)),
  },
  { key: 'avg', label: '평균 시간', align: 'right', render: (g) => fmtMs(g.avgMs) },
]

/**
 * The 확장 이벤트 tab. It lists every event that names this session, also
 * the ones the timeline could not place next to a tool call.
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
      <Card title="출처별 요약">
        {p.sources.length ? (
          <DataTable columns={sourceColumns} rows={p.sources} rowKey={(g) => g.name} />
        ) : (
          <Empty>이 세션에 연결된 확장 이벤트가 없습니다.</Empty>
        )}
      </Card>
      <Card
        title="이벤트"
        hint="새 이벤트부터 보여 줍니다. 도구 호출에 연결되지 않은 이벤트도 포함합니다."
        action={
          <button className="u-sd-small" onClick={paged.reload} disabled={paged.loading}>
            다시 읽기
          </button>
        }
      >
        <ErrorNote error={describeError(paged.error)} retry={paged.retry} />
        {paged.loading && !items.length ? (
          <p className="u-empty" role="status">
            확장 이벤트를 읽는 중입니다.
          </p>
        ) : items.length ? (
          <ol className="u-sd-events" aria-label="확장 이벤트">
            {items.map((item) => (
              <TimelineRow key={item.id} item={item} agentId="" fullTime />
            ))}
          </ol>
        ) : (
          !paged.error && <Empty>이 세션 ID로 들어온 확장 이벤트가 없습니다.</Empty>
        )}
        {items.length > 0 && (
          <div className="u-sd-more">
            <span className="u-hint" role="status">
              {paged.loadingMore
                ? '이전 이벤트를 읽는 중입니다.'
                : `전체 ${fmtInt(total)}건 중 ${fmtInt(items.length)}건을 불러왔습니다.`}
            </span>
            {paged.nextCursor && (
              <button onClick={paged.more} disabled={paged.loadingMore}>
                더 보기
              </button>
            )}
          </div>
        )}
      </Card>
    </>
  )
}
