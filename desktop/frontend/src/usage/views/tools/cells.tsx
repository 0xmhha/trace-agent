import type { TokenTotals } from '../../types'
import { Pill, fmtCompact, fmtInt, fmtMs, fmtPct, ratio, sumTokens, type Tone } from '../../ui'
import { failureTone } from './model'
import { t } from '../../../i18n'

const BAR_WIDTH = 64
const BAR_HEIGHT = 6
const BAR_END_RADIUS = BAR_HEIGHT / 2
/** A count above zero stays visible however small its share of the largest row is. */
const BAR_MIN_WIDTH = 1.5

/** Bar path that is square at the baseline on the left and rounded at the data end. */
function barPath(width: number): string {
  if (width <= BAR_END_RADIUS) return `M0 0H${width}V${BAR_HEIGHT}H0Z`
  const flat = width - BAR_END_RADIUS
  return `M0 0H${flat}A${BAR_END_RADIUS} ${BAR_END_RADIUS} 0 0 1 ${flat} ${BAR_HEIGHT}H0Z`
}

/**
 * Count with a bar scaled to the largest count of the rows on screen. The
 * number beside the bar is the value; the bar only makes rows comparable at a
 * glance, so it is hidden from assistive technology.
 */
export function CountBar({ value, max }: { value: number; max: number }) {
  const width = value > 0 && max > 0 ? Math.max(BAR_MIN_WIDTH, (value / max) * BAR_WIDTH) : 0
  return (
    <span className="u-tools-count">
      <span>{fmtInt(value)}</span>
      <svg
        className="u-tools-bar"
        width={BAR_WIDTH}
        height={BAR_HEIGHT}
        viewBox={`0 0 ${BAR_WIDTH} ${BAR_HEIGHT}`}
        // The style sheet shortens the bar in narrow windows; the scale follows its width.
        preserveAspectRatio="none"
        aria-hidden="true"
        focusable="false"
      >
        {width > 0 && <path d={barPath(width)} />}
      </svg>
    </span>
  )
}

/** Largest count of a list, for scaling its bars. */
export function maxOf<T>(rows: readonly T[], value: (row: T) => number): number {
  return rows.reduce((max, row) => Math.max(max, value(row)), 0)
}

/** Text with a tone and, when the tone is the error tone, a word that says so without colour. */
function Toned({ tone, text, flag }: { tone: Tone; text: string; flag: string }) {
  return (
    <span className="u-tools-toned">
      {tone === 'bad' && <Pill tone="bad">{flag}</Pill>}
      <span className={'u-tone-' + tone}>{text}</span>
    </span>
  )
}

export function failureRate(failures: number, total: number): number | null {
  return ratio(failures, total)
}

/** Failure rate of a row, toned by the shared thresholds. */
export function RateCell({ failures, total }: { failures: number; total: number }) {
  const rate = failureRate(failures, total)
  return <Toned tone={failureTone(rate)} text={fmtPct(rate)} flag={t('tools.flag.high')} />
}

/** Duration toned as slow. */
export function SlowCell({ ms, tone }: { ms: number | null; tone: Tone }) {
  return <Toned tone={tone} text={fmtMs(ms)} flag={t('tools.flag.slow')} />
}

/** Name of a row. An empty name is a group the collector could not name. */
export function NameCell({ name, sub }: { name: string; sub?: string }) {
  return (
    <span className="u-tools-name">
      {name ? <b>{name}</b> : <span className="u-tone-muted">{t('tools.unnamed')}</span>}
      {sub && <small>{sub}</small>}
    </span>
  )
}

/** Token total of a group. The tooltip splits it by token kind. */
export function TokenCell({ tokens }: { tokens: TokenTotals | undefined }) {
  if (!tokens) return <span className="u-tone-muted">—</span>
  const detail = t('tools.tokenDetail', {
    input: fmtInt(tokens.input),
    output: fmtInt(tokens.output),
    cacheRead: fmtInt(tokens.cacheRead),
    cacheCreate: fmtInt(tokens.cacheCreate),
  })
  return <span title={detail}>{fmtCompact(sumTokens(tokens))}</span>
}
