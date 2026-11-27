// Small pieces every dashboard chart uses: colour keys, legend, tooltip.
import type { ReactNode } from 'react'

/**
 * Colour slot of a mark. Series take s1..s4 in order; "bad" and "warn" are
 * status colours and only mark failures or a low reading, never a plain series.
 */
export type Paint = 's1' | 's2' | 's3' | 's4' | 'bad' | 'warn'

/** Class that sets the mark colour for both SVG fills and HTML swatches. */
export function paintClass(paint: Paint): string {
  return 'u-dash-c-' + paint
}

export interface LegendItem {
  key: string
  paint: Paint
  label: string
  /** Figure printed after the label so the legend also reads without the chart. */
  value?: string
}

export function Legend({ items, label }: { items: readonly LegendItem[]; label: string }) {
  return (
    <ul className="u-dash-legend" aria-label={label}>
      {items.map((item) => (
        <li key={item.key}>
          <span className={'u-dash-swatch ' + paintClass(item.paint)} aria-hidden="true" />
          <span>{item.label}</span>
          {item.value != null && <b>{item.value}</b>}
        </li>
      ))}
    </ul>
  )
}

/**
 * Floating detail next to a mark. It is decoration for sighted pointer and
 * keyboard users; the same text goes to a status line for screen readers.
 */
export function Tip(p: { x: number; y: number; flip: boolean; children: ReactNode }) {
  return (
    <div
      className={'u-dash-tip' + (p.flip ? ' u-dash-tip-flip' : '')}
      style={{ left: p.x, top: p.y }}
      aria-hidden="true"
    >
      {p.children}
    </div>
  )
}

export interface TipRow {
  key: string
  paint?: Paint
  label: string
  value: string
}

/** Tooltip body: a heading and one row per series, value first in weight. */
export function TipRows({ title, rows }: { title: string; rows: readonly TipRow[] }) {
  return (
    <>
      <div className="u-dash-tip-title">{title}</div>
      {rows.map((row) => (
        <div className="u-dash-tip-row" key={row.key}>
          {/* A row without a colour keeps the empty key column so labels line up. */}
          {row.paint ? <span className={'u-dash-key ' + paintClass(row.paint)} /> : <span />}
          <span>{row.label}</span>
          <b>{row.value}</b>
        </div>
      ))}
    </>
  )
}

/** Path of a bar that is square at its baseline and rounded at its data end. */
export function barPath(
  x: number,
  y: number,
  width: number,
  height: number,
  radius: number,
  end: 'top' | 'right',
): string {
  // The corner may use the whole length of the bar but only half its thickness.
  const r = Math.max(
    0,
    end === 'top' ? Math.min(radius, width / 2, height) : Math.min(radius, height / 2, width),
  )
  if (end === 'top')
    return (
      `M${x},${y + height}V${y + r}Q${x},${y} ${x + r},${y}` +
      `H${x + width - r}Q${x + width},${y} ${x + width},${y + r}V${y + height}Z`
    )
  return (
    `M${x},${y}H${x + width - r}Q${x + width},${y} ${x + width},${y + r}` +
    `V${y + height - r}Q${x + width},${y + height} ${x + width - r},${y + height}H${x}Z`
  )
}
