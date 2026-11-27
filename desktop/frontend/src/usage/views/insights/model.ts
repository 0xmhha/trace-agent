// Labels, ordering, filtering and number formatting of the insights page.
// Everything here is pure so the components stay declarative.
import type {
  Evidence,
  Insight,
  InsightCategory,
  RecommendationKind,
  Severity,
} from '../../types'
import { fmtInt, fmtMs, ratio, type Tone } from '../../ui'

/** Most severe first; the order insights are listed in within a category. */
export const severityOrder: readonly Severity[] = ['critical', 'warn', 'info']
/** Display order of the category sections. */
export const categoryOrder: readonly InsightCategory[] = [
  'cost',
  'reliability',
  'workflow',
  'extension',
]

const severityLabels: Record<Severity, string> = {
  critical: '심각',
  warn: '주의',
  info: '참고',
}
const severityTones: Record<Severity, Tone> = {
  critical: 'bad',
  warn: 'warn',
  info: 'muted',
}
const categoryLabels: Record<InsightCategory, string> = {
  cost: '비용',
  reliability: '안정성',
  workflow: '작업 방식',
  extension: '확장',
}
const categoryHints: Record<InsightCategory, string> = {
  cost: '토큰과 캐시 사용',
  reliability: '도구, MCP 서버, 훅의 실패',
  workflow: '도구 선택과 세션 운영',
  extension: '스킬, 플러그인, 확장 이벤트',
}
const kindLabels: Record<RecommendationKind, string> = {
  skill: '스킬',
  plugin: '플러그인',
  mcp: 'MCP',
  hook: '훅',
  setting: '설정',
  habit: '습관',
}

// The Go side may gain a value before this file learns its label. An unknown
// value is shown as it came instead of being dropped.
function lookup<T>(table: Record<string, T>, key: string): T | undefined {
  return Object.hasOwn(table, key) ? table[key] : undefined
}
export function severityLabel(severity: string): string {
  return lookup(severityLabels, severity) ?? severity
}
export function severityTone(severity: string): Tone {
  return lookup(severityTones, severity) ?? 'muted'
}
/** A known severity, for building a class name from a closed set only. */
export function knownSeverity(severity: string): Severity {
  return lookup(severityLabels, severity) ? (severity as Severity) : 'info'
}
export function categoryLabel(category: string): string {
  return lookup(categoryLabels, category) ?? category
}
export function kindLabel(kind: string): string {
  return lookup(kindLabels, kind) ?? kind
}

function severityRank(severity: string): number {
  const at = (severityOrder as readonly string[]).indexOf(severity)
  return at < 0 ? severityOrder.length : at
}

export type SeverityFilter = Severity | 'all'
export type CategoryFilter = InsightCategory | 'all'
export interface InsightFilter {
  severity: SeverityFilter
  category: CategoryFilter
}

const ALL = 'all'
// route.tab holds both filters as "<severity>.<category>" so that reload and
// back restore them.
const FILTER_SEPARATOR = '.'

export const noFilter: InsightFilter = { severity: ALL, category: ALL }

export function parseFilter(tab: string | undefined): InsightFilter {
  const [severity = '', category = ''] = (tab ?? '').split(FILTER_SEPARATOR)
  return {
    severity: (severityOrder as readonly string[]).includes(severity)
      ? (severity as Severity)
      : ALL,
    category: (categoryOrder as readonly string[]).includes(category)
      ? (category as InsightCategory)
      : ALL,
  }
}

/** The route.tab value of a filter; undefined when nothing is filtered. */
export function filterTab(filter: InsightFilter): string | undefined {
  if (filter.severity === ALL && filter.category === ALL) return undefined
  return filter.severity + FILTER_SEPARATOR + filter.category
}

export function isFiltered(filter: InsightFilter): boolean {
  return filterTab(filter) !== undefined
}

export function applyFilter(items: Insight[], filter: InsightFilter): Insight[] {
  return items.filter(
    (item) =>
      (filter.severity === ALL || item.severity === filter.severity) &&
      (filter.category === ALL || item.category === filter.category),
  )
}

export interface InsightGroup {
  category: string
  label: string
  hint: string
  items: Insight[]
}

/**
 * Groups by category in display order and sorts each group most severe
 * first. Insights of equal severity keep the order the report gave them.
 */
export function groupInsights(items: Insight[]): InsightGroup[] {
  const byCategory = new Map<string, Insight[]>()
  for (const category of categoryOrder) byCategory.set(category, [])
  for (const item of items) {
    const list = byCategory.get(item.category)
    if (list) list.push(item)
    else byCategory.set(item.category, [item])
  }
  const groups: InsightGroup[] = []
  for (const [category, list] of byCategory) {
    if (!list.length) continue
    groups.push({
      category,
      label: categoryLabel(category),
      hint: lookup(categoryHints, category) ?? '',
      // Array.prototype.sort is stable, so ties keep the report order.
      items: [...list].sort((a, b) => severityRank(a.severity) - severityRank(b.severity)),
    })
  }
  return groups
}

const UNIT_MS = 'ms'
const MAX_FRACTION_DIGITS = 1

function plainNumber(value: number): string {
  if (!Number.isFinite(value)) return '—'
  return Number.isInteger(value)
    ? fmtInt(value)
    : value.toLocaleString('ko-KR', { maximumFractionDigits: MAX_FRACTION_DIGITS })
}

// Korean counters and "%" attach to the number; a Latin word needs a space.
function withUnit(text: string, unit: string): string {
  if (!unit) return text
  return /^[A-Za-z]/.test(unit) ? text + ' ' + unit : text + unit
}

function amount(value: number, unit: string): string {
  if (unit === UNIT_MS) return Number.isFinite(value) ? fmtMs(value) : '—'
  return withUnit(plainNumber(value), unit)
}

export interface EvidenceFigure {
  /** "126 / 900회" or "1.2초". */
  text: string
  /** Value as a share of its denominator in [0, 1]; null without one. */
  share: number | null
}

export function evidenceFigure(evidence: Evidence): EvidenceFigure {
  const unit = evidence.unit ?? ''
  if (evidence.of == null) return { text: amount(evidence.value, unit), share: null }
  const text =
    unit === UNIT_MS
      ? amount(evidence.value, unit) + ' / ' + amount(evidence.of, unit)
      : withUnit(plainNumber(evidence.value) + ' / ' + plainNumber(evidence.of), unit)
  const share = ratio(evidence.value, evidence.of)
  return {
    text,
    share: share == null || !Number.isFinite(share) ? null : Math.min(1, Math.max(0, share)),
  }
}

const SHORT_ID_LENGTH = 8
// An ID only slightly longer than the short form is clearer shown whole.
const SHORT_ID_SLACK = 4

/** Session IDs are long random strings; the head is enough to tell them apart. */
export function shortId(id: string): string {
  return id.length > SHORT_ID_LENGTH + SHORT_ID_SLACK ? id.slice(0, SHORT_ID_LENGTH) + '…' : id
}
