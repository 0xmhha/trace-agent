import type { AgentRow } from '../../types'
import { Card, DataTable, Empty, fmtCompact, fmtDateTime, fmtInt, fmtMs, sumTokens, type Column } from '../../ui'
import { ROOT_AGENT_LABEL, elapsedMs, tokenParts } from './format'

function typeLabel(agent: AgentRow): string {
  return agent.agentId ? agent.agentType || '종류 미상' : ROOT_AGENT_LABEL
}

/** The 하위 에이전트 tab. Choosing a row opens that agent's transcript in the 대화 tab. */
export function AgentsTab(p: { agents: AgentRow[]; onOpen: (agentId: string) => void }) {
  const columns: Column<AgentRow>[] = [
    {
      key: 'agent',
      label: '에이전트',
      render: (a) => (
        <span className="u-sd-stack">
          <span>{typeLabel(a)}</span>
          {a.agentId && <code className="u-sd-dim">{a.agentId}</code>}
        </span>
      ),
    },
    { key: 'calls', label: '모델 호출', align: 'right', render: (a) => fmtInt(a.modelCalls) },
    { key: 'tools', label: '도구 호출', align: 'right', render: (a) => fmtInt(a.toolCalls) },
    {
      key: 'errors',
      label: '도구 실패',
      align: 'right',
      render: (a) =>
        a.toolErrors > 0 ? <b className="u-tone-bad">{fmtInt(a.toolErrors)}</b> : fmtInt(a.toolErrors),
    },
    {
      key: 'tokens',
      label: '토큰',
      align: 'right',
      render: (a) => <span title={tokenParts(a.tokens)}>{fmtCompact(sumTokens(a.tokens))}</span>,
    },
    {
      key: 'span',
      label: '시간 범위',
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
      label: '대화',
      render: (a) => (
        <button
          className="u-sd-small"
          aria-label={typeLabel(a) + (a.agentId ? ' ' + a.agentId : '') + ' 대화 보기'}
          onClick={(e) => {
            // The row has the same action; one navigation is enough.
            e.stopPropagation()
            p.onOpen(a.agentId)
          }}
        >
          대화 보기
        </button>
      ),
    },
  ]
  const hasSubagents = p.agents.some((a) => a.agentId)
  return (
    <Card title="에이전트별 사용" hint="행을 고르면 그 에이전트의 대화를 엽니다.">
      {p.agents.length > 0 && (
        <DataTable
          columns={columns}
          rows={p.agents}
          rowKey={(a) => a.agentId || 'root'}
          onRow={(a) => p.onOpen(a.agentId)}
        />
      )}
      {!hasSubagents && <Empty>이 세션은 하위 에이전트를 쓰지 않았습니다.</Empty>}
    </Card>
  )
}
