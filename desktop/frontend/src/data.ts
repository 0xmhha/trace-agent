import { formatDate, hasMessage, t, type MessageKey } from './i18n'
/** Values received from the loopback GraphQL service are untrusted at runtime. */
export type Row = Record<string, unknown>
export function obj(value: unknown): Row {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? (value as Row)
    : {}
}
export function rows(value: unknown): Row[] {
  return Array.isArray(value) ? value.map(obj) : []
}
export function str(value: unknown): string {
  return typeof value === 'string' ? value : ''
}
export function num(value: unknown): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : 0
}
export function list(value: unknown): string[] {
  return Array.isArray(value)
    ? value.filter((s): s is string => typeof s === 'string')
    : []
}
export type Point = { revision: string; epoch: string }
export type Range = { from: string; until: string }
export type Stamp = {
  point: Point
  cohort: string
  resume: string
  effectiveInterval: Range
}
export type Filter = {
  interval: Range
  clients?: string[]
  sources?: string[]
  tasks?: string[]
}
export type Fixed = { filter: Filter; point: Point; cohort: string }
export function point(input: unknown): Point {
  const v = obj(input)
  if (!/^\d+$/.test(str(v.revision)) || !/^\d+$/.test(str(v.epoch)))
    throw Error(t('shell.badPoint'))
  return { revision: str(v.revision), epoch: str(v.epoch) }
}
export function stamp(input: unknown): Stamp {
  const v = obj(input),
    interval = obj(v.effectiveInterval)
  if (
    !v.cohort ||
    !v.resume ||
    !Number.isFinite(Date.parse(str(interval.from))) ||
    !Number.isFinite(Date.parse(str(interval.until)))
  )
    throw Error(t('shell.badRange'))
  return {
    point: point(v.point),
    cohort: str(v.cohort),
    resume: str(v.resume),
    effectiveInterval: { from: str(interval.from), until: str(interval.until) },
  }
}
export function withoutBodies(input: unknown): unknown {
  if (Array.isArray(input)) return input.map(withoutBodies)
  if (typeof input !== 'object' || !input) return input
  const record = obj(input)
  return Object.fromEntries(
    Object.entries(record).map(([k, v]) => [
      k,
      k === 'text' ? null : withoutBodies(v),
    ]),
  )
}
export function short(id: unknown) {
  const v = str(id)
  return v.length > 18 ? v.slice(0, 8) + '…' + v.slice(-4) : v || t('shell.unknownId')
}
// Format of the trace timestamps: the year is left out because the trace
// workspace looks at recent activity only.
const DATE_FORMAT: Intl.DateTimeFormatOptions = {
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
}
export function date(value: unknown) {
  return str(value) && Number.isFinite(Date.parse(str(value)))
    ? formatDate(str(value), DATE_FORMAT)
    : t('shell.unknownTime')
}
export function label(v: unknown) {
  return str(obj(v).text) || code(obj(v).state) || t('shell.notProvided')
}
/** Label of an enum code from the labels namespace; unknown codes stay as they are. */
export function code(value: unknown) {
  const s = str(value)
  const key = `labels.${s}`
  return s && hasMessage(key) ? t(key as MessageKey) : s
}
