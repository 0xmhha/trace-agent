import type { ReactNode } from 'react'
import type { UsageView, ViewProps } from '../index'
import type { ModelUsage, Overview, ToolCategory, ToolUsage } from '../types'
import {
  Card,
  CoverageNote,
  Empty,
  ErrorNote,
  fmtCompact,
  fmtPct,
  ratio,
  sumTokens,
  useLoad,
} from '../ui'
import { BarList, type BarRow } from './dashboard/BarList'
import { CacheMeter } from './dashboard/CacheMeter'
import { HeatMap } from './dashboard/HeatMap'
import { InsightTeaser } from './dashboard/InsightTeaser'
import { KpiRow } from './dashboard/KpiRow'
import { ProjectTable } from './dashboard/ProjectTable'
import { TrendChart, measureTab, parseMeasure } from './dashboard/TrendChart'
import { UNIT, withUnit } from './dashboard/format'
import { Legend } from './dashboard/parts'
import './dashboard.css'

/** Rows of the ranked lists; the tools view has the full tables. */
const MODEL_ROWS = 6
const TOOL_ROWS = 8

/** Kind shown after a tool name. Built-in tools need no note. */
const TOOL_NOTES: Record<ToolCategory, string> = {
  builtin: '',
  mcp: 'MCP',
  skill: '스킬',
  agent: '하위 에이전트',
}

function modelRows(models: readonly ModelUsage[]): BarRow[] {
  return [...models]
    .sort((a, b) => sumTokens(b.tokens) - sumTokens(a.tokens))
    .slice(0, MODEL_ROWS)
    .map((m) => ({
      key: m.model,
      name: m.model || '모델 미상',
      value: sumTokens(m.tokens),
      figure: `${fmtCompact(sumTokens(m.tokens))} ${UNIT.tokens} · 호출 ${withUnit(m.calls, UNIT.times)}`,
    }))
}

function toolRows(tools: readonly ToolUsage[]): BarRow[] {
  return [...tools]
    .sort((a, b) => b.calls - a.calls)
    .slice(0, TOOL_ROWS)
    .map((t) => ({
      key: t.category + ':' + t.name,
      name: t.name,
      note: TOOL_NOTES[t.category] || undefined,
      value: t.calls,
      failed: t.errors,
      figure: `${withUnit(t.calls, UNIT.times)} · 실패 ${fmtPct(ratio(t.errors, t.calls))}`,
    }))
}

function Cards(
  p: { overview: Overview; teaser: ReactNode } & Pick<ViewProps, 'route' | 'navigate'>,
) {
  const { overview: o, route, navigate } = p
  const models = modelRows(o.models)
  const tools = toolRows(o.tools)
  // Leaving the dashboard keeps the period and project but drops its own tab.
  const open = (view: UsageView, project = route.project) =>
    navigate({ view, days: route.days, project })

  return (
    <>
      <KpiRow overview={o} onSessions={() => open('sessions')} onTools={() => open('tools')} />
      <div className="u-grid u-dash-grid">
        <TrendChart
          className="u-span-12"
          series={o.series}
          measure={parseMeasure(route.tab)}
          onMeasure={(id) => navigate({ ...route, tab: measureTab(id) })}
          coverage={o.coverage}
        />
        <Card title="모델별 사용" hint="토큰 합계 순" className="u-span-6">
          {models.length ? (
            <BarList rows={models} label="모델별 토큰과 호출 수" />
          ) : (
            <Empty coverage={o.coverage} />
          )}
        </Card>
        <Card title="캐시 적중률" className="u-span-6">
          <CacheMeter hitRatio={o.cacheHitRatio} tokens={o.tokens} coverage={o.coverage} />
        </Card>
        <Card
          title="많이 쓴 도구"
          hint="호출 수 순"
          className="u-span-6"
          action={
            <button type="button" className="u-dash-link" onClick={() => open('tools')}>
              전체 보기
            </button>
          }
        >
          {tools.length ? (
            <>
              <Legend
                label="막대 색"
                items={[
                  { key: 'ok', paint: 's1', label: '성공' },
                  { key: 'failed', paint: 'bad', label: '실패' },
                ]}
              />
              <BarList rows={tools} label="도구별 호출 수와 실패율" />
            </>
          ) : (
            <Empty coverage={o.coverage} />
          )}
        </Card>
        <Card
          title="활발한 프로젝트"
          hint="행을 누르면 그 프로젝트의 세션을 봅니다"
          className="u-span-6"
        >
          <ProjectTable
            projects={o.projects}
            coverage={o.coverage}
            onOpen={(project) => open('sessions', project)}
          />
        </Card>
        <Card title="요일과 시간" hint="프롬프트 수, 이 Mac의 시각 기준" className="u-span-8">
          <HeatMap cells={o.heatmap} coverage={o.coverage} />
        </Card>
        {p.teaser}
      </div>
    </>
  )
}

/** The 대시보드 view: headline numbers, trend, breakdowns and an insight preview. */
export function Dashboard({ api, route, navigate, range, refresh }: ViewProps) {
  const query = { range, project: route.project }
  const deps = [api, range, route.project, refresh]
  const overview = useLoad(() => api.overview(query), deps)
  const insights = useLoad(() => api.insights(query), deps)
  const teaser = (
    <InsightTeaser
      className="u-span-4"
      insights={insights}
      onOpen={() => navigate({ view: 'insights', days: route.days, project: route.project })}
    />
  )

  return (
    <div className="u-dash">
      <CoverageNote coverage={overview.data?.coverage} />
      <ErrorNote error={overview.error} retry={overview.reload} />
      {!overview.data && overview.loading && (
        <p className="u-empty" role="status">
          대시보드를 불러오는 중입니다.
        </p>
      )}
      {overview.data && (
        // Old numbers stay on screen while newer ones load; they are dimmed
        // so a refresh neither blanks the page nor moves the layout.
        <div className={'u-dash-body' + (overview.loading ? ' u-dash-stale' : '')}>
          <Cards overview={overview.data} teaser={teaser} route={route} navigate={navigate} />
        </div>
      )}
      {/* Insights load on their own, so they still show when the overview failed. */}
      {!overview.data && !overview.loading && teaser}
    </div>
  )
}
