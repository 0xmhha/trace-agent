import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { errorText, formatDate, formatNumber, t, type MessageKey } from '../i18n'
import type { Coverage, Range, TokenTotals } from './types'
import './usage.css'

const HOUR_MS = 3_600_000
const DAY_MS = 24 * HOUR_MS

/** Range ending now and covering the given number of days. */
export function lastDays(days: number, now = new Date()): Range {
  return {
    from: new Date(now.getTime() - days * DAY_MS).toISOString(),
    until: now.toISOString(),
  }
}

export function sumTokens(t: TokenTotals): number {
  return t.input + t.output + t.cacheRead + t.cacheCreate
}
export function fmtInt(n: number): string {
  return formatNumber(Math.round(n))
}

const ONE_DECIMAL: Intl.NumberFormatOptions = { minimumFractionDigits: 1, maximumFractionDigits: 1 }
/** Scale suffixes stay Latin in every locale so that axis ticks keep one width. */
const COMPACT_STEPS: readonly { size: number; suffix: string }[] = [
  { size: 1e9, suffix: 'B' },
  { size: 1e6, suffix: 'M' },
  { size: 1e3, suffix: 'K' },
]
/** 1234 → 1.2K, 3400000 → 3.4M. */
export function fmtCompact(n: number): string {
  const abs = Math.abs(n)
  const step = COMPACT_STEPS.find((s) => abs >= s.size)
  if (!step) return formatNumber(Math.round(n), { useGrouping: false })
  return formatNumber(n / step.size, { ...ONE_DECIMAL, useGrouping: false }) + step.suffix
}
export function fmtMs(ms: number | null | undefined): string {
  if (ms == null) return '—'
  if (ms < 1000) return t('usage.durationMs', { value: formatNumber(Math.round(ms)) })
  if (ms < 60_000) return t('usage.durationSeconds', { value: formatNumber(ms / 1000, ONE_DECIMAL) })
  if (ms < HOUR_MS) return t('usage.durationMinutes', { value: formatNumber(Math.round(ms / 60_000)) })
  return t('usage.durationHours', { value: formatNumber(ms / HOUR_MS, ONE_DECIMAL) })
}
/** Cost in US dollars, two decimals below $10. The symbol is "$" in every locale. */
export function fmtCost(usd: number | null | undefined): string {
  if (usd == null) return t('usage.costUnknown')
  const digits = usd < 10 ? 2 : 0
  return formatNumber(usd, {
    style: 'currency',
    currency: 'USD',
    currencyDisplay: 'narrowSymbol',
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
  })
}
/** Ratio in [0, 1] → "42.0%". */
export function fmtPct(ratio: number | null | undefined): string {
  return ratio == null ? '—' : formatNumber(ratio, { ...ONE_DECIMAL, style: 'percent' })
}
export function ratio(part: number, whole: number): number | null {
  return whole > 0 ? part / whole : null
}
export function fmtDateTime(at: string): string {
  const ms = Date.parse(at)
  if (!Number.isFinite(ms) || ms <= 0) return t('usage.timeUnknown')
  return formatDate(ms, {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  })
}
export function fmtDay(at: string): string {
  const ms = Date.parse(at)
  if (!Number.isFinite(ms) || ms <= 0) return ''
  return formatDate(ms, {
    month: '2-digit',
    day: '2-digit',
  })
}
/** Last path segment, for showing a project path in a narrow column. */
export function baseName(path: string): string {
  const parts = path.split('/').filter(Boolean)
  return parts.at(-1) ?? path
}

export interface Loaded<T> {
  data: T | null
  error: string
  loading: boolean
  reload: () => void
}
/**
 * Runs load whenever deps change. The previous data stays visible while a
 * newer request is in flight, and a slower stale response is dropped.
 */
export function useLoad<T>(load: () => Promise<T>, deps: unknown[]): Loaded<T> {
  const [data, setData] = useState<T | null>(null),
    [error, setError] = useState(''),
    [loading, setLoading] = useState(true),
    [tick, setTick] = useState(0)
  const latest = useRef(0)
  useEffect(() => {
    const id = ++latest.current
    setLoading(true)
    load()
      .then((value) => {
        if (id !== latest.current) return
        setData(value)
        setError('')
      })
      .catch((reason: unknown) => {
        if (id !== latest.current) return
        setError(errorText(reason))
      })
      .finally(() => {
        if (id === latest.current) setLoading(false)
      })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, tick])
  const reload = useCallback(() => setTick((n) => n + 1), [])
  return { data, error, loading, reload }
}

export function Card(p: {
  title?: string
  hint?: string
  action?: ReactNode
  className?: string
  children: ReactNode
}) {
  return (
    <section className={'u-card ' + (p.className ?? '')}>
      {(p.title || p.action) && (
        <header className="u-card-head">
          <div>
            {p.title && <h2>{p.title}</h2>}
            {p.hint && <p className="u-hint">{p.hint}</p>}
          </div>
          {p.action}
        </header>
      )}
      {p.children}
    </section>
  )
}

export type Tone = 'plain' | 'good' | 'warn' | 'bad' | 'muted'

export function Stat(p: {
  label: string
  value: string
  hint?: string
  tone?: Tone
  onClick?: () => void
}) {
  const body = (
    <>
      <span className="u-stat-label">{p.label}</span>
      <strong className={'u-stat-value u-tone-' + (p.tone ?? 'plain')}>
        {p.value}
      </strong>
      {p.hint && <small className="u-hint">{p.hint}</small>}
    </>
  )
  return p.onClick ? (
    <button className="u-stat" onClick={p.onClick}>
      {body}
    </button>
  ) : (
    <div className="u-stat">{body}</div>
  )
}

export function Pill(p: { tone?: Tone; children: ReactNode; title?: string }) {
  return (
    <span className={'u-pill u-tone-' + (p.tone ?? 'plain')} title={p.title}>
      {p.children}
    </span>
  )
}

/** Message of each coverage reason code sent by the store. */
const coverageReasons: Record<string, MessageKey> = {
  unreadable_files: 'usage.coverageUnreadableFiles',
  skipped_lines: 'usage.coverageSkippedLines',
  outside_window: 'usage.coverageOutsideWindow',
}

/** Explains why the numbers on a page may be incomplete. Renders nothing when complete. */
export function CoverageNote({ coverage }: { coverage: Coverage | undefined }) {
  if (!coverage || coverage.state === 'complete') return null
  if (coverage.state === 'disabled')
    return <div className="u-note">{t('usage.coverageDisabled')}</div>
  if (coverage.state === 'scanning')
    return (
      <div className="u-note" role="status">
        {t('usage.coverageScanning')}
      </div>
    )
  const reasons = coverage.reasons.map((r) => {
    const key = coverageReasons[r]
    return key ? t(key) : r
  })
  const files = coverage.filesUnreadable
  const lines = coverage.linesSkipped
  return (
    <div className="u-note u-note-warn" role="status">
      <b>{t('usage.coveragePartial')}</b>{' '}
      {t('usage.coverageReasons', {
        reasons: reasons.join(t('usage.listSeparator')) || t('usage.coverageSomeUnread'),
      })}
      {files > 0 && ' ' + t('usage.coverageFiles', { count: files, value: fmtInt(files) })}
      {lines > 0 && ' ' + t('usage.coverageLines', { count: lines, value: fmtInt(lines) })}
    </div>
  )
}

/** Empty-state message for each coverage state. */
const emptyText: Record<Coverage['state'], MessageKey> = {
  scanning: 'usage.emptyScanning',
  disabled: 'usage.emptyDisabled',
  partial: 'usage.emptyPartial',
  complete: 'usage.emptyComplete',
}

/**
 * Empty-state text that separates "nothing happened" from "could not read"
 * and "still reading".
 */
export function Empty(p: { coverage?: Coverage; children?: ReactNode }) {
  const state = p.coverage?.state ?? 'complete'
  return <p className="u-empty">{p.children ?? t(emptyText[state] ?? 'usage.emptyComplete')}</p>
}

export function ErrorNote(p: { error: string; retry?: () => void }) {
  if (!p.error) return null
  return (
    <div className="u-note u-note-bad" role="alert">
      {p.error}
      {p.retry && <button onClick={p.retry}>{t('usage.retry')}</button>}
    </div>
  )
}

export function Tabs<T extends string>(p: {
  tabs: readonly { id: T; label: string; count?: number }[]
  value: T
  onChange: (id: T) => void
}) {
  return (
    <div className="u-tabs" role="tablist">
      {p.tabs.map((t) => (
        <button
          key={t.id}
          role="tab"
          aria-selected={t.id === p.value}
          className={t.id === p.value ? 'selected' : ''}
          onClick={() => p.onChange(t.id)}
        >
          {t.label}
          {t.count != null && <span className="u-count">{fmtInt(t.count)}</span>}
        </button>
      ))}
    </div>
  )
}

export interface Column<T> {
  key: string
  label: string
  align?: 'left' | 'right'
  render: (row: T) => ReactNode
}
export function DataTable<T>(p: {
  columns: Column<T>[]
  rows: T[]
  rowKey: (row: T) => string
  onRow?: (row: T) => void
  coverage?: Coverage
}) {
  if (!p.rows.length) return <Empty coverage={p.coverage} />
  return (
    <div className="u-table-wrap">
      <table className="u-table">
        <thead>
          <tr>
            {p.columns.map((c) => (
              <th key={c.key} className={c.align === 'right' ? 'num' : ''}>
                {c.label}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {p.rows.map((row) => (
            <tr
              key={p.rowKey(row)}
              className={p.onRow ? 'clickable' : ''}
              onClick={p.onRow ? () => p.onRow?.(row) : undefined}
            >
              {p.columns.map((c) => (
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
