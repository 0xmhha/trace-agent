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
    throw Error('잘못된 조회 시점입니다.')
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
    throw Error('조회 범위를 확인할 수 없습니다.')
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
  return v.length > 18 ? v.slice(0, 8) + '…' + v.slice(-4) : v || '미확인'
}
export function date(value: unknown) {
  return str(value) && Number.isFinite(Date.parse(str(value)))
    ? new Date(str(value)).toLocaleString('ko-KR', {
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
      })
    : '시각 미상'
}
export function label(v: unknown) {
  return str(obj(v).text) || code(obj(v).state) || '미제공'
}
const labels: Record<string, string> = {
  RUNNING: '실행 중',
  ENDED: '종료',
  UNKNOWN: '불명',
  START_OBSERVED: '시작 확인',
  PROPOSED: '제안',
  SUCCESS: '성공',
  TOOL_ERROR: '도구 오류',
  RPC_ERROR: '통신 오류',
  MODEL_ERROR: '모델 오류',
  CANCELLED: '취소',
  ACTIVE: '진행 중',
  DONE: '완료',
  PENDING: '예정',
  FAILED: '실패',
  DIRECT: '직접 관찰',
  INFERRED: '추정',
  LOST: '연결 끊김',
  CONNECTED: '연결됨',
  DELAYED: '지연',
  UNSUPPORTED: '지원하지 않음',
  AVAILABLE: '사용 가능',
  UNAVAILABLE: '사용 불가',
  UNVERIFIED: '미확인',
  PARTIAL: '일부 확인',
  DISABLED: '본문 꺼짐',
  REVOKED: '동의 철회',
  DELETED: '삭제됨',
  MISSING: '미제공',
  TRUNCATED: '일부만 표시',
  CODEX: 'Codex',
  CLAUDE: 'Claude Code',
  TOOL: '도구',
  AGENT: '에이전트',
  MODEL: '모델',
  MCP_SERVER: 'MCP 서버',
  MCP_TOOL: 'MCP 도구',
  SPAWN: '자식 생성',
  ASSIGN: '작업 배정',
  SEND: '전달',
  CALL: '호출',
  RETURN: '반환',
  WAIT: '대기',
  TIMEOUT: '시간 초과',
  RESUME: '재개',
  JOIN: '합류',
  STATE: '상태 변경',
  GAP: '수집 공백',
  MATCHED: '연결됨',
  UNLINKED: '미연계',
  CONFLICT: '충돌',
  WAITING_TOOL: '도구 응답 대기',
  WAITING_CHILDREN: '자식 응답 대기',
  WAITING_MODEL: '모델 응답 대기',
  BOTH: '양쪽 관찰',
  RECEIVER: '수신 측 관찰',
  SENDER: '송신 측 관찰',
  LOCAL: '로컬 관찰',
}
export function code(value: unknown) {
  const s = str(value)
  return labels[s] || s
}
