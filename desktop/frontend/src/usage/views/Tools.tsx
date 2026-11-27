import type { ViewProps } from '../index'
import type { Tools } from '../types'
import { Card, CoverageNote, ErrorNote, Tabs, useLoad } from '../ui'
import { GroupTab } from './tools/GroupTab'
import { HooksTab } from './tools/HooksTab'
import {
  FAILURE_BAD_RATIO,
  FAILURE_WARN_RATIO,
  HOOK_AVG_BAD_MS,
  HOOK_MAX_BAD_MS,
  formatTab,
  parseTab,
  tabs,
  thresholdPct,
  type TabId,
  type TabMeta,
  type TabState,
} from './tools/model'
import { Summary } from './tools/Summary'
import { ToolsTab } from './tools/ToolsTab'
import './tools.css'
import { formatNumber, t } from '../../i18n'

const MS_PER_SECOND = 1000

function failureLegend(): string {
  return t('tools.legend.failure', {
    warn: thresholdPct(FAILURE_WARN_RATIO),
    bad: thresholdPct(FAILURE_BAD_RATIO),
  })
}
function hookLegend(): string {
  return t('tools.legend.hook', {
    avg: formatNumber(HOOK_AVG_BAD_MS / MS_PER_SECOND),
    max: formatNumber(HOOK_MAX_BAD_MS / MS_PER_SECOND),
  })
}

function TabBody(p: {
  meta: TabMeta
  data: Tools
  state: TabState
  onState: (state: TabState) => void
}) {
  const { meta, data } = p
  if (meta.id === 'tools')
    return (
      <ToolsTab
        rows={data.tools}
        coverage={data.coverage}
        category={p.state.category}
        onCategory={(category) => p.onState({ tab: meta.id, category })}
      />
    )
  if (meta.id === 'hooks') return <HooksTab rows={data.hooks} coverage={data.coverage} />
  return (
    <GroupTab
      caption={t('tools.caption.group', { group: meta.label })}
      nameLabel={meta.nameLabel}
      tokens={meta.tokens}
      rows={data[meta.id]}
      coverage={data.coverage}
    />
  )
}

/** Tools and extensions: tools, MCP servers, skills, plugins, hooks, sub-agents and Bash programs of the range. */
export function ToolsView({ api, route, navigate, range, refresh }: ViewProps) {
  const load = useLoad(
    () => api.tools({ range, project: route.project }),
    [api, range, route.project, refresh],
  )
  const state = parseTab(route.tab)
  const setState = (next: TabState) => navigate({ ...route, tab: formatTab(next) })
  const data = load.data
  const tabList = tabs()
  const meta = tabList.find((tab) => tab.id === state.tab) ?? tabList[0]!

  return (
    <>
      <ErrorNote error={load.error} retry={load.reload} />
      {!data && load.loading && (
        <p className="u-hint" role="status">
          {t('tools.loading')}
        </p>
      )}
      {data && (
        <>
          <CoverageNote coverage={data.coverage} />
          <Summary tools={data.tools} coverage={data.coverage} />
          <Card className="u-tools-card">
            <Tabs<TabId>
              tabs={tabList.map((tab) => ({
                id: tab.id,
                label: tab.label,
                count: data[tab.id].length,
              }))}
              value={state.tab}
              onChange={(tab) => setState({ tab, category: null })}
            />
            <div className="u-tools-panel" role="tabpanel" aria-label={meta.label}>
              <p className="u-hint u-tools-note">{meta.note}</p>
              {/* The key remounts the table per tab so one tab's sort does not carry over. */}
              <TabBody key={meta.id} meta={meta} data={data} state={state} onState={setState} />
              <p className="u-hint u-tools-note">
                {failureLegend()}
                {meta.id === 'hooks' && ' ' + hookLegend()}
              </p>
            </div>
          </Card>
        </>
      )}
    </>
  )
}
