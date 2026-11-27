import { useEffect, useMemo, useState } from 'react'
import { formatSessionTab, parseSessionTab, type SessionView } from './route'

/**
 * The session view state, read from and written to UsageRoute.tab.
 *
 * The address changes a moment after pushTab is called. Until it does, the
 * value just chosen is shown, so a checkbox or select bound to the view does
 * not snap back for a frame. Once the address moves, in either direction, it
 * is the only source again.
 */
export function useSessionView(
  tab: string | undefined,
  pushTab: (tab: string | undefined) => void,
): [SessionView, (next: SessionView) => void] {
  const fromRoute = useMemo(() => parseSessionTab(tab), [tab])
  const [pending, setPending] = useState<{ from: string | undefined; view: SessionView } | null>(null)
  useEffect(() => setPending(null), [tab])
  const view = pending && pending.from === tab ? pending.view : fromRoute
  const setView = (next: SessionView) => {
    setPending({ from: tab, view: next })
    pushTab(formatSessionTab(next))
  }
  return [view, setView]
}
