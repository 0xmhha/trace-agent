import type { GroupUsage, HookUsage, SessionDetail, ToolUsage } from '../../types'
import {
  Card,
  DataTable,
  Empty,
  Pill,
  fmtCompact,
  fmtInt,
  fmtMs,
  fmtPct,
  ratio,
  sumTokens,
  type Column,
} from '../../ui'
import { categoryLabels, labelOf, tokenParts } from './format'

function failures(errors: number) {
  return errors > 0 ? <b className="u-tone-bad">{fmtInt(errors)}</b> : fmtInt(errors)
}

const toolColumns: Column<ToolUsage>[] = [
  { key: 'name', label: '도구', render: (t) => <code className="u-sd-name">{t.name}</code> },
  {
    key: 'category',
    label: '분류',
    render: (t) => <Pill tone="muted">{labelOf(categoryLabels, t.category)}</Pill>,
  },
  { key: 'calls', label: '호출', align: 'right', render: (t) => fmtInt(t.calls) },
  { key: 'errors', label: '실패', align: 'right', render: (t) => failures(t.errors) },
  {
    key: 'rate',
    label: '실패율',
    align: 'right',
    render: (t) => fmtPct(ratio(t.errors, t.calls)),
  },
  { key: 'pending', label: '결과 없음', align: 'right', render: (t) => fmtInt(t.pending) },
  { key: 'p50', label: '중앙값', align: 'right', render: (t) => fmtMs(t.p50Ms) },
  { key: 'p95', label: 'p95', align: 'right', render: (t) => fmtMs(t.p95Ms) },
]

/** Columns of a name-keyed group. The session count is left out: it is always one here. */
function groupColumns(nameLabel: string, withTokens: boolean): Column<GroupUsage>[] {
  const columns: Column<GroupUsage>[] = [
    { key: 'name', label: nameLabel, render: (g) => <code className="u-sd-name">{g.name}</code> },
    { key: 'calls', label: '호출', align: 'right', render: (g) => fmtInt(g.calls) },
    { key: 'errors', label: '실패', align: 'right', render: (g) => failures(g.errors) },
  ]
  if (withTokens)
    columns.push({
      key: 'tokens',
      label: '토큰',
      align: 'right',
      render: (g) =>
        g.tokens ? <span title={tokenParts(g.tokens)}>{fmtCompact(sumTokens(g.tokens))}</span> : '—',
    })
  columns.push({ key: 'avg', label: '평균 시간', align: 'right', render: (g) => fmtMs(g.avgMs) })
  return columns
}

const mcpColumns = groupColumns('서버', false)
const skillColumns = groupColumns('스킬', true)
const pluginColumns = groupColumns('플러그인', true)

const hookColumns: Column<HookUsage>[] = [
  { key: 'event', label: '이벤트', render: (h) => h.event },
  { key: 'name', label: '훅', render: (h) => <code className="u-sd-name">{h.name}</code> },
  { key: 'runs', label: '실행', align: 'right', render: (h) => fmtInt(h.runs) },
  { key: 'failures', label: '실패', align: 'right', render: (h) => failures(h.failures) },
  { key: 'avg', label: '평균 시간', align: 'right', render: (h) => fmtMs(h.avgMs) },
  { key: 'max', label: '최대 시간', align: 'right', render: (h) => fmtMs(h.maxMs) },
]

function Section<T>(p: {
  title: string
  hint?: string
  className: string
  empty: string
  columns: Column<T>[]
  rows: T[]
  rowKey: (row: T) => string
}) {
  return (
    <Card title={p.title} hint={p.hint} className={p.className}>
      {p.rows.length ? (
        <DataTable columns={p.columns} rows={p.rows} rowKey={p.rowKey} />
      ) : (
        <Empty>{p.empty}</Empty>
      )}
    </Card>
  )
}

/** The 도구 tab: what the session called, grouped the way the tools page groups it. */
export function ToolsTab({ detail }: { detail: SessionDetail }) {
  return (
    <div className="u-grid">
      <Section
        title="도구"
        hint="걸린 시간은 결과가 돌아온 호출만으로 구합니다."
        className="u-span-12"
        empty="이 세션은 도구를 호출하지 않았습니다."
        columns={toolColumns}
        rows={detail.tools}
        rowKey={(t) => t.category + ':' + t.name}
      />
      <Section
        title="MCP 서버"
        className="u-span-6"
        empty="이 세션은 MCP 서버를 쓰지 않았습니다."
        columns={mcpColumns}
        rows={detail.mcpServers}
        rowKey={(g) => g.name}
      />
      <Section
        title="스킬"
        className="u-span-6"
        empty="이 세션은 스킬을 쓰지 않았습니다."
        columns={skillColumns}
        rows={detail.skills}
        rowKey={(g) => g.name}
      />
      <Section
        title="플러그인"
        className="u-span-6"
        empty="이 세션은 플러그인을 쓰지 않았습니다."
        columns={pluginColumns}
        rows={detail.plugins}
        rowKey={(g) => g.name}
      />
      <Section
        title="훅"
        className="u-span-6"
        empty="이 세션에서 실행된 훅이 없습니다."
        columns={hookColumns}
        rows={detail.hooks}
        rowKey={(h) => h.event + ':' + h.name}
      />
    </div>
  )
}
