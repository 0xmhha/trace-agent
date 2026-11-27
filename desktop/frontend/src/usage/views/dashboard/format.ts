// Number, unit and time helpers shared by the dashboard cards.
import { formatDate, t, type MessageKey } from '../../../i18n'
import type { Bin } from '../../types'
import { fmtCompact, fmtInt } from '../../ui'

export const HOUR_MS = 3_600_000
export const DAY_MS = 24 * HOUR_MS
/** A local day is 23 to 25 hours long around a DST change, so "daily" starts below 24. */
const DAY_BIN_MIN_MS = 20 * HOUR_MS

/** Counters printed next to numbers on the dashboard. */
export const UNIT = {
  items: 'items',
  times: 'times',
  days: 'days',
  tokens: 'tokens',
} as const
export type Unit = (typeof UNIT)[keyof typeof UNIT]

/** Message of a number with its counter, e.g. "12 days". */
const UNIT_TEXT: Record<Unit, MessageKey> = {
  items: 'dashboard.unitItems',
  times: 'dashboard.unitTimes',
  days: 'dashboard.unitDays',
  tokens: 'dashboard.unitTokens',
}
/** Name of a counter alone, for table column heads. */
const UNIT_NAME: Record<Unit, MessageKey> = {
  items: 'dashboard.unitNameItems',
  times: 'dashboard.unitNameTimes',
  days: 'dashboard.unitNameDays',
  tokens: 'dashboard.unitNameTokens',
}

/** n with its counter. value is the printed figure; it defaults to the whole number. */
export function withUnit(n: number, unit: Unit, value: string = fmtInt(n)): string {
  return t(UNIT_TEXT[unit], { count: n, value })
}
/** n in short form (1.2K) with its counter. */
export function compactWithUnit(n: number, unit: Unit): string {
  return withUnit(n, unit, fmtCompact(n))
}
export function unitName(unit: Unit): string {
  return t(UNIT_NAME[unit])
}

/** Axis tick text: compact, without a trailing ".0" so round ticks stay short. */
export function fmtTick(n: number): string {
  return fmtCompact(n).replace(/\.0(?=[KMB]?$)/, '')
}

const NICE_STEPS = [1, 2, 2.5, 5, 10]
const NICE_INTEGER_STEPS = [1, 2, 5, 10]

/**
 * Round tick values from 0 to at least max. The last tick is the top of the
 * scale, so every bar ends at or below a labelled line.
 */
export function niceTicks(max: number, target: number, integer: boolean): number[] {
  if (!(max > 0)) return [0]
  const raw = max / target
  const magnitude = 10 ** Math.floor(Math.log10(raw))
  const steps = integer ? NICE_INTEGER_STEPS : NICE_STEPS
  const base = integer ? Math.max(1, magnitude) : magnitude
  const step = (steps.find((s) => s * base >= raw) ?? 10) * base
  const ticks: number[] = []
  for (let i = 0; i * step < max + step; i++) {
    // Multiplying a decimal step accumulates float noise; trim it.
    ticks.push(Math.round(i * step * 1e6) / 1e6)
    if (i * step >= max) break
  }
  return ticks
}

/** Width of one series bucket, read from the data because the store may widen it. */
export function binWidthMs(series: readonly Bin[]): number {
  const first = Date.parse(series[0]?.from ?? '')
  const second = Date.parse(series[1]?.from ?? '')
  const width = second - first
  return Number.isFinite(width) && width > 0 ? width : DAY_MS
}

export function isDailyBin(widthMs: number): boolean {
  return widthMs >= DAY_BIN_MIN_MS
}

/** "Daily", "Hourly" or "Every 6 hours", for chart subtitles. */
export function binWord(widthMs: number): string {
  if (isDailyBin(widthMs)) {
    const days = Math.round(widthMs / DAY_MS)
    return days <= 1 ? t('dashboard.binDaily') : t('dashboard.binDays', { count: days })
  }
  const hours = Math.round(widthMs / HOUR_MS)
  return hours <= 1 ? t('dashboard.binHourly') : t('dashboard.binHours', { count: hours })
}

/** Short axis label of a day: "11/27". */
export function dayTick(ms: number): string {
  const d = new Date(ms)
  return t('dashboard.dayTick', { month: d.getMonth() + 1, day: d.getDate() })
}

/** Short label of an hour of the day, e.g. "14:00". */
export function hourLabel(hour: number): string {
  return t('dashboard.hourTick', { hour })
}
/** Short axis label of the hour an instant falls in. */
export function hourTick(ms: number): string {
  return hourLabel(new Date(ms).getHours())
}

/** A local date that fell on a Sunday; weekday i is this date plus i days. */
const SUNDAY = { year: 2026, month: 0, day: 4 } as const

function weekdayDate(weekday: number): Date {
  return new Date(SUNDAY.year, SUNDAY.month, SUNDAY.day + weekday)
}

/** Weekday name for the store's index, where 0 is Sunday. */
export function weekdayName(weekday: number): string {
  return formatDate(weekdayDate(weekday), { weekday: 'long' })
}
export function weekdayShort(weekday: number): string {
  return formatDate(weekdayDate(weekday), { weekday: 'short' })
}

/** Full name of a bucket for tooltips and the table view. */
export function binName(fromMs: number, widthMs: number): string {
  if (!Number.isFinite(fromMs)) return t('usage.timeUnknown')
  const d = new Date(fromMs)
  const day = formatDate(d, { month: 'long', day: 'numeric', weekday: 'short' })
  const hour = d.getHours()
  if (isDailyBin(widthMs)) {
    const days = Math.round(widthMs / DAY_MS)
    return days <= 1 ? day : t('dashboard.binNameDays', { day, count: days })
  }
  const hours = Math.round(widthMs / HOUR_MS)
  return hours <= 1
    ? t('dashboard.binNameHour', { day, hour })
    : t('dashboard.binNameHours', { day, hour, count: hours })
}
