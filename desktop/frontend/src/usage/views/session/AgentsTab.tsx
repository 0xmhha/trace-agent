import { t } from '../../../i18n'
import type { AgentRow } from '../../types'
import { Card, DataTable, Empty, fmtCompact, fmtDateTime, fmtInt, fmtMs, sumTokens, type Column } from '../../ui'
import { elapsedMs, rootAgentLabel, tokenParts } from './format'

function typeLabel(agent: AgentRow): string {
  return agent.agentId ? agent.agentType || t('session.unknownType') : rootAgentLabel()
}

/** The sub-agents tab. Choosing a row opens that agent's transcript in the conversation tab. */
export function AgentsTab(p: { agents: AgentRow[]; onOpen: (agentId: string) => void }) {
  const columns: Column<AgentRow>[] = [
    {
      key: 'agent',
      label: t('session.colAgent'),
      render: (a) => (
        <span className="u-sd-stack">
          <span>{typeLabel(a)}</span>
          {a.agentId && <code className="u-sd-dim">{a.agentId}</code>}
        </span>
      ),
    },
    { key: 'calls', label: t('session.colModelCalls'), align: 'right', render: (a) => fmtInt(a.modelCalls) },
    { key: 'tools', label: t('session.colToolCalls'), align: 'right', render: (a) => fmtInt(a.toolCalls) },
    {
      key: 'errors',
      label: t('session.colToolFailures'),
      align: 'right',
      render: (a) =>
        a.toolErrors > 0 ? <b className="u-tone-bad">{fmtInt(a.toolErrors)}</b> : fmtInt(a.toolErrors),
    },
    {
      key: 'tokens',
      label: t('session.colTokens'),
      align: 'right',
      render: (a) => <span title={tokenParts(a.tokens)}>{fmtCompact(sumTokens(a.tokens))}</span>,
    },
    {
      key: 'span',
      label: t('session.colSpan'),
      render: (a) => (
        <span className="u-sd-stack u-sd-num">
          <span>{fmtDateTime(a.startedAt)} ~</span>
          <span>{fmtDateTime(a.lastAt)}</span>
          <span className="u-sd-dim">{fmtMs(elapsedMs(a.startedAt, a.lastAt))}</span>
        </span>
      ),
    },
    {
      key: 'open',
      label: t('session.colOpen'),
      render: (a) => (
        <button
          className="u-sd-small"
          aria-label={t('session.openConversationOf', {
            agent: a.agentId ? typeLabel(a) + ' ' + a.agentId : typeLabel(a),
          })}
          onClick={(e) => {
            // The row has the same action; one navigation is enough.
            e.stopPropagation()
            p.onOpen(a.agentId)
          }}
        >
          {t('session.openConversation')}
        </button>
      ),
    },
  ]
  const hasSubagents = p.agents.some((a) => a.agentId)
  return (
    <Card title={t('session.agentsTitle')} hint={t('session.agentsHint')}>
      {p.agents.length > 0 && (
        <DataTable
          columns={columns}
          rows={p.agents}
          rowKey={(a) => a.agentId || 'root'}
          onRow={(a) => p.onOpen(a.agentId)}
        />
      )}
      {!hasSubagents && <Empty>{t('session.noSubagents')}</Empty>}
    </Card>
  )
}
