// Number, unit and time helpers shared by the dashboard cards.
import type { Bin } from '../../types'
import { fmtCompact, fmtInt } from '../../ui'

export const HOUR_MS = 3_600_000
export const DAY_MS = 24 * HOUR_MS
/** A local day is 23 to 25 hours long around a DST change, so "daily" starts below 24. */
const DAY_BIN_MIN_MS = 20 * HOUR_MS

/** Korean counters used next to numbers on the dashboard. */
export const UNIT = {
  items: '개',
  times: '회',
  days: '일',
  tokens: '토큰',
} as const

export function withUnit(n: number, unit: string): string {
  return fmtInt(n) + unit
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

/** "일별", "시간별" or "6시간 단위", for chart subtitles. */
export function binWord(widthMs: number): string {
  if (isDailyBin(widthMs)) {
    const days = Math.round(widthMs / DAY_MS)
    return days <= 1 ? '일별' : days + '일 단위'
  }
  const hours = Math.round(widthMs / HOUR_MS)
  return hours <= 1 ? '시간별' : hours + '시간 단위'
}

/** Short axis label of a day: "11/27". */
export function dayTick(ms: number): string {
  const d = new Date(ms)
  return d.getMonth() + 1 + '/' + d.getDate()
}

/** Short axis label of an hour: "14시". */
export function hourTick(ms: number): string {
  return new Date(ms).getHours() + '시'
}

const WEEKDAYS = ['일', '월', '화', '수', '목', '금', '토'] as const

/** Weekday name for the store's index, where 0 is Sunday. */
export function weekdayName(weekday: number): string {
  return (WEEKDAYS[weekday] ?? '') + '요일'
}
export function weekdayShort(weekday: number): string {
  return WEEKDAYS[weekday] ?? ''
}

/** Full name of a bucket for tooltips and the table view. */
export function binName(fromMs: number, widthMs: number): string {
  if (!Number.isFinite(fromMs)) return '시각 미상'
  const d = new Date(fromMs)
  const day = `${d.getMonth() + 1}월 ${d.getDate()}일 (${weekdayShort(d.getDay())})`
  if (isDailyBin(widthMs)) {
    const days = Math.round(widthMs / DAY_MS)
    return days <= 1 ? day : `${day}부터 ${days}일`
  }
  const hours = Math.round(widthMs / HOUR_MS)
  return hours <= 1 ? `${day} ${d.getHours()}시` : `${day} ${d.getHours()}시부터 ${hours}시간`
}
