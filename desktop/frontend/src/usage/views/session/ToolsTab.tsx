import { t, type MessageKey } from '../../../i18n'
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

// Column lists are built at render so their headers follow the locale.
function toolColumns(): Column<ToolUsage>[] {
  return [
    { key: 'name', label: t('session.colTool'), render: (u) => <code className="u-sd-name">{u.name}</code> },
    {
      key: 'category',
      label: t('session.colCategory'),
      render: (u) => <Pill tone="muted">{labelOf(categoryLabels, u.category)}</Pill>,
    },
    { key: 'calls', label: t('session.colCalls'), align: 'right', render: (u) => fmtInt(u.calls) },
    { key: 'errors', label: t('session.colFailures'), align: 'right', render: (u) => failures(u.errors) },
    {
      key: 'rate',
      label: t('session.colFailureRate'),
      align: 'right',
      render: (u) => fmtPct(ratio(u.errors, u.calls)),
    },
    { key: 'pending', label: t('session.colPending'), align: 'right', render: (u) => fmtInt(u.pending) },
    { key: 'p50', label: t('session.colMedian'), align: 'right', render: (u) => fmtMs(u.p50Ms) },
    { key: 'p95', label: t('session.colP95'), align: 'right', render: (u) => fmtMs(u.p95Ms) },
  ]
}

/** Columns of a name-keyed group. The session count is left out: it is always one here. */
function groupColumns(nameLabel: MessageKey, withTokens: boolean): Column<GroupUsage>[] {
  const columns: Column<GroupUsage>[] = [
    { key: 'name', label: t(nameLabel), render: (g) => <code className="u-sd-name">{g.name}</code> },
    { key: 'calls', label: t('session.colCalls'), align: 'right', render: (g) => fmtInt(g.calls) },
    { key: 'errors', label: t('session.colFailures'), align: 'right', render: (g) => failures(g.errors) },
  ]
  if (withTokens)
    columns.push({
      key: 'tokens',
      label: t('session.colTokens'),
      align: 'right',
      render: (g) =>
        g.tokens ? <span title={tokenParts(g.tokens)}>{fmtCompact(sumTokens(g.tokens))}</span> : '—',
    })
  columns.push({ key: 'avg', label: t('session.colAvg'), align: 'right', render: (g) => fmtMs(g.avgMs) })
  return columns
}

function hookColumns(): Column<HookUsage>[] {
  return [
    { key: 'event', label: t('session.colEvent'), render: (h) => h.event },
    { key: 'name', label: t('session.colHook'), render: (h) => <code className="u-sd-name">{h.name}</code> },
    { key: 'runs', label: t('session.colRuns'), align: 'right', render: (h) => fmtInt(h.runs) },
    { key: 'failures', label: t('session.colFailures'), align: 'right', render: (h) => failures(h.failures) },
    { key: 'avg', label: t('session.colAvg'), align: 'right', render: (h) => fmtMs(h.avgMs) },
    { key: 'max', label: t('session.colMax'), align: 'right', render: (h) => fmtMs(h.maxMs) },
  ]
}

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

/** The tools tab: what the session called, grouped the way the tools page groups it. */
export function ToolsTab({ detail }: { detail: SessionDetail }) {
  return (
    <div className="u-grid">
      <Section
        title={t('session.toolsTitle')}
        hint={t('session.toolsHint')}
        className="u-span-12"
        empty={t('session.noTools')}
        columns={toolColumns()}
        rows={detail.tools}
        rowKey={(u) => u.category + ':' + u.name}
      />
      <Section
        title={t('session.mcpTitle')}
        className="u-span-6"
        empty={t('session.noMcp')}
        columns={groupColumns('session.colServer', false)}
        rows={detail.mcpServers}
        rowKey={(g) => g.name}
      />
      <Section
        title={t('session.skillsTitle')}
        className="u-span-6"
        empty={t('session.noSkills')}
        columns={groupColumns('session.colSkill', true)}
        rows={detail.skills}
        rowKey={(g) => g.name}
      />
      <Section
        title={t('session.pluginsTitle')}
        className="u-span-6"
        empty={t('session.noPlugins')}
        columns={groupColumns('session.colPlugin', true)}
        rows={detail.plugins}
        rowKey={(g) => g.name}
      />
      <Section
        title={t('session.hooksTitle')}
        className="u-span-6"
        empty={t('session.noHooks')}
        columns={hookColumns()}
        rows={detail.hooks}
        rowKey={(h) => h.event + ':' + h.name}
      />
    </div>
  )
}
