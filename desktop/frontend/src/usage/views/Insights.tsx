import type { ViewProps } from '../index'
import { Card, CoverageNote, ErrorNote, fmtInt, useLoad } from '../ui'
import { Filters } from './insights/Filters'
import { InsightCard } from './insights/InsightCard'
import { NothingFired } from './insights/NothingFired'
import { Summary } from './insights/Summary'
import {
  applyFilter,
  filterTab,
  groupInsights,
  isFiltered,
  noFilter,
  parseFilter,
  type InsightFilter,
} from './insights/model'
import './insights.css'

/** The 인사이트 view: what the local rules found and what they recommend. */
export function Insights({ api, route, navigate, range, refresh }: ViewProps) {
  const report = useLoad(
    () => api.insights({ range, project: route.project }),
    [api, range, route.project, refresh],
  )
  const data = report.data
  // The filters live in the route so reload and back restore them.
  const filter = parseFilter(route.tab)
  const setFilter = (next: InsightFilter) => navigate({ ...route, tab: filterTab(next) })

  const shown = data ? applyFilter(data.items, filter) : []
  const groups = groupInsights(shown)

  return (
    <div className="u-ins" aria-busy={report.loading}>
      <ErrorNote error={report.error} retry={report.reload} />
      {!data && report.loading && (
        <p className="u-empty" role="status">
          인사이트를 읽는 중입니다.
        </p>
      )}
      {data && (
        <>
          <CoverageNote coverage={data.coverage} />
          <Summary report={data} />
          {data.items.length === 0 ? (
            <NothingFired report={data} />
          ) : (
            <>
              <Filters items={data.items} value={filter} onChange={setFilter} />
              <p className="u-ins-shown" role="status">
                {isFiltered(filter)
                  ? `인사이트 ${fmtInt(data.items.length)}건 가운데 ${fmtInt(shown.length)}건을 보고 있습니다.`
                  : `인사이트 ${fmtInt(data.items.length)}건을 찾았습니다.`}
                {data.rulesSkipped > 0 &&
                  ` 규칙 ${fmtInt(data.rulesSkipped)}개는 자료가 모자라 아직 판단하지 못했습니다.`}
              </p>
              {groups.length === 0 && (
                <Card>
                  <p className="u-empty">선택한 조건에 맞는 인사이트가 없습니다.</p>
                  <button
                    type="button"
                    className="u-ins-clear"
                    onClick={() => setFilter(noFilter)}
                  >
                    필터 지우기
                  </button>
                </Card>
              )}
              {groups.map((group) => (
                <section
                  key={group.category}
                  className="u-ins-group"
                  aria-label={group.label + ' 인사이트'}
                >
                  <div className="u-ins-group-head">
                    <h2>{group.label}</h2>
                    <span className="u-count">{fmtInt(group.items.length)}건</span>
                    {group.hint && <span className="u-hint">{group.hint}</span>}
                  </div>
                  <div className="u-ins-list">
                    {group.items.map((insight) => (
                      <InsightCard
                        key={insight.id}
                        insight={insight}
                        onOpenSession={(sessionId) => navigate({ view: 'session', sessionId })}
                      />
                    ))}
                  </div>
                </section>
              ))}
            </>
          )}
        </>
      )}
    </div>
  )
}
