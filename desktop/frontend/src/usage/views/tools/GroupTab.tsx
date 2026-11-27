import type { Coverage, GroupUsage } from '../../types'
import { fmtInt, fmtMs, sumTokens } from '../../ui'
import { CountBar, NameCell, RateCell, TokenCell, failureRate, maxOf } from './cells'
import { SortTable, type SortColumn } from './SortTable'
import { t } from '../../../i18n'

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
      label: t('tools.col.calls'),
      align: 'right',
      render: (g) => <CountBar value={g.calls} max={max} />,
      sortValue: (g) => g.calls,
    },
    {
      key: 'errors',
      label: t('tools.col.errors'),
      align: 'right',
      render: (g) => fmtInt(g.errors),
      sortValue: (g) => g.errors,
    },
    {
      key: 'rate',
      label: t('tools.col.rate'),
      align: 'right',
      render: (g) => <RateCell failures={g.errors} total={g.calls} />,
      sortValue: (g) => failureRate(g.errors, g.calls),
    },
    {
      key: 'sessions',
      label: t('tools.col.sessions'),
      align: 'right',
      render: (g) => fmtInt(g.sessions),
      sortValue: (g) => g.sessions,
    },
    {
      key: 'avg',
      label: t('tools.col.avg'),
      align: 'right',
      render: (g) => fmtMs(g.avgMs),
      sortValue: (g) => g.avgMs,
    },
  ]
  if (tokens)
    list.push({
      key: 'tokens',
      label: t('tools.col.tokens'),
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
