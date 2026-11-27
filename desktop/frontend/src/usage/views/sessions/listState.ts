import type { MessageKey } from '../../../i18n'
import type { SessionSort } from '../../types'

/** Sort and search of the session list, kept in the route so reload and back restore them. */
export interface ListState {
  sort: SessionSort
  search: string
}

export const DEFAULT_SORT: SessionSort = 'last'
/** Longest search text sent to the store; longer input is cut. */
export const MAX_SEARCH_LEN = 100

/** Sort choices in display order. Adding an order means adding one entry here. */
export const sortOptions: readonly { id: SessionSort; labelKey: MessageKey }[] = [
  { id: 'last', labelKey: 'sessions.sortLast' },
  { id: 'tokens', labelKey: 'sessions.sortTokens' },
  { id: 'errors', labelKey: 'sessions.sortErrors' },
  { id: 'cost', labelKey: 'sessions.sortCost' },
]

const SEPARATOR = ':'
const sortIds = new Set<string>(sortOptions.map((o) => o.id))

/** Trims the text and cuts it to the length the list accepts. */
export function normalizeSearch(text: string): string {
  return text.trim().slice(0, MAX_SEARCH_LEN)
}

/**
 * Reads the list state from route.tab. The format is "<sort>" or
 * "<sort>:<search>"; anything unknown falls back to the default so a stale
 * or hand-edited address still opens the list.
 */
export function parseListState(tab: string | undefined): ListState {
  if (!tab) return { sort: DEFAULT_SORT, search: '' }
  const cut = tab.indexOf(SEPARATOR)
  const sort = cut < 0 ? tab : tab.slice(0, cut)
  return {
    sort: sortIds.has(sort) ? (sort as SessionSort) : DEFAULT_SORT,
    search: cut < 0 ? '' : normalizeSearch(tab.slice(cut + 1)),
  }
}

/** Inverse of parseListState. The default state has no tab, which keeps the address short. */
export function encodeListState(state: ListState): string | undefined {
  const search = normalizeSearch(state.search)
  if (search) return state.sort + SEPARATOR + search
  return state.sort === DEFAULT_SORT ? undefined : state.sort
}
