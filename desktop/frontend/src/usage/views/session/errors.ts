// Error text of the session page. The desktop bridge may reject with a plain
// string instead of an Error, so both shapes are read here.
import { errorText, t } from '../../../i18n'

/** Codes the desktop bridge rejects with (see docs/design/i18n.md). */
const CODE_NOT_FOUND = 'usage.not_found'
const CODE_NO_CONSENT = 'usage.no_consent'
/** Wording of usage.ErrNotFound when the page talks to the service directly. */
const NOT_FOUND = /not found/i
/** Wording of usage.ErrNoConsent when the page talks to the service directly. */
const NO_CONSENT = /collection not allowed/i

/**
 * The message of a rejection, left untranslated so that codes can still be
 * recognised; empty when the rejection carries none.
 */
export function rejectionMessage(reason: unknown): string {
  if (reason instanceof Error) return reason.message
  if (typeof reason === 'string') return reason
  return ''
}

/** Turns any rejection into an Error so that its message survives a generic catch. */
export function rethrowAsError(reason: unknown): never {
  throw reason instanceof Error ? reason : new Error(rejectionMessage(reason) || t('errors.generic'))
}

export function isNotFound(message: string): boolean {
  return message === CODE_NOT_FOUND || NOT_FOUND.test(message)
}

function isNoConsent(message: string): boolean {
  return message === CODE_NO_CONSENT || NO_CONSENT.test(message)
}

/** Message for a failed read of one transcript stream or event list. */
export function describeError(message: string): string {
  if (!message) return ''
  if (isNotFound(message)) return t('session.errorNotFound')
  if (isNoConsent(message)) return t('session.errorNoConsent')
  return errorText(message)
}
