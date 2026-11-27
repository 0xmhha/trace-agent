// Locale state and message lookup shared by every page. Messages live in
// messages/<namespace>.ts; catalog.ts merges them. React components use the
// hook in index.tsx; plain modules may call t() directly, but must then be
// rendered again on a locale change (the provider remounts the tree).
import { catalogs, type MessageKey } from './catalog'

/** One supported language. Add a row and a column in every message file. */
export interface LocaleInfo {
  /** Short code sent to the desktop process and stored in localStorage. */
  code: string
  /** Name of the language in that language, for the selector. */
  name: string
  /** BCP 47 tag for Intl number and date formatting. */
  intl: string
}

/** Supported languages, in selector order. The first one is the default. */
export const LOCALES = [
  { code: 'en', name: 'English', intl: 'en-US' },
  { code: 'ko', name: '한국어', intl: 'ko-KR' },
] as const satisfies readonly LocaleInfo[]

export type Locale = (typeof LOCALES)[number]['code']
export const DEFAULT_LOCALE: Locale = LOCALES[0].code
export const LOCALE_STORAGE_KEY = 'trace-agent.locale'

export function isLocale(value: unknown): value is Locale {
  return LOCALES.some((l) => l.code === value)
}

function storedLocale(): Locale {
  try {
    const v = localStorage.getItem(LOCALE_STORAGE_KEY)
    return isLocale(v) ? v : DEFAULT_LOCALE
  } catch {
    return DEFAULT_LOCALE
  }
}

let current: Locale = storedLocale()
const listeners = new Set<() => void>()

export function getLocale(): Locale {
  return current
}

export function intlTag(locale: Locale = current): string {
  return LOCALES.find((l) => l.code === locale)?.intl ?? LOCALES[0].intl
}

/** Subscribes to locale changes; returns the unsubscribe function. */
export function subscribeLocale(listener: () => void): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

type LocaleBridge = { SetLocale?: (code: string) => Promise<void> }

/** Tells the desktop process, which localizes native dialogs and insight text. */
function tellNative(locale: Locale) {
  const b = (window as unknown as { go?: { main?: { Bridge?: LocaleBridge } } })
    .go?.main?.Bridge
  b?.SetLocale?.(locale).catch(() => {})
}

export function setLocale(locale: Locale) {
  if (locale === current) return
  current = locale
  try {
    localStorage.setItem(LOCALE_STORAGE_KEY, locale)
  } catch {
    // Private mode or a full store: the choice lasts for this window only.
  }
  tellNative(locale)
  document.documentElement.lang = locale
  listeners.forEach((l) => l())
}

/** Applies the stored locale to <html lang> and the desktop process at start. */
export function initLocale() {
  document.documentElement.lang = current
  tellNative(current)
}

export type Vars = Record<string, string | number>

function lookup(locale: Locale, key: string): string | undefined {
  return (catalogs[locale] as Record<string, string>)[key]
}

/**
 * Message for key in the current locale, falling back to English and then to
 * the key itself. {name} placeholders take vars.name. When vars.count is set
 * and the catalog has key + "_one" / key + "_other", the plural form for the
 * count is used.
 */
export function t(key: MessageKey, vars?: Vars): string {
  let template: string | undefined
  if (vars && typeof vars.count === 'number') {
    const form = new Intl.PluralRules(intlTag()).select(vars.count)
    template =
      lookup(current, `${key}_${form}`) ??
      lookup(current, `${key}_other`) ??
      lookup(DEFAULT_LOCALE, `${key}_${new Intl.PluralRules(intlTag(DEFAULT_LOCALE)).select(vars.count)}`) ??
      lookup(DEFAULT_LOCALE, `${key}_other`)
  }
  template ??= lookup(current, key) ?? lookup(DEFAULT_LOCALE, key) ?? key
  if (!vars) return template
  return template.replace(/\{(\w+)\}/g, (whole, name: string) =>
    name in vars ? String(vars[name]) : whole,
  )
}

/** True when key has a message in any locale; for codes sent by the backend. */
export function hasMessage(key: string): boolean {
  return LOCALES.some((l) => lookup(l.code, key) !== undefined)
}

/**
 * Text for an error. The desktop bridge rejects with stable codes such as
 * "usage.not_found"; those are translated from the errors namespace. Any
 * other message is shown as it is.
 */
export function errorText(reason: unknown, fallback: MessageKey = 'errors.generic'): string {
  const message =
    reason instanceof Error ? reason.message : typeof reason === 'string' ? reason : ''
  if (!message) return t(fallback)
  const key = `errors.${message}`
  return hasMessage(key) ? t(key as MessageKey) : message
}

export function formatNumber(n: number, options?: Intl.NumberFormatOptions): string {
  return new Intl.NumberFormat(intlTag(), options).format(n)
}

export function formatDate(value: Date | string | number, options?: Intl.DateTimeFormatOptions): string {
  return new Date(value).toLocaleString(intlTag(), options)
}
