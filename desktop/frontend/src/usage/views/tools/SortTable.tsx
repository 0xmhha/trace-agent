import { useState, type ReactNode } from 'react'
import type { Coverage } from '../../types'
import { Empty } from '../../ui'

export type SortDir = 'asc' | 'desc'
export type SortValue = number | string | null

export interface SortColumn<T> {
  key: string
  label: string
  align?: 'left' | 'right'
  render: (row: T) => ReactNode
  /** Value the column sorts by. A column without it has no sort button. */
  sortValue?: (row: T) => SortValue
}

export interface SortState {
  key: string
  dir: SortDir
}

const sortLabels: Record<SortDir, string> = { asc: '오름차순', desc: '내림차순' }
const ariaSort: Record<SortDir, 'ascending' | 'descending'> = {
  asc: 'ascending',
  desc: 'descending',
}
const sortMarks: Record<SortDir, string> = { asc: '▲', desc: '▼' }

function compare(a: SortValue, b: SortValue): number {
  if (typeof a === 'number' && typeof b === 'number') return a - b
  return String(a).localeCompare(String(b), 'ko-KR', { numeric: true, sensitivity: 'base' })
}

/**
 * Returns rows ordered by the column. Rows without a value go last in both
 * directions, and ties keep the order of their keys so the result does not
 * depend on the order the rows arrived in.
 */
export function sortRows<T>(
  rows: readonly T[],
  value: (row: T) => SortValue,
  dir: SortDir,
  rowKey: (row: T) => string,
): T[] {
  const sign = dir === 'asc' ? 1 : -1
  return [...rows].sort((x, y) => {
    const a = value(x),
      b = value(y)
    if (a == null || b == null) {
      if (a != null) return -1
      if (b != null) return 1
    } else {
      const order = compare(a, b)
      if (order !== 0) return sign * order
    }
    return rowKey(x).localeCompare(rowKey(y))
  })
}

/** Numbers start with the largest first, names in alphabetical order. */
function firstDir<T>(column: SortColumn<T>): SortDir {
  return column.align === 'right' ? 'desc' : 'asc'
}

/**
 * Table whose column headers are sort buttons. The sort is transient view
 * state: it resets when the table is mounted again.
 */
export function SortTable<T>(p: {
  caption: string
  columns: SortColumn<T>[]
  rows: readonly T[]
  rowKey: (row: T) => string
  initial: SortState
  coverage?: Coverage
  /** Shown instead of the coverage text when the list is empty, e.g. for a filter without a match. */
  empty?: ReactNode
}) {
  const [sort, setSort] = useState<SortState>(p.initial)
  const { columns, rows, rowKey } = p
  if (!rows.length) return <Empty coverage={p.coverage}>{p.empty}</Empty>

  // The lists are short (one row per tool or group), so sorting on every
  // render is cheaper than keeping a memo in step with the column functions.
  const value = columns.find((c) => c.key === sort.key)?.sortValue
  const sorted = value ? sortRows(rows, value, sort.dir, rowKey) : rows

  const toggle = (column: SortColumn<T>) =>
    setSort((current) =>
      current.key === column.key
        ? { key: column.key, dir: current.dir === 'asc' ? 'desc' : 'asc' }
        : { key: column.key, dir: firstDir(column) },
    )

  return (
    <div className="u-table-wrap">
      <table className="u-table u-tools-table">
        <caption className="u-tools-vh">{p.caption}</caption>
        <thead>
          <tr>
            {columns.map((c) => {
              const active = sort.key === c.key
              const align = c.align === 'right' ? 'num' : ''
              if (!c.sortValue)
                return (
                  <th key={c.key} scope="col" className={align}>
                    {c.label}
                  </th>
                )
              return (
                <th
                  key={c.key}
                  scope="col"
                  className={'u-tools-sortable ' + align}
                  aria-sort={active ? ariaSort[sort.dir] : 'none'}
                >
                  <button
                    type="button"
                    className={'u-tools-sort' + (active ? ' u-tools-sort-active' : '')}
                    title={
                      active
                        ? `${c.label}: ${sortLabels[sort.dir]}으로 정렬됨. 누르면 순서를 뒤집습니다.`
                        : `${c.label} 기준으로 정렬`
                    }
                    onClick={() => toggle(c)}
                  >
                    {c.label}
                    <span className="u-tools-sort-mark" aria-hidden="true">
                      {active ? sortMarks[sort.dir] : ''}
                    </span>
                  </button>
                </th>
              )
            })}
          </tr>
        </thead>
        <tbody>
          {sorted.map((row) => (
            <tr key={rowKey(row)}>
              {columns.map((c) => (
                <td key={c.key} className={c.align === 'right' ? 'num' : ''}>
                  {c.render(row)}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
