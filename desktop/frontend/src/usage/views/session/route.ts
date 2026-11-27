// The session page keeps its whole view state in UsageRoute.tab so that a
// reload or the back button restores the tab, the selected agent and the
// timeline filters. The value is "<tab>[;a=<agent>][;h=<kind>,<kind>][;e=1]".
import type { MessageKey } from '../../../i18n'
import type { TimelineKind } from '../../types'
import { kindOrder } from './format'

export const sessionTabs = [
  { id: 'chat', labelKey: 'session.tabChat' },
  { id: 'tools', labelKey: 'session.tabTools' },
  { id: 'tokens', labelKey: 'session.tabTokens' },
  { id: 'agents', labelKey: 'session.tabAgents' },
  { id: 'ext', labelKey: 'session.tabExt' },
] as const satisfies readonly { id: string; labelKey: MessageKey }[]

export type SessionTab = (typeof sessionTabs)[number]['id']

/** View state of the session page. agentId is empty for the root agent. */
export interface SessionView {
  tab: SessionTab
  agentId: string
  /** Timeline kinds the user switched off. */
  hidden: ReadonlySet<TimelineKind>
  errorsOnly: boolean
}

const DEFAULT_TAB: SessionTab = 'chat'
const PART_SEPARATOR = ';'
const LIST_SEPARATOR = ','
const KEY_AGENT = 'a'
const KEY_HIDDEN = 'h'
const KEY_ERRORS = 'e'
const FLAG_ON = '1'

const tabIds = new Set<string>(sessionTabs.map((t) => t.id))
const kinds = new Set<string>(kindOrder)

function decode(value: string): string {
  try {
    return decodeURIComponent(value)
  } catch {
    // A hand-edited address with a broken escape falls back to the raw text.
    return value
  }
}

/** Reads UsageRoute.tab. Unknown parts are ignored so an old link still opens. */
export function parseSessionTab(raw: string | undefined): SessionView {
  const [head = '', ...parts] = (raw ?? '').split(PART_SEPARATOR)
  const view: { tab: SessionTab; agentId: string; hidden: Set<TimelineKind>; errorsOnly: boolean } = {
    tab: tabIds.has(head) ? (head as SessionTab) : DEFAULT_TAB,
    agentId: '',
    hidden: new Set(),
    errorsOnly: false,
  }
  for (const part of parts) {
    const at = part.indexOf('=')
    if (at < 0) continue
    const key = part.slice(0, at),
      value = part.slice(at + 1)
    if (key === KEY_AGENT) view.agentId = decode(value)
    if (key === KEY_ERRORS) view.errorsOnly = value === FLAG_ON
    if (key === KEY_HIDDEN)
      for (const kind of value.split(LIST_SEPARATOR))
        if (kinds.has(kind)) view.hidden.add(kind as TimelineKind)
  }
  return view
}

/** Writes UsageRoute.tab; the default view is undefined so the address stays short. */
export function formatSessionTab(view: SessionView): string | undefined {
  const parts: string[] = []
  if (view.agentId) parts.push(KEY_AGENT + '=' + encodeURIComponent(view.agentId))
  // kindOrder keeps the address the same whatever order the user clicked in.
  const hidden = kindOrder.filter((kind) => view.hidden.has(kind))
  if (hidden.length) parts.push(KEY_HIDDEN + '=' + hidden.join(LIST_SEPARATOR))
  if (view.errorsOnly) parts.push(KEY_ERRORS + '=' + FLAG_ON)
  if (view.tab === DEFAULT_TAB && !parts.length) return undefined
  return [view.tab, ...parts].join(PART_SEPARATOR)
}
