import type { Coverage, GroupUsage } from '../../types'
import { fmtInt, fmtMs, sumTokens } from '../../ui'
import { CountBar, NameCell, RateCell, TokenCell, failureRate, maxOf } from './cells'
import { SortTable, type SortColumn } from './SortTable'

function columns(nameLabel: string, tokens: boolean, max: number): SortColumn<GroupUsage>[] {
  const list: SortColumn<GroupUsage>[] = [
    {
      key: 'name',
      label: nameLabel,
      render: (g) => <NameCell name={g.name} />,
      sortValue: (g) => g.name,
    },
    {
      key: 'calls',
      label: '호출(회)',
      align: 'right',
      render: (g) => <CountBar value={g.calls} max={max} />,
      sortValue: (g) => g.calls,
    },
    {
      key: 'errors',
      label: '실패(회)',
      align: 'right',
      render: (g) => fmtInt(g.errors),
      sortValue: (g) => g.errors,
    },
    {
      key: 'rate',
      label: '실패율',
      align: 'right',
      render: (g) => <RateCell failures={g.errors} total={g.calls} />,
      sortValue: (g) => failureRate(g.errors, g.calls),
    },
    {
      key: 'sessions',
      label: '세션(개)',
      align: 'right',
      render: (g) => fmtInt(g.sessions),
      sortValue: (g) => g.sessions,
    },
    {
      key: 'avg',
      label: '평균 시간',
      align: 'right',
      render: (g) => fmtMs(g.avgMs),
      sortValue: (g) => g.avgMs,
    },
  ]
  if (tokens)
    list.push({
      key: 'tokens',
      label: '귀속 토큰',
      align: 'right',
      render: (g) => <TokenCell tokens={g.tokens} />,
      sortValue: (g) => (g.tokens ? sumTokens(g.tokens) : null),
    })
  return list
}

/** Table of one kind of group: MCP servers, skills, plugins, sub-agent types or Bash programs. */
export function GroupTab(p: {
  caption: string
  nameLabel: string
  /** Adds the attributed-token column. */
  tokens: boolean
  rows: readonly GroupUsage[]
  coverage: Coverage
}) {
  return (
    <SortTable
      caption={p.caption}
      columns={columns(p.nameLabel, p.tokens, maxOf(p.rows, (g) => g.calls))}
      rows={p.rows}
      rowKey={(g) => g.name}
      initial={{ key: 'calls', dir: 'desc' }}
      coverage={p.coverage}
    />
  )
}
