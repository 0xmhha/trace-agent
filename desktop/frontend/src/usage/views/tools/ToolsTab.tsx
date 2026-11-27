import { useState } from 'react'
import type { Coverage, ToolCategory, ToolUsage } from '../../types'
import { Pill, fmtInt, fmtMs } from '../../ui'
import { CountBar, NameCell, RateCell, failureRate, maxOf } from './cells'
import { categories, categoryMeta } from './model'
import { SortTable, type SortColumn } from './SortTable'
import { t } from '../../../i18n'

const ALL_CATEGORIES = ''

function origin(tool: ToolUsage): string | undefined {
  const parts = [
    tool.mcpServer && t('tools.origin.server', { name: tool.mcpServer }),
    tool.plugin && t('tools.origin.plugin', { name: tool.plugin }),
  ]
  return parts.filter(Boolean).join(' · ') || undefined
}

function matches(tool: ToolUsage, needle: string): boolean {
  return [tool.name, tool.mcpServer ?? '', tool.plugin ?? ''].some((s) => s.toLowerCase().includes(needle))
}

function CategoryPill({ id }: { id: ToolCategory }) {
  const meta = categoryMeta(id)
  return (
    <Pill>
      <i className="u-tools-swatch" style={{ background: meta.color }} aria-hidden="true" />
      {t(meta.labelKey)}
    </Pill>
  )
}

function columns(max: number): SortColumn<ToolUsage>[] {
  return [
    {
      key: 'name',
      label: t('tools.col.name'),
      render: (tool) => <NameCell name={tool.name} sub={origin(tool)} />,
      sortValue: (tool) => tool.name,
    },
    {
      key: 'category',
      label: t('tools.col.category'),
      render: (tool) => <CategoryPill id={tool.category} />,
      sortValue: (tool) => categories.findIndex((c) => c.id === tool.category),
    },
    {
      key: 'calls',
      label: t('tools.col.calls'),
      align: 'right',
      render: (tool) => <CountBar value={tool.calls} max={max} />,
      sortValue: (tool) => tool.calls,
    },
    {
      key: 'errors',
      label: t('tools.col.errors'),
      align: 'right',
      render: (tool) => fmtInt(tool.errors),
      sortValue: (tool) => tool.errors,
    },
    {
      key: 'rate',
      label: t('tools.col.rate'),
      align: 'right',
      render: (tool) => <RateCell failures={tool.errors} total={tool.calls} />,
      sortValue: (tool) => failureRate(tool.errors, tool.calls),
    },
    {
      key: 'pending',
      label: t('tools.col.pending'),
      align: 'right',
      render: (tool) => fmtInt(tool.pending),
      sortValue: (tool) => tool.pending,
    },
    {
      key: 'sessions',
      label: t('tools.col.sessions'),
      align: 'right',
      render: (tool) => fmtInt(tool.sessions),
      sortValue: (tool) => tool.sessions,
    },
    {
      key: 'p50',
      label: 'p50',
      align: 'right',
      render: (tool) => fmtMs(tool.p50Ms),
      sortValue: (tool) => tool.p50Ms,
    },
    {
      key: 'p95',
      label: 'p95',
      align: 'right',
      render: (tool) => fmtMs(tool.p95Ms),
      sortValue: (tool) => tool.p95Ms,
    },
  ]
}

/**
 * Tool table with a category filter and a name filter. The category lives in
 * the route; the typed text is transient, since a route change per keystroke
 * would fill the history.
 */
export function ToolsTab(p: {
  rows: readonly ToolUsage[]
  coverage: Coverage
  category: ToolCategory | null
  onCategory: (category: ToolCategory | null) => void
}) {
  const [text, setText] = useState('')
  const needle = text.trim().toLowerCase()
  const shown = p.rows.filter(
    (tool) => (!p.category || tool.category === p.category) && (!needle || matches(tool, needle)),
  )
  const filtered = p.category !== null || needle !== ''
  const clear = () => {
    setText('')
    if (p.category) p.onCategory(null)
  }
  return (
    <>
      <div className="u-tools-filters">
        <label>
          {t('tools.filter.category')}
          <select
            value={p.category ?? ALL_CATEGORIES}
            onChange={(e) =>
              p.onCategory(categories.find((c) => c.id === e.target.value)?.id ?? null)
            }
          >
            <option value={ALL_CATEGORIES}>{t('tools.filter.all')}</option>
            {categories.map((c) => (
              <option key={c.id} value={c.id}>
                {t(c.labelKey)}
              </option>
            ))}
          </select>
        </label>
        <label>
          {t('tools.filter.name')}
          <input
            type="search"
            value={text}
            placeholder={t('tools.filter.placeholder')}
            onChange={(e) => setText(e.target.value)}
          />
        </label>
        {filtered && (
          <button type="button" onClick={clear}>
            {t('tools.filter.clear')}
          </button>
        )}
        <span className="u-hint u-tools-shown" role="status">
          {filtered
            ? t('tools.filter.shown', {
                total: fmtInt(p.rows.length),
                shown: fmtInt(shown.length),
              })
            : t('tools.filter.total', { count: p.rows.length, total: fmtInt(p.rows.length) })}
        </span>
      </div>
      <SortTable
        caption={t('tools.caption.tools')}
        columns={columns(maxOf(shown, (tool) => tool.calls))}
        rows={shown}
        rowKey={(tool) => tool.name}
        initial={{ key: 'calls', dir: 'desc' }}
        coverage={p.coverage}
        empty={p.rows.length > 0 ? t('tools.noMatch') : undefined}
      />
    </>
  )
}
