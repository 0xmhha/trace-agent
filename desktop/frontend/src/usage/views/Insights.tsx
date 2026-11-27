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
import { t } from '../../i18n'

/** The insights view: what the local rules found and what they recommend. */
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
          {t('insights.loading')}
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
                  ? t('insights.shown.filtered', {
                      total: fmtInt(data.items.length),
                      shown: fmtInt(shown.length),
                    })
                  : t('insights.shown.all', {
                      count: data.items.length,
                      total: fmtInt(data.items.length),
                    })}
                {data.rulesSkipped > 0 &&
                  ' ' +
                    t('insights.shown.skipped', {
                      count: data.rulesSkipped,
                      n: fmtInt(data.rulesSkipped),
                    })}
              </p>
              {groups.length === 0 && (
                <Card>
                  <p className="u-empty">{t('insights.noMatch')}</p>
                  <button
                    type="button"
                    className="u-ins-clear"
                    onClick={() => setFilter(noFilter)}
                  >
                    {t('insights.clearFilters')}
                  </button>
                </Card>
              )}
              {groups.map((group) => (
                <section
                  key={group.category}
                  className="u-ins-group"
                  aria-label={t('insights.group.aria', { label: group.label })}
                >
                  <div className="u-ins-group-head">
                    <h2>{group.label}</h2>
                    <span className="u-count">
                      {t('insights.group.count', {
                        count: group.items.length,
                        n: fmtInt(group.items.length),
                      })}
                    </span>
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
