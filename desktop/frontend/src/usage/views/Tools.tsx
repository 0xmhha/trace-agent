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

const MS_PER_SECOND = 1000

const failureLegend =
  `실패율이 ${thresholdPct(FAILURE_WARN_RATIO)} 이상이면 주의 색으로, ` +
  `${thresholdPct(FAILURE_BAD_RATIO)} 이상이면 경고 색과 '높음' 표시로 나타냅니다.`
const hookLegend =
  ` 평균 시간이 ${HOOK_AVG_BAD_MS / MS_PER_SECOND}초 이상이거나 ` +
  `최대 시간이 ${HOOK_MAX_BAD_MS / MS_PER_SECOND}초 이상이면 '느림'을 붙입니다.`

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
      caption={`${meta.label}별 호출 수와 실패율, 평균 시간`}
      nameLabel={meta.nameLabel}
      tokens={meta.tokens}
      rows={data[meta.id]}
      coverage={data.coverage}
    />
  )
}

/** 도구와 확장: tools, MCP servers, skills, plugins, hooks, sub-agents and Bash programs of the range. */
export function ToolsView({ api, route, navigate, range, refresh }: ViewProps) {
  const load = useLoad(
    () => api.tools({ range, project: route.project }),
    [api, range, route.project, refresh],
  )
  const state = parseTab(route.tab)
  const setState = (next: TabState) => navigate({ ...route, tab: formatTab(next) })
  const data = load.data
  const meta = tabs.find((t) => t.id === state.tab) ?? tabs[0]!

  return (
    <>
      <ErrorNote error={load.error} retry={load.reload} />
      {!data && load.loading && (
        <p className="u-hint" role="status">
          도구 사용 기록을 읽는 중입니다.
        </p>
      )}
      {data && (
        <>
          <CoverageNote coverage={data.coverage} />
          <Summary tools={data.tools} coverage={data.coverage} />
          <Card className="u-tools-card">
            <Tabs<TabId>
              tabs={tabs.map((t) => ({ id: t.id, label: t.label, count: data[t.id].length }))}
              value={state.tab}
              onChange={(tab) => setState({ tab, category: null })}
            />
            <div className="u-tools-panel" role="tabpanel" aria-label={meta.label}>
              <p className="u-hint u-tools-note">{meta.note}</p>
              {/* The key remounts the table per tab so one tab's sort does not carry over. */}
              <TabBody key={meta.id} meta={meta} data={data} state={state} onState={setState} />
              <p className="u-hint u-tools-note">
                {failureLegend}
                {meta.id === 'hooks' && hookLegend}
              </p>
            </div>
          </Card>
        </>
      )}
    </>
  )
}
