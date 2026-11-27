import { useRef } from 'react'
import { t, type MessageKey } from '../../i18n'
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

const sourceNotes: Record<Exclude<SourceState, 'available'>, { text: MessageKey; className: string }> = {
  missing: { text: 'session.sourceMissing', className: 'u-note u-note-warn' },
  unreadable: { text: 'session.sourceUnreadable', className: 'u-note u-note-bad' },
}

/** Placeholder of session.notFoundLead that the session ID is drawn into. */
const ID_SLOT = '{id}'

/** The session detail view: one session's summary, timeline, tools, token flow, sub-agents and extension events. */
export function SessionDetail(props: ViewProps) {
  const { route, navigate } = props
  const back = () => navigate({ view: 'sessions', days: route.days, project: route.project })
  if (!route.sessionId)
    return (
      <Card title={t('session.noSessionTitle')}>
        <p className="u-sd-lead">{t('session.noSessionLead')}</p>
        <BackButton onBack={back} />
      </Card>
    )
  // The key drops the loaded timeline and tab memory when another session opens.
  return <SessionBody key={route.sessionId} {...props} sessionId={route.sessionId} onBack={back} />
}

function tabLabel(tab: SessionTab): string | undefined {
  const entry = sessionTabs.find((candidate) => candidate.id === tab)
  return entry && t(entry.labelKey)
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
    if (isNotFound(loaded.error)) {
      // The ID is drawn as code, wherever the language puts it in the sentence.
      const [before = '', after = ''] = t('session.notFoundLead').split(ID_SLOT)
      return (
        <Card title={t('session.notFoundTitle')}>
          <p className="u-sd-lead">
            {before}
            <code>{sessionId}</code>
            {after}
          </p>
          <BackButton onBack={onBack} />
        </Card>
      )
    }
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
        {t('session.loading')}
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
    'aria-label': tabLabel(tab),
    hidden: view.tab !== tab,
    className: 'u-sd-panel',
  })

  return (
    <>
      <ErrorNote error={describeError(loaded.error)} retry={loaded.reload} />
      {status?.scanning && (
        <div className="u-note" role="status">
          {t('session.scanning')}
        </div>
      )}
      <Header detail={detail} onBack={onBack} />
      {sourceNote && (
        <div className={sourceNote.className} role="status">
          {t(sourceNote.text)}
        </div>
      )}
      <Tabs
        tabs={sessionTabs.map((tab) => ({ id: tab.id, label: t(tab.labelKey), count: counts[tab.id] }))}
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
