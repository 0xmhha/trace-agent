import { useEffect, useRef, useState } from 'react'
import type { SessionRow, SessionSort } from '../../types'
import {
  Pill,
  baseName,
  fmtCompact,
  fmtCost,
  fmtDateTime,
  fmtInt,
  fmtMs,
  fmtPct,
  ratio,
  sumTokens,
  type Tone,
} from '../../ui'
import { formatNumber, t, type MessageKey } from '../../../i18n'
import { SplitBar, splitSummary } from './SplitBar'

/** Fewer tool calls than this say too little about a session's failure rate to flag it. */
const FAILURE_MIN_CALLS = 10
const FAILURE_WARN_RATIO = 0.1
const FAILURE_BAD_RATIO = 0.2

type ColumnId =
  | 'session'
  | 'activity'
  | 'duration'
  | 'prompts'
  | 'modelCalls'
  | 'tokens'
  | 'tools'
  | 'subagents'
  | 'compactions'
  | 'cost'
  | 'models'

const columns: readonly { id: ColumnId; label: MessageKey; numeric: boolean }[] = [
  { id: 'session', label: 'sessions.colSession', numeric: false },
  { id: 'activity', label: 'sessions.colActivity', numeric: false },
  { id: 'duration', label: 'sessions.colDuration', numeric: true },
  { id: 'prompts', label: 'sessions.colPrompts', numeric: true },
  { id: 'modelCalls', label: 'sessions.colModelCalls', numeric: true },
  { id: 'tokens', label: 'sessions.colTokens', numeric: true },
  { id: 'tools', label: 'sessions.colTools', numeric: true },
  { id: 'subagents', label: 'sessions.colSubagents', numeric: true },
  { id: 'compactions', label: 'sessions.colCompactions', numeric: true },
  { id: 'cost', label: 'sessions.colCost', numeric: true },
  { id: 'models', label: 'sessions.colModels', numeric: false },
]

/** The column each order sorts by, so the header can say which one is in effect. */
const sortedColumn: Record<SessionSort, ColumnId> = {
  last: 'activity',
  tokens: 'tokens',
  errors: 'tools',
  cost: 'cost',
}

function failureTone(calls: number, errors: number): Tone {
  if (errors <= 0) return 'muted'
  const rate = ratio(errors, calls) ?? 0
  if (calls < FAILURE_MIN_CALLS || rate < FAILURE_WARN_RATIO) return 'plain'
  return rate < FAILURE_BAD_RATIO ? 'warn' : 'bad'
}

function Failures({ calls, errors }: { calls: number; errors: number }) {
  const tone = failureTone(calls, errors)
  const text =
    errors > 0
      ? t('sessions.failuresSome', { count: errors, n: fmtInt(errors), pct: fmtPct(ratio(errors, calls)) })
      : t('sessions.failuresNone')
  // A high rate gets a pill, so it stands out by shape as well as colour.
  if (tone === 'warn' || tone === 'bad')
    return (
      <Pill tone={tone} title={t('sessions.failureRateHigh')}>
        {text}
      </Pill>
    )
  return <span className={'u-sess-sub' + (tone === 'plain' ? ' u-sess-ink' : '')}>{text}</span>
}

/** A counted cell. unit is the message that puts the locale's unit on the number. */
function Count({ value, unit }: { value: number; unit: MessageKey }) {
  return (
    <span className={value === 0 ? 'u-sess-zero' : undefined}>
      {t(unit, { count: value, n: formatNumber(Math.round(value)) })}
    </span>
  )
}

function duration(row: SessionRow): string {
  const ms = Date.parse(row.lastAt) - Date.parse(row.startedAt)
  return Number.isFinite(ms) && ms >= 0 ? fmtMs(ms) : '—'
}

function Row({ row, onOpen }: { row: SessionRow; onOpen: (id: string) => void }) {
  const total = sumTokens(row.tokens)
  return (
    // The button inside the first cell is what the keyboard reaches; its
    // click bubbles to the row, so pointer and keyboard share one handler.
    <tr className="clickable u-sess-row" onClick={() => onOpen(row.id)}>
      <th scope="row" className="u-sess-main">
        <button type="button" className="u-sess-open" title={row.title || undefined}>
          <span className={'u-sess-title' + (row.title ? '' : ' u-sess-untitled')}>
            {row.title || t('sessions.untitled')}
          </span>
        </button>
        <span className="u-sess-project" title={row.projectPath || row.projectKey || undefined}>
          {baseName(row.projectPath || row.projectKey) || t('sessions.noProject')}
        </span>
      </th>
      <td className="u-sess-when">
        <span>{fmtDateTime(row.lastAt)}</span>
        <span className="u-sess-sub">{t('sessions.started', { time: fmtDateTime(row.startedAt) })}</span>
      </td>
      <td className="num">{duration(row)}</td>
      <td className="num">
        <Count value={row.prompts} unit="sessions.times" />
      </td>
      <td className="num">
        <Count value={row.modelCalls} unit="sessions.times" />
      </td>
      <td className="num u-sess-tokens" title={splitSummary(row.tokens)}>
        <span className={total === 0 ? 'u-sess-zero' : undefined}>{fmtCompact(total)}</span>
        <SplitBar tokens={row.tokens} />
      </td>
      <td className="num u-sess-tools">
        <Count value={row.toolCalls} unit="sessions.times" />
        <Failures calls={row.toolCalls} errors={row.toolErrors} />
      </td>
      <td className="num">
        <Count value={row.subagents} unit="sessions.items" />
      </td>
      <td className="num">
        <Count value={row.compactions} unit="sessions.times" />
      </td>
      <td className="num">
        <span className={row.costUsd == null ? 'u-sess-zero' : undefined}>
          {fmtCost(row.costUsd)}
        </span>
      </td>
      <td>
        {row.models.length === 0 ? (
          <span className="u-sess-zero">—</span>
        ) : (
          <span className="u-sess-models">
            {row.models.map((model) => (
              <Pill key={model} tone="muted">
                {model}
              </Pill>
            ))}
          </span>
        )}
      </td>
    </tr>
  )
}

/** True while the element's content is wider than the element, i.e. it scrolls sideways. */
function useOverflows<T extends HTMLElement>() {
  const ref = useRef<T>(null)
  const [overflows, setOverflows] = useState(false)
  useEffect(() => {
    const el = ref.current
    if (!el) return
    const measure = () => setOverflows(el.scrollWidth > el.clientWidth + 1)
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(el)
    if (el.firstElementChild) observer.observe(el.firstElementChild)
    return () => observer.disconnect()
  }, [])
  return [ref, overflows] as const
}

/**
 * The session list as a table. Every row opens its session. When the table is
 * wider than its card it scrolls sideways and the session column stays put.
 */
export function SessionTable(p: {
  rows: SessionRow[]
  sort: SessionSort
  busy: boolean
  onOpen: (id: string) => void
}) {
  const [wrap, overflows] = useOverflows<HTMLDivElement>()
  const sorted = sortedColumn[p.sort]
  return (
    <div
      ref={wrap}
      className={
        'u-table-wrap u-sess-wrap' +
        (overflows ? ' u-sess-scrolls' : '') +
        (p.busy ? ' u-sess-busy' : '')
      }
      aria-busy={p.busy}
    >
      <table className="u-table u-sess-table">
        <thead>
          <tr>
            {columns.map((c) => (
              <th
                key={c.id}
                scope="col"
                className={(c.numeric ? 'num' : '') + (c.id === 'session' ? ' u-sess-main' : '')}
                aria-sort={c.id === sorted ? 'descending' : undefined}
              >
                {t(c.label)}
                {c.id === sorted && (
                  <span className="u-sess-sorted" aria-hidden="true">
                    {' '}
                    ▼
                  </span>
                )}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {p.rows.map((row) => (
            <Row key={row.id} row={row} onOpen={p.onOpen} />
          ))}
        </tbody>
      </table>
    </div>
  )
}
