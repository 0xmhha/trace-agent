import {
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
  type PointerEvent,
  type RefObject,
} from 'react'
import { t } from '../../../i18n'
import type { TurnPoint } from '../../types'
import { Card, DataTable, Empty, fmtCompact, fmtInt, type Column } from '../../ui'
import { countVars, fmtFull, fmtTimes } from './format'

// Geometry in CSS pixels. The SVG is drawn at the measured width of its box,
// so one user unit is one pixel and pointer positions need no conversion.
const DEFAULT_WIDTH = 720
const MIN_WIDTH = 320
const PAD_LEFT = 56
const PAD_RIGHT = 18
const TITLE_H = 26
const CONTEXT_H = 190
const OUTPUT_H = 100
const PANEL_GAP = 14
const AXIS_H = 42
const CONTEXT_TOP = TITLE_H
const CONTEXT_BOTTOM = CONTEXT_TOP + CONTEXT_H
const OUTPUT_TOP = CONTEXT_BOTTOM + PANEL_GAP + TITLE_H
const OUTPUT_BOTTOM = OUTPUT_TOP + OUTPUT_H
const HEIGHT = OUTPUT_BOTTOM + AXIS_H

const MAX_BAR = 24
const MIN_BAR = 1
const BAR_GAP = 2
const BAR_RADIUS = 4
const DOT_RADIUS = 4
const Y_TICKS = 4
const X_TICK_SPACING = 84
const MIN_TICK_GAP = 30
/** Room kept above the tallest point for its label. */
const HEADROOM = 1.15
const COMPACT_AXIS_FROM = 10_000
const PAGE_STEP = 10
const TIP_OFFSET = 12
const NICE_STEPS = [1, 2, 5, 10] as const

function clamp(value: number, low: number, high: number): number {
  return Math.min(high, Math.max(low, value))
}

/** Smallest 1, 2 or 5 times a power of ten that is at least raw, and at least one. */
function niceStep(raw: number): number {
  if (!(raw > 1)) return 1
  const power = 10 ** Math.floor(Math.log10(raw))
  return (NICE_STEPS.find((m) => m * power >= raw) ?? 10) * power
}

/** Round axis values from zero to at least max. */
function axisTicks(max: number): number[] {
  const step = niceStep(max / Y_TICKS)
  const ticks: number[] = []
  for (let value = 0; value < max + step; value += step) ticks.push(value)
  return ticks
}

/** One number style per axis: in full while the axis stays under 10,000. */
function fmtAxis(value: number, top: number): string {
  return top < COMPACT_AXIS_FROM ? fmtInt(value) : fmtCompact(value).replace(/\.0(?=[KMB]$)/, '')
}

function useBoxWidth(): [RefObject<HTMLDivElement | null>, number] {
  const ref = useRef<HTMLDivElement>(null)
  const [width, setWidth] = useState(DEFAULT_WIDTH)
  useEffect(() => {
    const box = ref.current
    if (!box) return
    const measure = () => {
      // A hidden tab panel reports zero; keep the last real width then.
      if (box.clientWidth > 0) setWidth(Math.max(MIN_WIDTH, box.clientWidth))
    }
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(box)
    return () => observer.disconnect()
  }, [])
  return [ref, width]
}

interface Layout {
  step: number
  x: (index: number) => number
  yContext: (value: number) => number
  yOutput: (value: number) => number
  barWidth: number
  contextTicks: number[]
  outputTicks: number[]
  xTicks: number[]
  line: string
  area: string
  bars: string
  peak: number
}

function layoutOf(turns: TurnPoint[], width: number): Layout {
  const count = turns.length
  const plotWidth = width - PAD_LEFT - PAD_RIGHT
  const step = plotWidth / Math.max(1, count)
  const x = (index: number) => PAD_LEFT + (index + 0.5) * step

  let peak = 0,
    maxContext = 0,
    maxOutput = 0
  turns.forEach((turn, index) => {
    if (turn.context > maxContext) {
      maxContext = turn.context
      peak = index
    }
    maxOutput = Math.max(maxOutput, turn.output)
  })
  const contextTicks = axisTicks(maxContext * HEADROOM)
  const outputTicks = axisTicks(maxOutput)
  const contextTop = contextTicks.at(-1) ?? 1
  const outputTop = outputTicks.at(-1) ?? 1
  const yContext = (value: number) => CONTEXT_BOTTOM - (value / contextTop) * CONTEXT_H
  const yOutput = (value: number) => OUTPUT_BOTTOM - (value / outputTop) * OUTPUT_H

  const line = turns
    .map((turn, i) => `${i ? 'L' : 'M'}${x(i).toFixed(1)},${yContext(turn.context).toFixed(1)}`)
    .join('')
  const area =
    line + `L${x(count - 1).toFixed(1)},${CONTEXT_BOTTOM}L${x(0).toFixed(1)},${CONTEXT_BOTTOM}Z`

  const barWidth = clamp(step - BAR_GAP, MIN_BAR, MAX_BAR)
  const bars = turns
    .map((turn, i) => {
      const top = yOutput(turn.output)
      const height = OUTPUT_BOTTOM - top
      if (height <= 0) return ''
      const left = x(i) - barWidth / 2,
        right = left + barWidth
      // Rounded at the data end, square at the baseline.
      const r = Math.min(BAR_RADIUS, barWidth / 2, height)
      return (
        `M${left.toFixed(1)},${OUTPUT_BOTTOM}V${(top + r).toFixed(1)}` +
        `Q${left.toFixed(1)},${top.toFixed(1)} ${(left + r).toFixed(1)},${top.toFixed(1)}` +
        `H${(right - r).toFixed(1)}` +
        `Q${right.toFixed(1)},${top.toFixed(1)} ${right.toFixed(1)},${(top + r).toFixed(1)}` +
        `V${OUTPUT_BOTTOM}Z`
      )
    })
    .join('')

  const wanted = clamp(Math.floor(plotWidth / X_TICK_SPACING), 2, 10)
  const tickStep = niceStep(count / wanted)
  const xTicks: number[] = []
  for (let call = tickStep; call <= count; call += tickStep) xTicks.push(call)
  // The first call gets a tick when it does not crowd the next one.
  if (tickStep > 1 && (tickStep - 1) * step >= MIN_TICK_GAP) xTicks.unshift(1)

  return { step, x, yContext, yOutput, barWidth, contextTicks, outputTicks, xTicks, line, area, bars, peak }
}

function YAxis(p: { ticks: number[]; y: (value: number) => number; width: number }) {
  const top = p.ticks.at(-1) ?? 1
  return (
    <g>
      {p.ticks.map((tick) => (
        <g key={tick}>
          <line className="u-sd-grid" x1={PAD_LEFT} x2={p.width - PAD_RIGHT} y1={p.y(tick)} y2={p.y(tick)} />
          <text className="u-sd-tick" x={PAD_LEFT - 8} y={p.y(tick)} dy="0.32em" textAnchor="end">
            {fmtAxis(tick, top)}
          </text>
        </g>
      ))}
    </g>
  )
}

function Chart({ turns }: { turns: TurnPoint[] }) {
  const [boxRef, width] = useBoxWidth()
  const [active, setActive] = useState<number | null>(null)
  const titleId = useId(),
    helpId = useId()
  const count = turns.length
  const layout = useMemo(() => layoutOf(turns, width), [turns, width])
  const { x, yContext, yOutput } = layout

  const indexAt = (event: PointerEvent<SVGRectElement>) => {
    const box = event.currentTarget.ownerSVGElement?.getBoundingClientRect()
    if (!box) return null
    return clamp(Math.floor((event.clientX - box.left - PAD_LEFT) / layout.step), 0, count - 1)
  }
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const from = active ?? count - 1
    const moves: Record<string, number> = {
      ArrowLeft: from - 1,
      ArrowRight: from + 1,
      PageUp: from - PAGE_STEP,
      PageDown: from + PAGE_STEP,
      Home: 0,
      End: count - 1,
    }
    const next = moves[event.key]
    if (next == null) return
    event.preventDefault()
    setActive(clamp(next, 0, count - 1))
  }

  const peak = turns[layout.peak]
  const picked = active == null ? undefined : turns[active]
  const totalOutput = turns.reduce((sum, turn) => sum + turn.output, 0)
  const plotMiddle = PAD_LEFT + (width - PAD_LEFT - PAD_RIGHT) / 2
  const peakOnLeft = x(layout.peak) < plotMiddle

  // The marks do not depend on the hovered call, so pointer moves repaint
  // only the crosshair layer below.
  const marks = useMemo(
    () => (
      <>
        <text className="u-sd-panel-title" x={PAD_LEFT} y={CONTEXT_TOP - 10}>
          {t('session.chartContextTitle')}
        </text>
        <YAxis ticks={layout.contextTicks} y={layout.yContext} width={width} />
        <path className="u-sd-area" d={layout.area} />
        <path className="u-sd-line-mark" d={layout.line} />
        <text className="u-sd-panel-title" x={PAD_LEFT} y={OUTPUT_TOP - 10}>
          {t('session.chartOutputTitle')}
        </text>
        <YAxis ticks={layout.outputTicks} y={layout.yOutput} width={width} />
        <path className="u-sd-bars" d={layout.bars} />
        {layout.xTicks.map((call) => (
          <text key={call} className="u-sd-tick" x={layout.x(call - 1)} y={OUTPUT_BOTTOM + 16} textAnchor="middle">
            {fmtInt(call)}
          </text>
        ))}
        <text className="u-sd-axis-title" x={plotMiddle} y={HEIGHT - 6} textAnchor="middle">
          {t('session.chartAxis')}
        </text>
      </>
    ),
    [layout, width, plotMiddle],
  )

  return (
    <div
      ref={boxRef}
      className="u-sd-chart"
      tabIndex={0}
      role="group"
      aria-labelledby={titleId}
      aria-describedby={helpId}
      onKeyDown={onKeyDown}
      onFocus={() => setActive((current) => current ?? count - 1)}
      onBlur={() => setActive(null)}
    >
      <svg width={width} height={HEIGHT} viewBox={`0 0 ${width} ${HEIGHT}`} role="img" aria-labelledby={titleId}>
        <title id={titleId}>
          {t('session.chartTitle', {
            ...countVars(count),
            peak: fmtInt(peak?.context ?? 0),
            total: fmtInt(totalOutput),
          })}
        </title>
        {marks}
        {peak && (
          <g>
            <circle className="u-sd-dot" cx={x(layout.peak)} cy={yContext(peak.context)} r={DOT_RADIUS} />
            <text
              className="u-sd-direct"
              x={x(layout.peak) + (peakOnLeft ? -2 : 2)}
              y={yContext(peak.context) - 10}
              textAnchor={peakOnLeft ? 'start' : 'end'}
            >
              {t('session.chartPeak', { value: fmtCompact(peak.context) })}
            </text>
          </g>
        )}
        {picked && active != null && (
          <g>
            <line className="u-sd-cross" x1={x(active)} x2={x(active)} y1={CONTEXT_TOP} y2={OUTPUT_BOTTOM} />
            <circle className="u-sd-dot" cx={x(active)} cy={yContext(picked.context)} r={DOT_RADIUS} />
            <rect
              className="u-sd-bar-pick"
              x={x(active) - layout.barWidth / 2 - 1}
              y={yOutput(picked.output) - 1}
              width={layout.barWidth + 2}
              height={OUTPUT_BOTTOM - yOutput(picked.output) + 1}
            />
          </g>
        )}
        <rect
          className="u-sd-hit"
          x={PAD_LEFT}
          y={CONTEXT_TOP}
          width={width - PAD_LEFT - PAD_RIGHT}
          height={OUTPUT_BOTTOM - CONTEXT_TOP}
          onPointerMove={(event) => setActive(indexAt(event))}
          onPointerDown={(event) => setActive(indexAt(event))}
          onPointerLeave={() => setActive(null)}
        />
      </svg>
      {picked && active != null && (
        <div
          className={'u-sd-tip' + (x(active) > plotMiddle ? ' u-sd-tip-left' : '')}
          style={{ left: x(active) + (x(active) > plotMiddle ? -TIP_OFFSET : TIP_OFFSET), top: CONTEXT_TOP + 6 }}
          role="status"
        >
          <div className="u-sd-tip-head">
            {t('session.chartTipHead', {
              index: fmtInt(active + 1),
              n: fmtInt(count),
              at: fmtFull(picked.at),
            })}
          </div>
          <div className="u-sd-tip-row">
            <span className="u-sd-key u-sd-key-line" aria-hidden="true" />
            <strong>{fmtInt(picked.context)}</strong> {t('session.chartContext')}
          </div>
          <div className="u-sd-tip-row">
            <span className="u-sd-key u-sd-key-bar" aria-hidden="true" />
            <strong>{fmtInt(picked.output)}</strong> {t('session.chartOutput')}
          </div>
          <div className="u-sd-tip-foot">
            {t('session.chartTipFoot', {
              ...countVars(picked.toolCalls),
              model: picked.model || t('session.unknownModel'),
            })}
          </div>
        </div>
      )}
      <p id={helpId} className="u-hint">
        {t('session.chartHelp')}
      </p>
    </div>
  )
}

interface NumberedTurn extends TurnPoint {
  call: number
}

/** Columns of the table view, built at render so the headers follow the locale. */
function tableColumns(): Column<NumberedTurn>[] {
  return [
    { key: 'call', label: t('session.colCall'), align: 'right', render: (turn) => fmtInt(turn.call) },
    { key: 'at', label: t('session.colAt'), render: (turn) => fmtFull(turn.at) },
    { key: 'model', label: t('session.colModel'), render: (turn) => turn.model || t('session.unknownModel') },
    { key: 'context', label: t('session.colContext'), align: 'right', render: (turn) => fmtInt(turn.context) },
    { key: 'output', label: t('session.colOutput'), align: 'right', render: (turn) => fmtInt(turn.output) },
    { key: 'tools', label: t('session.colToolCalls'), align: 'right', render: (turn) => fmtInt(turn.toolCalls) },
  ]
}

/** The token flow tab: context size and output of each model call of the root agent. */
export function FlowTab(p: { turns: TurnPoint[]; truncated: boolean; compactions: number }) {
  const { turns } = p
  const [tableOpen, setTableOpen] = useState(false)
  const rows = useMemo(
    () => (tableOpen ? turns.map((turn, index) => ({ ...turn, call: index + 1 })) : []),
    [turns, tableOpen],
  )
  if (!turns.length)
    return (
      <Card title={t('session.flowTitle')}>
        <Empty>{t('session.noTurns')}</Empty>
      </Card>
    )
  const last = turns.at(-1)
  const peak = turns.reduce((max, turn) => Math.max(max, turn.context), 0)
  const output = turns.reduce((sum, turn) => sum + turn.output, 0)
  return (
    <Card title={t('session.flowTitle')} hint={t('session.flowHint')}>
      {p.truncated && (
        <p className="u-note u-note-warn" role="status">
          {t('session.truncatedTurns', countVars(turns.length))}
        </p>
      )}
      <ul className="u-sd-facts">
        <li>
          {t('session.factModelCalls')} <b>{fmtTimes(turns.length)}</b>
        </li>
        <li>
          {t('session.factPeak')} <b>{fmtCompact(peak)}</b>
        </li>
        <li>
          {t('session.factLast')} <b>{fmtCompact(last?.context ?? 0)}</b>
        </li>
        <li>
          {t('session.factOutput')} <b>{fmtCompact(output)}</b>
        </li>
        <li>
          {t('session.factCompactions')} <b>{fmtTimes(p.compactions)}</b>
        </li>
      </ul>
      <ul className="u-sd-legend" aria-label={t('session.legendLabel')}>
        <li>
          <span className="u-sd-key u-sd-key-line" aria-hidden="true" />
          {t('session.legendLine')}
        </li>
        <li>
          <span className="u-sd-key u-sd-key-bar" aria-hidden="true" />
          {t('session.legendBar')}
        </li>
      </ul>
      <Chart turns={turns} />
      <details
        className="u-sd-fold"
        onToggle={(event) => setTableOpen(event.currentTarget.open)}
      >
        <summary>{t('session.showTable')}</summary>
        {tableOpen && <DataTable columns={tableColumns()} rows={rows} rowKey={(turn) => String(turn.call)} />}
      </details>
    </Card>
  )
}
