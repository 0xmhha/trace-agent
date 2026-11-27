// Labels and formatters shared by the session detail sub-components.
import type {
  AgentRow,
  ExtKind,
  ExtLevel,
  ExtStatus,
  HookStatus,
  TimelineKind,
  TokenTotals,
  ToolCategory,
} from '../../types'
import { fmtCompact, fmtInt, type Tone } from '../../ui'

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

export const kindLabels: Record<TimelineKind, string> = {
  prompt: '프롬프트',
  assistant: '어시스턴트',
  thinking: '생각',
  tool_call: '도구 호출',
  tool_result: '도구 결과',
  hook: '훅',
  system: '시스템',
  ext: '확장 이벤트',
}

export const categoryLabels: Record<ToolCategory, string> = {
  builtin: '기본 도구',
  mcp: 'MCP',
  skill: '스킬',
  agent: '에이전트',
}

export const hookStatusLabels: Record<HookStatus, string> = {
  success: '성공',
  error: '오류',
  blocked: '차단',
  unknown: '상태 불명',
}
export const hookStatusTones: Record<HookStatus, Tone> = {
  success: 'good',
  error: 'bad',
  blocked: 'warn',
  unknown: 'muted',
}

export const extStatusLabels: Record<ExtStatus, string> = {
  ok: '성공',
  error: '오류',
  unknown: '상태 불명',
}
export const extStatusTones: Record<ExtStatus, Tone> = {
  ok: 'good',
  error: 'bad',
  unknown: 'muted',
}

export const extLevelLabels: Record<ExtLevel, string> = {
  debug: '디버그',
  info: '정보',
  warn: '경고',
  error: '오류',
}

export const extKindLabels: Record<ExtKind, string> = {
  span: '구간',
  log: '로그',
  metric: '측정값',
}

/**
 * Looks a label up without trusting that the API only sends the values this
 * build knows; an unknown value is shown as it came.
 */
export function labelOf<K extends string>(labels: Record<K, string>, key: K): string {
  return (labels as Record<string, string | undefined>)[key] ?? key
}

/** Same as labelOf for tones; an unknown value gets the neutral tone. */
export function toneOf<K extends string>(tones: Record<K, Tone>, key: K): Tone {
  return (tones as Record<string, Tone | undefined>)[key] ?? 'muted'
}

/** "입력 12 · 출력 420 · 캐시 읽기 48.0K · 캐시 쓰기 2.1K" */
export function tokenParts(t: TokenTotals): string {
  return [
    '입력 ' + fmtCompact(t.input),
    '출력 ' + fmtCompact(t.output),
    '캐시 읽기 ' + fmtCompact(t.cacheRead),
    '캐시 쓰기 ' + fmtCompact(t.cacheCreate),
  ].join(' · ')
}

function parse(at: string): number | null {
  const ms = Date.parse(at)
  return Number.isFinite(ms) && ms > 0 ? ms : null
}

/** Wall-clock time with seconds, for rows that sit close together. */
export function fmtClock(at: string): string {
  const ms = parse(at)
  if (ms == null) return '시각 미상'
  return new Date(ms).toLocaleTimeString('ko-KR', {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  })
}

/** Date and time with seconds, for tooltips and table cells. */
export function fmtFull(at: string): string {
  const ms = parse(at)
  if (ms == null) return '시각 미상'
  return new Date(ms).toLocaleString('ko-KR', {
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

export const ROOT_AGENT_LABEL = '기본 에이전트'

/** Name of a transcript stream; the root agent has an empty ID. */
export function agentLabel(row: Pick<AgentRow, 'agentId' | 'agentType'>): string {
  if (!row.agentId) return ROOT_AGENT_LABEL
  return (row.agentType || '하위 에이전트') + ' · ' + row.agentId
}

export function fmtCount(n: number, unit: string): string {
  return fmtInt(n) + unit
}

/** Zero time of the Go side: an ExtQuery range with both bounds zero selects all time. */
const ZERO_INSTANT = '0001-01-01T00:00:00Z'
export const ALL_TIME = { from: ZERO_INSTANT, until: ZERO_INSTANT } as const
