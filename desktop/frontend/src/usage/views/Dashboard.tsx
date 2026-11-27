import type { ReactNode } from 'react'
import { t, type MessageKey } from '../../i18n'
import type { UsageView, ViewProps } from '../index'
import type { ModelUsage, Overview, ToolCategory, ToolUsage } from '../types'
import {
  Card,
  CoverageNote,
  Empty,
  ErrorNote,
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
import { UNIT, compactWithUnit, withUnit } from './dashboard/format'
import { Legend } from './dashboard/parts'
import './dashboard.css'

/** Rows of the ranked lists; the tools view has the full tables. */
const MODEL_ROWS = 6
const TOOL_ROWS = 8

/** Kind shown after a tool name. Built-in tools need no note. */
const TOOL_NOTES: Record<ToolCategory, MessageKey | null> = {
  builtin: null,
  mcp: 'dashboard.toolKindMcp',
  skill: 'dashboard.toolKindSkill',
  agent: 'dashboard.toolKindAgent',
}

function modelRows(models: readonly ModelUsage[]): BarRow[] {
  return [...models]
    .sort((a, b) => sumTokens(b.tokens) - sumTokens(a.tokens))
    .slice(0, MODEL_ROWS)
    .map((m) => ({
      key: m.model,
      name: m.model || t('dashboard.modelUnknown'),
      value: sumTokens(m.tokens),
      figure: t('dashboard.modelFigure', {
        tokens: compactWithUnit(sumTokens(m.tokens), UNIT.tokens),
        calls: withUnit(m.calls, UNIT.times),
      }),
    }))
}

function toolRows(tools: readonly ToolUsage[]): BarRow[] {
  return [...tools]
    .sort((a, b) => b.calls - a.calls)
    .slice(0, TOOL_ROWS)
    .map((tool) => {
      const note = TOOL_NOTES[tool.category]
      return {
        key: tool.category + ':' + tool.name,
        name: tool.name,
        note: note ? t(note) : undefined,
        value: tool.calls,
        failed: tool.errors,
        figure: t('dashboard.toolFigure', {
          calls: withUnit(tool.calls, UNIT.times),
          rate: fmtPct(ratio(tool.errors, tool.calls)),
        }),
      }
    })
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
        <Card title={t('dashboard.modelsTitle')} hint={t('dashboard.modelsHint')} className="u-span-6">
          {models.length ? (
            <BarList rows={models} label={t('dashboard.modelsLabel')} />
          ) : (
            <Empty coverage={o.coverage} />
          )}
        </Card>
        <Card title={t('dashboard.cacheTitle')} className="u-span-6">
          <CacheMeter hitRatio={o.cacheHitRatio} tokens={o.tokens} coverage={o.coverage} />
        </Card>
        <Card
          title={t('dashboard.toolsTitle')}
          hint={t('dashboard.toolsHint')}
          className="u-span-6"
          action={
            <button type="button" className="u-dash-link" onClick={() => open('tools')}>
              {t('dashboard.viewAll')}
            </button>
          }
        >
          {tools.length ? (
            <>
              <Legend
                label={t('dashboard.barColours')}
                items={[
                  { key: 'ok', paint: 's1', label: t('dashboard.succeeded') },
                  { key: 'failed', paint: 'bad', label: t('dashboard.failed') },
                ]}
              />
              <BarList rows={tools} label={t('dashboard.toolsLabel')} />
            </>
          ) : (
            <Empty coverage={o.coverage} />
          )}
        </Card>
        <Card
          title={t('dashboard.projectsTitle')}
          hint={t('dashboard.projectsHint')}
          className="u-span-6"
        >
          <ProjectTable
            projects={o.projects}
            coverage={o.coverage}
            onOpen={(project) => open('sessions', project)}
          />
        </Card>
        <Card title={t('dashboard.heatTitle')} hint={t('dashboard.heatHint')} className="u-span-8">
          <HeatMap cells={o.heatmap} coverage={o.coverage} />
        </Card>
        {p.teaser}
      </div>
    </>
  )
}

/** The dashboard view: headline numbers, trend, breakdowns and an insight preview. */
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
          {t('dashboard.loading')}
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
