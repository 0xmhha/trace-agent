import { useMemo, useState, type FocusEvent, type KeyboardEvent, type PointerEvent } from 'react'
import { t, type MessageKey } from '../../../i18n'
import type { Bin, Coverage } from '../../types'
import { Card, DataTable, Empty, fmtCompact, fmtInt, fmtPct, ratio, type Column } from '../../ui'
import {
  HOUR_MS,
  UNIT,
  binName,
  binWidthMs,
  binWord,
  dayTick,
  fmtTick,
  hourTick,
  isDailyBin,
  niceTicks,
  unitName,
  withUnit,
  type Unit,
} from './format'
import { Legend, Tip, TipRows, barPath, paintClass, type Paint, type TipRow } from './parts'
import { useWidth } from './useWidth'

/** What the bars of the trend chart count. Stored in the route as its tab. */
export type MeasureId = 'tokens' | 'sessions' | 'tools'

interface Series {
  key: string
  label: MessageKey
  paint: Paint
  value: (bin: Bin) => number
}

interface Measure {
  id: MeasureId
  label: MessageKey
  /** Subtitle after the bucket word, e.g. "token usage" in "Daily token usage". */
  subject: MessageKey
  integer: boolean
  /** Counter of the values; the table prints it once, in the column heads. */
  unit: Unit
  /** Exact figure with its unit, for the tooltip. */
  exact: (n: number) => string
  /** Short figure for the legend. */
  brief: (n: number) => string
  /** Series in legend order; colours follow this order. */
  series: readonly Series[]
  /** Indexes into series from the baseline up. */
  stack: readonly number[]
  /** Rows added under the total in the tooltip. */
  extra?: (values: readonly number[], total: number) => TipRow[]
}

const tokenText = (n: number) => withUnit(n, UNIT.tokens)
const timesText = (n: number) => withUnit(n, UNIT.times)
const itemsText = (n: number) => withUnit(n, UNIT.items)

const MEASURES: Record<MeasureId, Measure> = {
  tokens: {
    id: 'tokens',
    label: 'dashboard.measureTokens',
    subject: 'dashboard.subjectTokens',
    integer: false,
    unit: UNIT.tokens,
    exact: tokenText,
    brief: fmtCompact,
    series: [
      { key: 'input', label: 'dashboard.input', paint: 's1', value: (b) => b.tokens.input },
      { key: 'output', label: 'dashboard.output', paint: 's2', value: (b) => b.tokens.output },
      { key: 'cacheRead', label: 'dashboard.cacheRead', paint: 's3', value: (b) => b.tokens.cacheRead },
      { key: 'cacheCreate', label: 'dashboard.cacheCreate', paint: 's4', value: (b) => b.tokens.cacheCreate },
    ],
    // The third and fourth series colours are too close to sit next to each
    // other, so the two cache segments are kept apart by the other two.
    stack: [2, 0, 1, 3],
  },
  sessions: {
    id: 'sessions',
    label: 'dashboard.measureSessions',
    subject: 'dashboard.subjectSessions',
    integer: true,
    unit: UNIT.items,
    exact: itemsText,
    brief: itemsText,
    series: [{ key: 'sessions', label: 'dashboard.measureSessions', paint: 's1', value: (b) => b.sessions }],
    stack: [0],
  },
  tools: {
    id: 'tools',
    label: 'dashboard.kpiToolCalls',
    subject: 'dashboard.subjectTools',
    integer: true,
    unit: UNIT.times,
    exact: timesText,
    brief: timesText,
    series: [
      { key: 'ok', label: 'dashboard.succeeded', paint: 's1', value: (b) => b.toolCalls - b.toolErrors },
      { key: 'failed', label: 'dashboard.failed', paint: 'bad', value: (b) => b.toolErrors },
    ],
    stack: [0, 1],
    extra: (values, total) => [
      { key: 'rate', label: t('dashboard.failureRate'), value: fmtPct(ratio(values[1] ?? 0, total)) },
    ],
  },
}
const MEASURE_ORDER: readonly MeasureId[] = ['tokens', 'sessions', 'tools']
const DEFAULT_MEASURE: MeasureId = 'tokens'

/** Reads the measure from the route tab; anything unknown is the default. */
export function parseMeasure(tab: string | undefined): MeasureId {
  return MEASURE_ORDER.find((id) => id === tab) ?? DEFAULT_MEASURE
}
/** Route tab for a measure; the default is left out to keep the address short. */
export function measureTab(id: MeasureId): string | undefined {
  return id === DEFAULT_MEASURE ? undefined : id
}

/** Chart subtitle: the bucket word and what the bars count, e.g. "Daily active sessions". */
function subjectText(measure: Measure, widthMs: number): string {
  return t('dashboard.trendHint', { bin: binWord(widthMs), subject: t(measure.subject) })
}

interface Stack {
  from: number
  values: number[]
  total: number
}

const PLOT_HEIGHT = 180
const PAD_TOP = 10
const PAD_LEFT = 46
const PAD_RIGHT = 16
const AXIS_GAP = 6
const AXIS_ROW = 15
const TICK_TARGET = 4
const BAR_MAX = 24
const BAR_RADIUS = 4
/** Surface-coloured gap that separates stacked segments. */
const SEGMENT_GAP = 2
/** Segments thinner than this are not drawn; the tooltip and table carry them. */
const SEGMENT_MIN = 0.5
const DAY_LABEL_PX = 44
const HOUR_LABEL_PX = 34
const HOUR_STEPS = [1, 2, 3, 4, 6, 8, 12, 24]
const TIP_TOP = 4

interface AxisLabel {
  index: number
  text: string
  /** Date printed under an hour label where the day changes. */
  day?: string
}

function axisLabels(stacks: readonly Stack[], widthMs: number, slot: number): AxisLabel[] {
  if (slot <= 0) return []
  const last = stacks.length - 1
  if (isDailyBin(widthMs)) {
    // Count from the newest bucket so the latest day always carries a label.
    const every = Math.max(1, Math.ceil(DAY_LABEL_PX / slot))
    return stacks.flatMap((s, index) =>
      (last - index) % every === 0 ? [{ index, text: dayTick(s.from) }] : [],
    )
  }
  const binHours = widthMs / HOUR_MS
  const stepHours = HOUR_STEPS.find(
    (h) => Number.isInteger(h / binHours) && (h / binHours) * slot >= HOUR_LABEL_PX,
  )
  const every = Math.max(1, Math.ceil(HOUR_LABEL_PX / slot))
  const labels: AxisLabel[] = []
  let lastDay = ''
  let lastDayX = -Infinity
  stacks.forEach((s, index) => {
    const hour = new Date(s.from).getHours()
    const shown = stepHours ? hour % stepHours === 0 : index % every === 0
    if (!shown) return
    const label: AxisLabel = { index, text: hourTick(s.from) }
    const day = dayTick(s.from)
    const x = index * slot
    if (day !== lastDay && x - lastDayX >= DAY_LABEL_PX) {
      label.day = day
      lastDay = day
      lastDayX = x
    }
    labels.push(label)
  })
  return labels
}

function TrendPlot(p: { measure: Measure; stacks: readonly Stack[]; widthMs: number }) {
  const { measure, stacks, widthMs } = p
  const [ref, width] = useWidth<HTMLDivElement>()
  const [active, setActive] = useState<number | null>(null)

  const count = stacks.length
  const current = active != null && active < count ? active : null
  const max = stacks.reduce((m, s) => Math.max(m, s.total), 0)
  const ticks = niceTicks(max, TICK_TARGET, measure.integer)
  const top = ticks.at(-1) || 1
  const daily = isDailyBin(widthMs)
  const height = PAD_TOP + PLOT_HEIGHT + AXIS_GAP + AXIS_ROW * (daily ? 1 : 2)
  const plotWidth = Math.max(0, width - PAD_LEFT - PAD_RIGHT)
  const slot = count ? plotWidth / count : 0
  const gap = slot >= 6 ? 2 : slot >= 3 ? 1 : 0
  const barWidth = Math.max(1, Math.min(BAR_MAX, slot - gap))
  const base = PAD_TOP + PLOT_HEIGHT
  const y = (value: number) => base - (value / top) * PLOT_HEIGHT
  const center = (index: number) => PAD_LEFT + (index + 0.5) * slot
  const labels = useMemo(() => axisLabels(stacks, widthMs, slot), [stacks, widthMs, slot])

  const onPointerMove = (e: PointerEvent<HTMLDivElement>) => {
    const x = e.clientX - e.currentTarget.getBoundingClientRect().left - PAD_LEFT
    const index = slot > 0 ? Math.floor(x / slot) : -1
    setActive(index >= 0 && index < count ? index : null)
  }
  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    const last = count - 1
    const from = current ?? last
    const moves: Record<string, number> = {
      ArrowLeft: Math.max(0, from - 1),
      ArrowRight: Math.min(last, from + 1),
      Home: 0,
      End: last,
    }
    if (e.key === 'Escape') return setActive(null)
    const next = moves[e.key]
    if (next == null) return
    e.preventDefault()
    setActive(next)
  }
  const onFocus = (e: FocusEvent<HTMLDivElement>) => {
    // A mouse click also focuses the plot; only keyboard focus picks a bucket.
    if (current == null && e.currentTarget.matches(':focus-visible')) setActive(count - 1)
  }

  const picked = current != null ? stacks[current] : undefined
  const rows: TipRow[] = picked
    ? [
        ...measure.series.map((s, i) => ({
          key: s.key,
          paint: s.paint,
          label: t(s.label),
          value: measure.exact(picked.values[i] ?? 0),
        })),
        ...(measure.series.length > 1
          ? [{ key: 'total', label: t('dashboard.total'), value: measure.exact(picked.total) }]
          : []),
        ...(measure.extra?.(picked.values, picked.total) ?? []),
      ]
    : []
  const title = picked ? binName(picked.from, widthMs) : ''
  const summary = t('dashboard.trendAria', {
    subject: subjectText(measure, widthMs),
    count,
    max: measure.exact(max),
  })

  return (
    <div
      ref={ref}
      className="u-dash-plot"
      style={{ minHeight: height }}
      tabIndex={0}
      role="group"
      aria-label={summary}
      onPointerMove={onPointerMove}
      onPointerLeave={() => setActive(null)}
      onKeyDown={onKeyDown}
      onFocus={onFocus}
      onBlur={() => setActive(null)}
    >
      {width > 0 && (
        <svg className="u-dash-svg" width={width} height={height} aria-hidden="true">
          {ticks.map((t) => (
            <g key={t}>
              {t > 0 && (
                <line
                  className="u-dash-gridline"
                  x1={PAD_LEFT}
                  x2={width - PAD_RIGHT}
                  y1={y(t)}
                  y2={y(t)}
                />
              )}
              <text className="u-dash-tick" x={PAD_LEFT - 6} y={y(t)} dy="0.32em" textAnchor="end">
                {fmtTick(t)}
              </text>
            </g>
          ))}
          {current != null && (
            <rect
              className="u-dash-band"
              x={PAD_LEFT + current * slot}
              y={PAD_TOP}
              width={slot}
              height={PLOT_HEIGHT}
            />
          )}
          {stacks.map((s, index) => {
            const x = center(index) - barWidth / 2
            const drawn = measure.stack.filter(
              (i) => ((s.values[i] ?? 0) / top) * PLOT_HEIGHT >= SEGMENT_MIN,
            )
            let floor = base
            return (
              <g key={index}>
                {drawn.map((i, order) => {
                  const full = ((s.values[i] ?? 0) / top) * PLOT_HEIGHT
                  const isTop = order === drawn.length - 1
                  // The gap comes off the top of a segment, and only where the
                  // segment is tall enough to keep most of its height.
                  const h = !isTop && full > SEGMENT_GAP + 1 ? full - SEGMENT_GAP : full
                  const bottom = floor
                  floor -= full
                  const paint = paintClass(measure.series[i]?.paint ?? 's1')
                  return isTop ? (
                    <path
                      key={i}
                      className={'u-dash-fill ' + paint}
                      d={barPath(x, bottom - h, barWidth, h, BAR_RADIUS, 'top')}
                    />
                  ) : (
                    <rect
                      key={i}
                      className={'u-dash-fill ' + paint}
                      x={x}
                      y={bottom - h}
                      width={barWidth}
                      height={h}
                    />
                  )
                })}
              </g>
            )
          })}
          <line className="u-dash-axis" x1={PAD_LEFT} x2={width - PAD_RIGHT} y1={base} y2={base} />
          {labels.map((l) => (
            <g key={l.index}>
              <text
                className="u-dash-tick"
                x={center(l.index)}
                y={base + AXIS_GAP + AXIS_ROW - 4}
                textAnchor="middle"
              >
                {l.text}
              </text>
              {l.day && (
                <text
                  className="u-dash-tick u-dash-tick-strong"
                  x={center(l.index)}
                  y={base + AXIS_GAP + AXIS_ROW * 2 - 4}
                  textAnchor="middle"
                >
                  {l.day}
                </text>
              )}
            </g>
          ))}
        </svg>
      )}
      {current != null && (
        <Tip x={center(current)} y={TIP_TOP} flip={center(current) > width / 2}>
          <TipRows title={title} rows={rows} />
        </Tip>
      )}
      <p className="u-dash-sr" role="status">
        {picked
          ? t('dashboard.trendStatus', {
              bin: title,
              values: rows
                .map((row) => t('dashboard.labelValue', { label: row.label, value: row.value }))
                .join(t('usage.listSeparator')),
            })
          : ''}
      </p>
    </div>
  )
}

function TrendTable(p: { measure: Measure; stacks: readonly Stack[]; widthMs: number }) {
  const { measure, stacks, widthMs } = p
  const columns: Column<Stack>[] = [
    { key: 'bin', label: t('dashboard.bin'), render: (s) => binName(s.from, widthMs) },
    ...measure.series.map(
      (series, i): Column<Stack> => ({
        key: series.key,
        label: t('dashboard.columnWithUnit', { label: t(series.label), unit: unitName(measure.unit) }),
        align: 'right',
        render: (s) => fmtInt(s.values[i] ?? 0),
      }),
    ),
  ]
  if (measure.series.length > 1)
    columns.push({
      key: 'total',
      label: t('dashboard.columnWithUnit', { label: t('dashboard.total'), unit: unitName(measure.unit) }),
      align: 'right',
      render: (s) => fmtInt(s.total),
    })
  // Newest first: the table is read for recent buckets, the chart for shape.
  const rows = [...stacks].reverse()
  return (
    <div className="u-dash-table-scroll">
      <DataTable columns={columns} rows={rows} rowKey={(s) => String(s.from)} />
    </div>
  )
}

/** Stacked bars per time bucket with a switch between tokens, sessions and tool calls. */
export function TrendChart(p: {
  series: readonly Bin[]
  measure: MeasureId
  onMeasure: (id: MeasureId) => void
  coverage: Coverage
  className?: string
}) {
  const measure = MEASURES[p.measure]
  const [tableOpen, setTableOpen] = useState(false)
  const widthMs = binWidthMs(p.series)
  const stacks = useMemo(
    () =>
      p.series.map((bin): Stack => {
        const values = measure.series.map((s) => Math.max(0, s.value(bin)))
        return {
          from: Date.parse(bin.from),
          values,
          total: values.reduce((sum, v) => sum + v, 0),
        }
      }),
    [p.series, measure],
  )
  const totals = measure.series.map((_, i) => stacks.reduce((sum, s) => sum + (s.values[i] ?? 0), 0))
  const empty = !stacks.some((s) => s.total > 0)

  const toggle = (
    <div className="u-dash-toggle" role="group" aria-label={t('dashboard.measureGroup')}>
      {MEASURE_ORDER.map((id) => (
        <button
          key={id}
          type="button"
          aria-pressed={id === measure.id}
          className={id === measure.id ? 'u-dash-on' : ''}
          onClick={() => p.onMeasure(id)}
        >
          {t(MEASURES[id].label)}
        </button>
      ))}
    </div>
  )

  return (
    <Card
      title={t('dashboard.trendTitle')}
      hint={subjectText(measure, widthMs)}
      action={toggle}
      className={p.className}
    >
      {empty ? (
        <Empty coverage={p.coverage} />
      ) : (
        <>
          {measure.series.length > 1 && (
            <Legend
              label={t('dashboard.trendLegend')}
              items={measure.series.map((s, i) => ({
                key: s.key,
                paint: s.paint,
                label: t(s.label),
                value: measure.brief(totals[i] ?? 0),
              }))}
            />
          )}
          <TrendPlot measure={measure} stacks={stacks} widthMs={widthMs} />
          <details
            className="u-dash-details"
            onToggle={(e) => setTableOpen(e.currentTarget.open)}
          >
            <summary>{t('dashboard.showTable')}</summary>
            {tableOpen && <TrendTable measure={measure} stacks={stacks} widthMs={widthMs} />}
          </details>
        </>
      )}
    </Card>
  )
}
