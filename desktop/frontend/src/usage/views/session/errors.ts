// Error text of the session page. The desktop bridge may reject with a plain
// string instead of an Error, so both shapes are read here.

const GENERIC = '요청을 처리하지 못했습니다.'
/** Wording of usage.ErrNotFound on the Go side. */
const NOT_FOUND = /not found/i
/** Wording of usage.ErrNoConsent on the Go side. */
const NO_CONSENT = /collection not allowed/i

export function errorText(reason: unknown): string {
  if (reason instanceof Error) return reason.message || GENERIC
  if (typeof reason === 'string' && reason) return reason
  return GENERIC
}

/** Turns any rejection into an Error so that its message survives a generic catch. */
export function rethrowAsError(reason: unknown): never {
  throw reason instanceof Error ? reason : new Error(errorText(reason))
}

export function isNotFound(message: string): boolean {
  return NOT_FOUND.test(message)
}

/** Message for a failed read of one transcript stream or event list. */
export function describeError(message: string): string {
  if (!message) return ''
  if (isNotFound(message)) return '요청한 기록을 찾지 못했습니다. 지워졌거나 아직 수집되지 않았을 수 있습니다.'
  if (NO_CONSENT.test(message)) return '수집이 꺼져 있어 원본 세션 기록을 읽을 수 없습니다.'
  return message
}
