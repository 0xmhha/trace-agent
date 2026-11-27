import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
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
  return Math.round(n).toLocaleString('ko-KR')
}
/** 1234 → 1.2K, 3400000 → 3.4M. */
export function fmtCompact(n: number): string {
  const abs = Math.abs(n)
  if (abs >= 1e9) return (n / 1e9).toFixed(1) + 'B'
  if (abs >= 1e6) return (n / 1e6).toFixed(1) + 'M'
  if (abs >= 1e3) return (n / 1e3).toFixed(1) + 'K'
  return String(Math.round(n))
}
export function fmtMs(ms: number | null | undefined): string {
  if (ms == null) return '—'
  if (ms < 1000) return Math.round(ms) + 'ms'
  if (ms < 60_000) return (ms / 1000).toFixed(1) + '초'
  if (ms < HOUR_MS) return Math.round(ms / 60_000) + '분'
  return (ms / HOUR_MS).toFixed(1) + '시간'
}
export function fmtCost(usd: number | null | undefined): string {
  return usd == null ? '미확인' : '$' + usd.toFixed(usd < 10 ? 2 : 0)
}
/** Ratio in [0, 1] → "42.0%". */
export function fmtPct(ratio: number | null | undefined): string {
  return ratio == null ? '—' : (ratio * 100).toFixed(1) + '%'
}
export function ratio(part: number, whole: number): number | null {
  return whole > 0 ? part / whole : null
}
export function fmtDateTime(at: string): string {
  const ms = Date.parse(at)
  if (!Number.isFinite(ms) || ms <= 0) return '시각 미상'
  return new Date(ms).toLocaleString('ko-KR', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  })
}
export function fmtDay(at: string): string {
  const ms = Date.parse(at)
  if (!Number.isFinite(ms) || ms <= 0) return ''
  return new Date(ms).toLocaleDateString('ko-KR', {
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
        setError(reason instanceof Error ? reason.message : '요청을 처리하지 못했습니다.')
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

const coverageReasons: Record<string, string> = {
  unreadable_files: '읽지 못한 파일이 있습니다',
  skipped_lines: '해석하지 못해 건너뛴 줄이 있습니다',
  outside_window: '수집 기간보다 앞선 구간이 포함됐습니다',
}

/** Explains why the numbers on a page may be incomplete. Renders nothing when complete. */
export function CoverageNote({ coverage }: { coverage: Coverage | undefined }) {
  if (!coverage || coverage.state === 'complete') return null
  if (coverage.state === 'disabled')
    return <div className="u-note">수집이 꺼져 있습니다. 수집 설정에서 켤 수 있습니다.</div>
  if (coverage.state === 'scanning')
    return (
      <div className="u-note" role="status">
        세션 기록을 읽는 중입니다. 수치가 계속 늘어날 수 있습니다.
      </div>
    )
  const reasons = coverage.reasons.map((r) => coverageReasons[r] ?? r)
  return (
    <div className="u-note u-note-warn" role="status">
      <b>부분 집계</b> {reasons.join(', ') || '일부 기록을 읽지 못했습니다'}.
      {coverage.filesUnreadable > 0 && ` 파일 ${fmtInt(coverage.filesUnreadable)}개`}
      {coverage.linesSkipped > 0 && ` 줄 ${fmtInt(coverage.linesSkipped)}개`}
    </div>
  )
}

/**
 * Empty-state text that separates "nothing happened" from "could not read"
 * and "still reading".
 */
export function Empty(p: { coverage?: Coverage; children?: ReactNode }) {
  const state = p.coverage?.state ?? 'complete'
  const text =
    state === 'scanning'
      ? '아직 읽는 중입니다. 잠시 뒤 채워집니다.'
      : state === 'disabled'
        ? '수집이 꺼져 있어 보여 줄 기록이 없습니다.'
        : state === 'partial'
          ? '이 범위에서 읽은 기록이 없습니다. 읽지 못한 부분이 있어 실제로는 있을 수 있습니다.'
          : '이 범위에는 기록이 없습니다.'
  return <p className="u-empty">{p.children ?? text}</p>
}

export function ErrorNote(p: { error: string; retry?: () => void }) {
  if (!p.error) return null
  return (
    <div className="u-note u-note-bad" role="alert">
      {p.error}
      {p.retry && <button onClick={p.retry}>다시 읽기</button>}
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
