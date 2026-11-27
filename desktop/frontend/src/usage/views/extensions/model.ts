import type { ExtLevel, ExtStatus } from '../../types'
import type { Tone } from '../../ui'

/** Environment variables trace-emit and the examples read. */
export const URL_ENV = 'TRACE_EVENTS_URL'
export const TOKEN_ENV = 'TRACE_EVENTS_TOKEN'

/** Rows asked for per page; equals the service's default page size. */
export const EVENT_PAGE_SIZE = 50
/** Largest page the service returns; a refresh reloads at most this many rows. */
export const EVENT_PAGE_MAX = 200

/** Levels in rising order, as senders write them on the wire. */
export const levels: readonly ExtLevel[] = ['debug', 'info', 'warn', 'error']

const statusPills: Record<ExtStatus, { label: string; tone: Tone }> = {
  ok: { label: '성공', tone: 'good' },
  error: { label: '오류', tone: 'bad' },
  unknown: { label: '미확인', tone: 'muted' },
}
/** Label and tone of a status; a value this build does not know reads as unknown. */
export function statusPill(status: ExtStatus): { label: string; tone: Tone } {
  return statusPills[status] ?? statusPills.unknown
}

/** Filters of the event list. An empty string means "all". */
export interface EventFilter {
  source: string
  level: ExtLevel | ''
}
export const noFilter: EventFilter = { source: '', level: '' }

// Both filters live in the route's tab value as "<level>@<source>" so that a
// reload or the back button restores them. A source never contains "@".
const FILTER_SEPARATOR = '@'
const sourcePattern = /^[A-Za-z0-9._-]{1,64}$/

export function parseFilter(tab: string | undefined): EventFilter {
  if (!tab) return noFilter
  const cut = tab.indexOf(FILTER_SEPARATOR)
  if (cut < 0) return noFilter
  const level = tab.slice(0, cut)
  const source = tab.slice(cut + 1)
  return {
    source: sourcePattern.test(source) ? source : '',
    level: (levels as readonly string[]).includes(level) ? (level as ExtLevel) : '',
  }
}

/** Route tab value of a filter; undefined keeps the address clean when nothing is set. */
export function filterTab(filter: EventFilter): string | undefined {
  if (!filter.source && !filter.level) return undefined
  return filter.level + FILTER_SEPARATOR + filter.source
}

// The session route accepts a narrower ID than the event wire format does
// (no ":"). An ID outside it would land on the session list, so such an event
// is shown as linked but without a link.
const routableSessionId = /^[A-Za-z0-9._-]{1,128}$/
export function canOpenSession(sessionId: string | undefined): sessionId is string {
  return sessionId !== undefined && routableSessionId.test(sessionId)
}

/**
 * Address of the endpoint reduced to origin and path. User info, query and
 * fragment are dropped so that nothing credential-like can reach the screen
 * even if the address ever carried one. An unparsable address reads as empty.
 */
export function displayUrl(raw: string): string {
  try {
    const url = new URL(raw)
    return url.origin + url.pathname
  } catch {
    return ''
  }
}

/** Message of a rejected request, with a fallback for non-Error rejections. */
export function errorText(reason: unknown, fallback: string): string {
  return reason instanceof Error && reason.message ? reason.message : fallback
}

/** Time with seconds and milliseconds, for telling apart events of one minute. */
export function fmtPrecise(at: string): string {
  const ms = Date.parse(at)
  if (!Number.isFinite(ms) || ms <= 0) return '시각 미상'
  return new Date(ms).toLocaleString('ko-KR', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    fractionalSecondDigits: 3,
    hour12: false,
  })
}
