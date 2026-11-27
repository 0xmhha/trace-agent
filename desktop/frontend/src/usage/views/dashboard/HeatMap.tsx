import { useMemo, useState, type FocusEvent, type KeyboardEvent, type PointerEvent } from 'react'
import { t } from '../../../i18n'
import type { Coverage, HeatCell } from '../../types'
import { Empty, fmtInt } from '../../ui'
import { UNIT, hourLabel, weekdayName, weekdayShort, withUnit } from './format'
import { Tip, TipRows } from './parts'
import { useWidth } from './useWidth'

const HOURS = 24
/** Store weekday indexes (0 is Sunday) in display order: Monday first, weekend together. */
const WEEK_ORDER: readonly number[] = [1, 2, 3, 4, 5, 6, 0]
/** Colour steps above zero. One hue, darker for more. */
const LEVELS = 4
const LABEL_WIDTH = 26
const HEADER_HEIGHT = 16
const CELL_GAP = 2
const CELL_RADIUS = 2
const ROW_MIN = 14
const ROW_MAX = 24
const HOUR_LABEL_EVERY = 3

interface Slot {
  row: number
  hour: number
}

function levelOf(value: number, max: number): number {
  if (value <= 0 || max <= 0) return 0
  return Math.min(LEVELS, Math.ceil((value / max) * LEVELS))
}

/** Inclusive prompt-count range of each non-empty colour step, lightest first. */
function levelRanges(max: number): { level: number; text: string }[] {
  const ranges = [{ level: 0, text: withUnit(0, UNIT.times) }]
  for (let level = 1; level <= LEVELS; level++) {
    const low = Math.floor((max * (level - 1)) / LEVELS) + 1
    const high = Math.floor((max * level) / LEVELS)
    if (low > high) continue
    ranges.push({
      level,
      text:
        low === high
          ? withUnit(low, UNIT.times)
          : t('dashboard.heatRange', { low: fmtInt(low), high: withUnit(high, UNIT.times) }),
    })
  }
  return ranges
}

function slotName(slot: Slot): string {
  return t('dashboard.heatSlot', {
    weekday: weekdayName(WEEK_ORDER[slot.row] ?? 0),
    hour: hourLabel(slot.hour),
  })
}

function HeatGrid(p: { grid: readonly (readonly number[])[]; max: number; peak: Slot }) {
  const { grid, max, peak } = p
  const [ref, width] = useWidth<HTMLDivElement>()
  const [active, setActive] = useState<Slot | null>(null)

  const cell = Math.max(0, (width - LABEL_WIDTH) / HOURS)
  const rowHeight = Math.max(ROW_MIN, Math.min(ROW_MAX, cell))
  const height = HEADER_HEIGHT + rowHeight * WEEK_ORDER.length
  const valueAt = (slot: Slot) => grid[slot.row]?.[slot.hour] ?? 0
  const cellX = (hour: number) => LABEL_WIDTH + hour * cell
  const cellY = (row: number) => HEADER_HEIGHT + row * rowHeight

  const onPointerMove = (e: PointerEvent<HTMLDivElement>) => {
    const box = e.currentTarget.getBoundingClientRect()
    const hour = cell > 0 ? Math.floor((e.clientX - box.left - LABEL_WIDTH) / cell) : -1
    const row = Math.floor((e.clientY - box.top - HEADER_HEIGHT) / rowHeight)
    const inside = hour >= 0 && hour < HOURS && row >= 0 && row < WEEK_ORDER.length
    setActive(inside ? { row, hour } : null)
  }
  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key === 'Escape') return setActive(null)
    const from = active ?? peak
    const steps: Record<string, Slot> = {
      ArrowLeft: { row: from.row, hour: Math.max(0, from.hour - 1) },
      ArrowRight: { row: from.row, hour: Math.min(HOURS - 1, from.hour + 1) },
      ArrowUp: { row: Math.max(0, from.row - 1), hour: from.hour },
      ArrowDown: { row: Math.min(WEEK_ORDER.length - 1, from.row + 1), hour: from.hour },
    }
    const next = steps[e.key]
    if (!next) return
    e.preventDefault()
    setActive(next)
  }
  const onFocus = (e: FocusEvent<HTMLDivElement>) => {
    // A mouse click also focuses the grid; only keyboard focus picks a cell.
    if (!active && e.currentTarget.matches(':focus-visible')) setActive(peak)
  }

  const detail = active ? withUnit(valueAt(active), UNIT.times) : ''
  return (
    <div
      ref={ref}
      className="u-dash-plot"
      style={{ minHeight: height }}
      tabIndex={0}
      role="group"
      aria-label={t('dashboard.heatAria', {
        slot: slotName(peak),
        value: withUnit(max, UNIT.times),
      })}
      onPointerMove={onPointerMove}
      onPointerLeave={() => setActive(null)}
      onKeyDown={onKeyDown}
      onFocus={onFocus}
      onBlur={() => setActive(null)}
    >
      {width > 0 && (
        <svg className="u-dash-svg" width={width} height={height} aria-hidden="true">
          {Array.from({ length: HOURS / HOUR_LABEL_EVERY }, (_, i) => i * HOUR_LABEL_EVERY).map(
            (hour) => (
              <text key={hour} className="u-dash-tick" x={cellX(hour)} y={HEADER_HEIGHT - 5}>
                {hourLabel(hour)}
              </text>
            ),
          )}
          {WEEK_ORDER.map((weekday, row) => (
            <g key={weekday}>
              <text
                className="u-dash-tick"
                x={0}
                y={cellY(row) + rowHeight / 2}
                dy="0.32em"
              >
                {weekdayShort(weekday)}
              </text>
              {(grid[row] ?? []).map((value, hour) => (
                <rect
                  key={hour}
                  className={'u-dash-heat-' + levelOf(value, max)}
                  x={cellX(hour)}
                  y={cellY(row)}
                  width={Math.max(0, cell - CELL_GAP)}
                  height={rowHeight - CELL_GAP}
                  rx={CELL_RADIUS}
                />
              ))}
            </g>
          ))}
          {active && (
            <rect
              className="u-dash-heat-active"
              x={cellX(active.hour) - 1}
              y={cellY(active.row) - 1}
              width={Math.max(0, cell - CELL_GAP) + 2}
              height={rowHeight - CELL_GAP + 2}
              rx={CELL_RADIUS + 1}
            />
          )}
        </svg>
      )}
      {active && (
        <Tip
          // Left half: the tip hangs off the right edge of the cell; right half: off its left edge.
          x={cellX(active.hour) + (active.hour >= HOURS / 2 ? 0 : cell - CELL_GAP)}
          y={cellY(active.row)}
          flip={active.hour >= HOURS / 2}
        >
          <TipRows
            title={slotName(active)}
            rows={[{ key: 'prompts', label: t('dashboard.colPrompts'), value: detail }]}
          />
        </Tip>
      )}
      <p className="u-dash-sr" role="status">
        {active ? t('dashboard.heatStatus', { slot: slotName(active), value: detail }) : ''}
      </p>
    </div>
  )
}

function HeatTable({ grid }: { grid: readonly (readonly number[])[] }) {
  return (
    <div className="u-table-wrap">
      <table className="u-table u-dash-heat-table">
        <caption className="u-dash-sr">{t('dashboard.heatCaption')}</caption>
        <thead>
          <tr>
            <th scope="col">{t('dashboard.heatWeekday')}</th>
            {Array.from({ length: HOURS }, (_, hour) => (
              <th key={hour} scope="col" className="num">
                {hourLabel(hour)}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {WEEK_ORDER.map((weekday, row) => (
            <tr key={weekday}>
              <th scope="row">{weekdayShort(weekday)}</th>
              {(grid[row] ?? []).map((value, hour) => (
                <td key={hour} className="num">
                  {value}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

/** Prompts by weekday and hour as a 7 x 24 grid with one-hue colour steps. */
export function HeatMap(p: { cells: readonly HeatCell[]; coverage: Coverage }) {
  const [tableOpen, setTableOpen] = useState(false)
  const { grid, max, peak } = useMemo(() => {
    const grid = WEEK_ORDER.map(() => Array.from({ length: HOURS }, () => 0))
    for (const c of p.cells) {
      const row = WEEK_ORDER.indexOf(c.weekday)
      const line = grid[row]
      // Cells outside the 7 x 24 grid would be a store bug; they are left out.
      if (!line || !Number.isInteger(c.hour) || c.hour < 0 || c.hour >= HOURS) continue
      line[c.hour] = (line[c.hour] ?? 0) + Math.max(0, c.prompts)
    }
    let max = 0
    let peak: Slot = { row: 0, hour: 0 }
    grid.forEach((line, row) =>
      line.forEach((value, hour) => {
        if (value > max) {
          max = value
          peak = { row, hour }
        }
      }),
    )
    return { grid, max, peak }
  }, [p.cells])

  if (max <= 0) return <Empty coverage={p.coverage} />
  return (
    <>
      <HeatGrid grid={grid} max={max} peak={peak} />
      <div className="u-dash-heat-foot">
        <span className="u-hint">
          {t('dashboard.heatPeak', { slot: slotName(peak), value: withUnit(max, UNIT.times) })}
        </span>
        <ul className="u-dash-legend" aria-label={t('dashboard.heatLegend')}>
          {levelRanges(max).map((range) => (
            <li key={range.level}>
              <svg className="u-dash-svg" width={12} height={12} aria-hidden="true">
                <rect
                  className={'u-dash-heat-' + range.level}
                  width={12}
                  height={12}
                  rx={CELL_RADIUS}
                />
              </svg>
              <span>{range.text}</span>
            </li>
          ))}
        </ul>
      </div>
      <details className="u-dash-details" onToggle={(e) => setTableOpen(e.currentTarget.open)}>
        <summary>{t('dashboard.showTable')}</summary>
        {tableOpen && <HeatTable grid={grid} />}
      </details>
    </>
  )
}
