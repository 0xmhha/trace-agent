import { useState } from 'react'
import type { Coverage, ToolCategory, ToolUsage } from '../../types'
import { Pill, fmtInt, fmtMs } from '../../ui'
import { CountBar, NameCell, RateCell, failureRate, maxOf } from './cells'
import { categories, categoryMeta } from './model'
import { SortTable, type SortColumn } from './SortTable'

const ALL_CATEGORIES = ''

function origin(t: ToolUsage): string | undefined {
  const parts = [t.mcpServer && `서버 ${t.mcpServer}`, t.plugin && `플러그인 ${t.plugin}`]
  return parts.filter(Boolean).join(' · ') || undefined
}

function matches(t: ToolUsage, needle: string): boolean {
  return [t.name, t.mcpServer ?? '', t.plugin ?? ''].some((s) => s.toLowerCase().includes(needle))
}

function CategoryPill({ id }: { id: ToolCategory }) {
  const meta = categoryMeta(id)
  return (
    <Pill>
      <i className="u-tools-swatch" style={{ background: meta.color }} aria-hidden="true" />
      {meta.label}
    </Pill>
  )
}

function columns(max: number): SortColumn<ToolUsage>[] {
  return [
    {
      key: 'name',
      label: '이름',
      render: (t) => <NameCell name={t.name} sub={origin(t)} />,
      sortValue: (t) => t.name,
    },
    {
      key: 'category',
      label: '분류',
      render: (t) => <CategoryPill id={t.category} />,
      sortValue: (t) => categories.findIndex((c) => c.id === t.category),
    },
    {
      key: 'calls',
      label: '호출(회)',
      align: 'right',
      render: (t) => <CountBar value={t.calls} max={max} />,
      sortValue: (t) => t.calls,
    },
    {
      key: 'errors',
      label: '실패(회)',
      align: 'right',
      render: (t) => fmtInt(t.errors),
      sortValue: (t) => t.errors,
    },
    {
      key: 'rate',
      label: '실패율',
      align: 'right',
      render: (t) => <RateCell failures={t.errors} total={t.calls} />,
      sortValue: (t) => failureRate(t.errors, t.calls),
    },
    {
      key: 'pending',
      label: '미완료(회)',
      align: 'right',
      render: (t) => fmtInt(t.pending),
      sortValue: (t) => t.pending,
    },
    {
      key: 'sessions',
      label: '세션(개)',
      align: 'right',
      render: (t) => fmtInt(t.sessions),
      sortValue: (t) => t.sessions,
    },
    {
      key: 'p50',
      label: 'p50',
      align: 'right',
      render: (t) => fmtMs(t.p50Ms),
      sortValue: (t) => t.p50Ms,
    },
    {
      key: 'p95',
      label: 'p95',
      align: 'right',
      render: (t) => fmtMs(t.p95Ms),
      sortValue: (t) => t.p95Ms,
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
    (t) => (!p.category || t.category === p.category) && (!needle || matches(t, needle)),
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
          분류
          <select
            value={p.category ?? ALL_CATEGORIES}
            onChange={(e) =>
              p.onCategory(categories.find((c) => c.id === e.target.value)?.id ?? null)
            }
          >
            <option value={ALL_CATEGORIES}>전체</option>
            {categories.map((c) => (
              <option key={c.id} value={c.id}>
                {c.label}
              </option>
            ))}
          </select>
        </label>
        <label>
          이름
          <input
            type="search"
            value={text}
            placeholder="이름, 서버, 플러그인"
            onChange={(e) => setText(e.target.value)}
          />
        </label>
        {filtered && (
          <button type="button" onClick={clear}>
            조건 지우기
          </button>
        )}
        <span className="u-hint u-tools-shown" role="status">
          {filtered
            ? `도구 ${fmtInt(p.rows.length)}개 중 ${fmtInt(shown.length)}개`
            : `도구 ${fmtInt(p.rows.length)}개`}
        </span>
      </div>
      <SortTable
        caption="도구별 호출 수와 실패율, 소요 시간"
        columns={columns(maxOf(shown, (t) => t.calls))}
        rows={shown}
        rowKey={(t) => t.name}
        initial={{ key: 'calls', dir: 'desc' }}
        coverage={p.coverage}
        empty={p.rows.length > 0 ? '조건에 맞는 도구가 없습니다.' : undefined}
      />
    </>
  )
}
