// The browser suites read the page in one language. The page takes its
// language from localStorage at start (src/i18n/core.ts), so the value is
// written by an init script, before any page script runs.
const LOCALE_STORAGE_KEY = 'trace-agent.locale'
export const KOREAN = 'ko'
export const ENGLISH = 'en'

// Runs inside the page, so it can only use its argument.
function store({ key, locale }) {
  localStorage.setItem(key, locale)
}

/** Makes every document the page loads start in locale. */
export function pinLocale(page, locale) {
  return page.addInitScript(store, { key: LOCALE_STORAGE_KEY, locale })
}

/** The locale the page has stored, or null when it has none. */
export function storedLocale(page) {
  return page.evaluate((key) => localStorage.getItem(key), LOCALE_STORAGE_KEY)
}
