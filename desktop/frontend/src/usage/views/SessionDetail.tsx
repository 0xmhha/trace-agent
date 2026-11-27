import { useRef } from 'react'
import type { ViewProps } from '../index'
import type { SourceState } from '../types'
import { Card, ErrorNote, Tabs, useLoad } from '../ui'
import { AgentsTab } from './session/AgentsTab'
import { Conversation } from './session/Conversation'
import { ExtTab } from './session/ExtTab'
import { FlowTab } from './session/FlowTab'
import { describeError, isNotFound, rethrowAsError } from './session/errors'
import { BackButton, Header } from './session/Header'
import { ToolsTab } from './session/ToolsTab'
import { sessionTabs, type SessionTab } from './session/route'
import { useSessionView } from './session/useSessionView'
import './session.css'

const sourceNotes: Record<Exclude<SourceState, 'available'>, { text: string; className: string }> = {
  missing: {
    text: '원본 세션 기록 파일이 더 이상 없습니다. 집계해 둔 수치만 남아 있고 대화 내용은 볼 수 없습니다.',
    className: 'u-note u-note-warn',
  },
  unreadable: {
    text: '원본 세션 기록 파일을 읽지 못했습니다. 아래 수치는 이전에 읽어 둔 것이고 대화 내용은 볼 수 없습니다.',
    className: 'u-note u-note-bad',
  },
}

/** The 세션 상세 view: one session's summary, timeline, tools, token flow, sub-agents and extension events. */
export function SessionDetail(props: ViewProps) {
  const { route, navigate } = props
  const back = () => navigate({ view: 'sessions', days: route.days, project: route.project })
  if (!route.sessionId)
    return (
      <Card title="세션을 고르지 않았습니다">
        <p className="u-sd-lead">세션 목록에서 살펴볼 세션을 골라 주세요.</p>
        <BackButton onBack={back} />
      </Card>
    )
  // The key drops the loaded timeline and tab memory when another session opens.
  return <SessionBody key={route.sessionId} {...props} sessionId={route.sessionId} onBack={back} />
}

function SessionBody(p: ViewProps & { sessionId: string; onBack: () => void }) {
  const { api, route, navigate, refresh, status, sessionId, onBack } = p
  const loaded = useLoad(() => api.session(sessionId).catch(rethrowAsError), [api, sessionId, refresh])
  const [view, setView] = useSessionView(route.tab, (tab) => navigate({ ...route, tab }))
  // A tab is mounted on its first visit and then kept, so the pages loaded
  // into the timeline survive a look at another tab.
  const visited = useRef(new Set<SessionTab>())
  visited.current.add(view.tab)

  const detail = loaded.data
  if (!detail) {
    if (isNotFound(loaded.error))
      return (
        <Card title="세션을 찾지 못했습니다">
          <p className="u-sd-lead">
            <code>{sessionId}</code> 세션은 저장된 기록에 없습니다. 수집 기간보다 오래됐거나, 제외한
            프로젝트의 세션이거나, 기록을 지운 뒤일 수 있습니다.
          </p>
          <BackButton onBack={onBack} />
        </Card>
      )
    if (loaded.error)
      return (
        <>
          <ErrorNote error={describeError(loaded.error)} retry={loaded.reload} />
          <div>
            <BackButton onBack={onBack} />
          </div>
        </>
      )
    return (
      <p className="u-empty" role="status">
        세션을 읽는 중입니다.
      </p>
    )
  }

  const sourceNote = detail.source === 'available' ? undefined : sourceNotes[detail.source]
  const subagents = detail.agents.filter((agent) => agent.agentId).length
  const extEvents = detail.extSources.reduce((sum, source) => sum + source.calls, 0)
  const counts: Partial<Record<SessionTab, number>> = {
    tools: detail.session.toolCalls,
    agents: subagents,
    ext: extEvents,
  }
  const panel = (tab: SessionTab) => ({
    role: 'tabpanel',
    'aria-label': sessionTabs.find((t) => t.id === tab)?.label,
    hidden: view.tab !== tab,
    className: 'u-sd-panel',
  })

  return (
    <>
      <ErrorNote error={describeError(loaded.error)} retry={loaded.reload} />
      {status?.scanning && (
        <div className="u-note" role="status">
          세션 기록을 읽는 중입니다. 이 세션의 수치가 더 늘어날 수 있습니다.
        </div>
      )}
      <Header detail={detail} onBack={onBack} />
      {sourceNote && (
        <div className={sourceNote.className} role="status">
          {sourceNote.text}
        </div>
      )}
      <Tabs
        tabs={sessionTabs.map((tab) => ({ ...tab, count: counts[tab.id] }))}
        value={view.tab}
        onChange={(tab) => setView({ ...view, tab })}
      />
      {visited.current.has('chat') && (
        <div {...panel('chat')}>
          <Conversation
            api={api}
            sessionId={sessionId}
            agents={detail.agents}
            view={view}
            onView={setView}
            refresh={refresh}
          />
        </div>
      )}
      {view.tab === 'tools' && (
        <div {...panel('tools')}>
          <ToolsTab detail={detail} />
        </div>
      )}
      {view.tab === 'tokens' && (
        <div {...panel('tokens')}>
          <FlowTab
            turns={detail.turns}
            truncated={detail.turnsTruncated}
            compactions={detail.session.compactions}
          />
        </div>
      )}
      {view.tab === 'agents' && (
        <div {...panel('agents')}>
          <AgentsTab agents={detail.agents} onOpen={(agentId) => setView({ ...view, tab: 'chat', agentId })} />
        </div>
      )}
      {visited.current.has('ext') && (
        <div {...panel('ext')}>
          <ExtTab api={api} sessionId={sessionId} sources={detail.extSources} refresh={refresh} />
        </div>
      )}
    </>
  )
}
