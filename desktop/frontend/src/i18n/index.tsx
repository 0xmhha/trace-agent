import { useSyncExternalStore, type ReactNode } from 'react'
import {
  LOCALES,
  getLocale,
  setLocale,
  subscribeLocale,
  t,
  isLocale,
  type Locale,
} from './core'

export * from './core'
export type { MessageKey } from './catalog'

/** Current locale; re-renders the caller when it changes. */
export function useLocale(): Locale {
  return useSyncExternalStore(subscribeLocale, getLocale, getLocale)
}

/** Hook form of the i18n API for components. */
export function useI18n() {
  const locale = useLocale()
  return { locale, setLocale, t }
}

/**
 * Remounts its children when the locale changes, so that text computed
 * outside render (module helpers, memoized values) is computed again.
 */
export function I18nProvider({ children }: { children: ReactNode }) {
  const locale = useLocale()
  return <div key={locale} style={{ display: 'contents' }}>{children}</div>
}

/** Language selector. id ties it to an external <label>. */
export function LanguageSelect(p: { id?: string; className?: string }) {
  const locale = useLocale()
  return (
    <select
      id={p.id}
      className={p.className}
      aria-label={p.id ? undefined : t('common.language')}
      value={locale}
      onChange={(e) => {
        if (isLocale(e.target.value)) setLocale(e.target.value)
      }}
    >
      {LOCALES.map((l) => (
        <option key={l.code} value={l.code} lang={l.code}>
          {l.name}
        </option>
      ))}
    </select>
  )
}
