// Labels and formatters shared by the session detail sub-components. Label
// tables map API values to message keys, so they follow the locale at render.
import { formatDate, formatNumber, t, type MessageKey } from '../../../i18n'
import type {
  AgentRow,
  ExtKind,
  ExtLevel,
  ExtStatus,
  HookStatus,
  TimelineItem,
  TimelineKind,
  TokenTotals,
  ToolCategory,
} from '../../types'
import { fmtCompact, type Tone } from '../../ui'

/** Display order of the timeline kinds in the filter. */
export const kindOrder: readonly TimelineKind[] = [
  'prompt',
  'assistant',
  'thinking',
  'tool_call',
  'tool_result',
  'hook',
  'system',
  'ext',
]

export const kindLabels: Record<TimelineKind, MessageKey> = {
  prompt: 'session.kindPrompt',
  assistant: 'session.kindAssistant',
  thinking: 'session.kindThinking',
  tool_call: 'session.kindToolCall',
  tool_result: 'session.kindToolResult',
  hook: 'session.kindHook',
  system: 'session.kindSystem',
  ext: 'session.kindExt',
}

export const categoryLabels: Record<ToolCategory, MessageKey> = {
  builtin: 'session.categoryBuiltin',
  mcp: 'session.categoryMcp',
  skill: 'session.categorySkill',
  agent: 'session.categoryAgent',
}

export const hookStatusLabels: Record<HookStatus, MessageKey> = {
  success: 'session.statusSuccess',
  error: 'session.statusError',
  blocked: 'session.statusBlocked',
  unknown: 'session.statusUnknown',
}
export const hookStatusTones: Record<HookStatus, Tone> = {
  success: 'good',
  error: 'bad',
  blocked: 'warn',
  unknown: 'muted',
}

export const extStatusLabels: Record<ExtStatus, MessageKey> = {
  ok: 'session.statusSuccess',
  error: 'session.statusError',
  unknown: 'session.statusUnknown',
}
export const extStatusTones: Record<ExtStatus, Tone> = {
  ok: 'good',
  error: 'bad',
  unknown: 'muted',
}

export const extLevelLabels: Record<ExtLevel, MessageKey> = {
  debug: 'session.levelDebug',
  info: 'session.levelInfo',
  warn: 'session.levelWarn',
  error: 'session.levelError',
}

export const extKindLabels: Record<ExtKind, MessageKey> = {
  span: 'session.extSpan',
  log: 'session.extLog',
  metric: 'session.extMetric',
}

/**
 * Looks a label up without trusting that the API only sends the values this
 * build knows; an unknown value is shown as it came.
 */
export function labelOf<K extends string>(labels: Record<K, MessageKey>, key: K): string {
  const message = (labels as Record<string, MessageKey | undefined>)[key]
  return message ? t(message) : key
}

/** Same as labelOf for tones; an unknown value gets the neutral tone. */
export function toneOf<K extends string>(tones: Record<K, Tone>, key: K): Tone {
  return (tones as Record<string, Tone | undefined>)[key] ?? 'muted'
}

/** Placeholder values of a counted message: count picks the plural form, n is what is shown. */
export function countVars(count: number): { count: number; n: string } {
  return { count, n: formatNumber(Math.round(count)) }
}

/** "Input 12 · Output 420 · Cache read 48.0K · Cache write 2.1K" */
export function tokenParts(tokens: TokenTotals): string {
  return t('session.tokensSummary', {
    input: fmtCompact(tokens.input),
    output: fmtCompact(tokens.output),
    cacheRead: fmtCompact(tokens.cacheRead),
    cacheCreate: fmtCompact(tokens.cacheCreate),
  })
}

const MS_PER_SECOND = 1000
/** One decimal, as the label the backend wrote before the structured form existed. */
const SECONDS_FORMAT: Intl.NumberFormatOptions = { minimumFractionDigits: 1, maximumFractionDigits: 1 }

/**
 * Text of a system item, worded from its structured form. An item without
 * one (older backend, or a kind this build does not know) shows its label.
 */
export function systemLabel(item: TimelineItem): string | undefined {
  const system = item.system
  switch (system?.kind) {
    case 'turn_end':
      return system.durationMs == null
        ? t('session.systemTurnEnd')
        : t('session.systemTurnEndSeconds', {
            seconds: formatNumber(system.durationMs / MS_PER_SECOND, SECONDS_FORMAT),
          })
    case 'compaction':
      return system.trigger
        ? t('session.systemCompactionTrigger', { trigger: system.trigger })
        : t('session.systemCompaction')
    default:
      return item.label
  }
}

function parse(at: string): number | null {
  const ms = Date.parse(at)
  return Number.isFinite(ms) && ms > 0 ? ms : null
}

/** Wall-clock time with seconds, for rows that sit close together. */
export function fmtClock(at: string): string {
  const ms = parse(at)
  if (ms == null) return t('session.timeUnknown')
  return formatDate(ms, {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  })
}

/** Date and time with seconds, for tooltips and table cells. */
export function fmtFull(at: string): string {
  const ms = parse(at)
  if (ms == null) return t('session.timeUnknown')
  return formatDate(ms, {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  })
}

/** Milliseconds between two instants, or null when either is unknown. */
export function elapsedMs(from: string, until: string): number | null {
  const a = parse(from),
    b = parse(until)
  return a == null || b == null ? null : Math.max(0, b - a)
}

/** Name of the root agent's stream. */
export function rootAgentLabel(): string {
  return t('session.rootAgent')
}

/** Name of a transcript stream; the root agent has an empty ID. */
export function agentLabel(row: Pick<AgentRow, 'agentId' | 'agentType'>): string {
  if (!row.agentId) return rootAgentLabel()
  return t('session.agentName', { type: row.agentType || t('session.subagent'), id: row.agentId })
}

/** A count of repeated events, with the unit the locale puts on it. */
export function fmtTimes(n: number): string {
  return t('session.times', countVars(n))
}

/** A count of things, with the unit the locale puts on it. */
export function fmtItems(n: number): string {
  return t('session.items', countVars(n))
}

/** Zero time of the Go side: an ExtQuery range with both bounds zero selects all time. */
const ZERO_INSTANT = '0001-01-01T00:00:00Z'
export const ALL_TIME = { from: ZERO_INSTANT, until: ZERO_INSTANT } as const
