import { useId } from 'react'
import { t, type MessageKey } from '../../../i18n'
import type { TokenTotals } from '../../types'
import { fmtInt, fmtPct, ratio, sumTokens } from '../../ui'

/**
 * The four token kinds in the order they are drawn and listed. The class sets
 * the series colour (--u-s1..--u-s4, in that order) for the bar and the legend.
 */
const tokenParts: readonly { key: keyof TokenTotals; label: MessageKey; className: string }[] = [
  { key: 'input', label: 'sessions.partInput', className: 'u-sess-part-input' },
  { key: 'output', label: 'sessions.partOutput', className: 'u-sess-part-output' },
  { key: 'cacheRead', label: 'sessions.partCacheRead', className: 'u-sess-part-cache-read' },
  { key: 'cacheCreate', label: 'sessions.partCacheCreate', className: 'u-sess-part-cache-create' },
]

const BAR_WIDTH = 72
const BAR_HEIGHT = 6
/** Empty space between two parts, so neighbours are told apart by more than colour. */
const PART_GAP = 2
/** Narrowest drawn part. A kind that was used stays visible next to a dominant one. */
const MIN_PART_WIDTH = 2

interface Segment {
  className: string
  x: number
  width: number
}

function layout(tokens: TokenTotals): Segment[] {
  const parts = tokenParts
    .map((part) => ({ className: part.className, value: Math.max(0, tokens[part.key]) }))
    .filter((part) => part.value > 0)
  if (!parts.length) return []
  const total = parts.reduce((sum, part) => sum + part.value, 0)
  const usable = BAR_WIDTH - PART_GAP * (parts.length - 1)
  const widths = parts.map((part) => Math.max(MIN_PART_WIDTH, (part.value / total) * usable))
  // Raising small parts to the minimum overshoots the bar; the widest part
  // gives that back. It is always wide enough: it holds at least a quarter.
  const widest = widths.indexOf(Math.max(...widths))
  widths[widest] = (widths[widest] ?? 0) - (widths.reduce((sum, w) => sum + w, 0) - usable)
  let x = 0
  return parts.map((part, i) => {
    const width = widths[i] ?? 0
    const segment = { className: part.className, x, width }
    x += width + PART_GAP
    return segment
  })
}

/** Joins the parts of a split summary. */
const PART_SEPARATOR = ', '

/** "Input 4,012 (1.3%), Output …": the exact figures behind a bar, for tooltips and screen readers. */
export function splitSummary(tokens: TokenTotals): string {
  const total = sumTokens(tokens)
  return tokenParts
    .map((part) => {
      const share = ratio(tokens[part.key], total)
      const vars = { label: t(part.label), n: fmtInt(tokens[part.key]) }
      return share == null
        ? t('sessions.splitPart', vars)
        : t('sessions.splitPartShare', { ...vars, pct: fmtPct(share) })
    })
    .join(PART_SEPARATOR)
}

/**
 * Thin bar showing how one session's tokens split into the four kinds. It
 * carries no text of its own: the total stands next to it, the legend names
 * the colours, and the exact split is in the tooltip and the accessible name.
 */
export function SplitBar({ tokens }: { tokens: TokenTotals }) {
  const clipId = useId()
  const segments = layout(tokens)
  const summary = splitSummary(tokens)
  return (
    <svg
      className="u-sess-tokenbar"
      width={BAR_WIDTH}
      height={BAR_HEIGHT}
      viewBox={`0 0 ${BAR_WIDTH} ${BAR_HEIGHT}`}
      role="img"
      aria-label={t('sessions.barLabel', { summary })}
    >
      <title>{summary}</title>
      <clipPath id={clipId}>
        <rect width={BAR_WIDTH} height={BAR_HEIGHT} rx={BAR_HEIGHT / 2} />
      </clipPath>
      {segments.length === 0 ? (
        <rect
          className="u-sess-part-none"
          width={BAR_WIDTH}
          height={BAR_HEIGHT}
          rx={BAR_HEIGHT / 2}
        />
      ) : (
        <g clipPath={`url(#${clipId})`}>
          {segments.map((s) => (
            <rect key={s.className} className={s.className} x={s.x} width={s.width} height={BAR_HEIGHT} />
          ))}
        </g>
      )}
    </svg>
  )
}

/** One legend for every bar in the table. */
export function SplitLegend() {
  return (
    <ul className="u-sess-legend" aria-label={t('sessions.legendLabel')}>
      {tokenParts.map((part) => (
        <li key={part.key}>
          <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true">
            <rect className={part.className} width="10" height="10" rx="2" />
          </svg>
          {t(part.label)}
        </li>
      ))}
    </ul>
  )
}
