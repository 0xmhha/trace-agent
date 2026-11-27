// Merges the namespace files into one catalog per locale. Adding a namespace
// means one import and one entry in NAMESPACES; adding a locale means one
// column in every namespace file and one row in LOCALES (core.ts).
import * as common from './messages/common'
import * as shell from './messages/shell'
import * as labels from './messages/labels'
import * as trace from './messages/trace'
import * as usage from './messages/usage'
import * as dashboard from './messages/dashboard'
import * as sessions from './messages/sessions'
import * as session from './messages/session'
import * as tools from './messages/tools'
import * as insights from './messages/insights'
import * as extensions from './messages/extensions'
import * as settings from './messages/settings'
import * as errors from './messages/errors'
import * as capabilities from './messages/capabilities'

const NAMESPACES = { common, shell, labels, trace, usage, dashboard, sessions, session, tools, insights, extensions, settings, errors, capabilities } as const

type Namespaces = typeof NAMESPACES
type KeysOf<N extends keyof Namespaces> = Extract<keyof Namespaces[N]['en'], string>
type PluralBase<K extends string> = K extends `${infer B}_${'one' | 'other' | 'zero' | 'two' | 'few' | 'many'}` ? B : never

/** Every message key, written "<namespace>.<key>", plus plural base keys. */
export type MessageKey = {
  [N in keyof Namespaces]: `${N}.${KeysOf<N> | PluralBase<KeysOf<N>>}`
}[keyof Namespaces]

function merge(locale: 'en' | 'ko'): Record<string, string> {
  const out: Record<string, string> = {}
  for (const [name, messages] of Object.entries(NAMESPACES)) {
    for (const [key, text] of Object.entries(messages[locale] as Record<string, string>)) {
      out[`${name}.${key}`] = text
    }
  }
  return out
}

export const catalogs = { en: merge('en'), ko: merge('ko') } as const
