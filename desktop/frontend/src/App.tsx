// app.css comes first so that the usage pages' own styles follow it, the same
// order the usage preview loads them in.
import './app.css'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  navSelected,
  navSelection,
  traceNav,
  url,
  type Route,
  type TraceNavEntry,
} from './nav'
import { TraceWorkspace, type TraceNav } from './trace/TraceWorkspace'
import {
  UsagePage,
  parseUsageHash,
  usageHash,
  usageNav,
  usageTitle,
  type UsageRoute,
  type UsageView,
} from './usage'
import { nativeApi } from './usage/api'

const LANDING_VIEW: UsageView = 'dashboard'
// The session page has no sidebar entry of its own. It belongs to the list.
const SESSION_VIEW: UsageView = 'session'
const SESSION_LIST_VIEW: UsageView = 'sessions'
const USAGE_CAPTION_ID = 'nav-usage'
const TRACE_CAPTION_ID = 'nav-trace'

// History state of a usage entry. Trace entries keep the bare Route, which
// initial() in nav.ts tells apart by its selection.
type UsageState = { usage: UsageRoute }

/** The usage route an address names, or null when it is a trace address. */
function usageAt(hash: string): UsageRoute | null {
  return hash === '' || hash === '#'
    ? { view: LANDING_VIEW }
    : parseUsageHash(hash)
}
const traceOwnsAddress = () => !usageAt(location.hash)

function Sidebar(p: {
  usage: UsageRoute | null
  trace: TraceNav | null
  openUsage: (view: UsageView) => void
  openTrace: (entry: TraceNavEntry) => void
}) {
  const view =
    p.usage?.view === SESSION_VIEW ? SESSION_LIST_VIEW : p.usage?.view
  return (
    <aside>
      <div className="brand">
        <i aria-hidden="true">⌁</i>
        <strong>Trace Agent</strong>
      </div>
      <nav>
        <div
          className="navGroup"
          role="group"
          aria-labelledby={USAGE_CAPTION_ID}
        >
          <div className="navCaption" id={USAGE_CAPTION_ID}>
            사용 분석
          </div>
          {usageNav.map((n) => (
            <button
              key={n.view}
              className={n.view === view ? 'selected' : ''}
              onClick={() => p.openUsage(n.view)}
            >
              <span aria-hidden="true">{n.icon}</span>
              {n.label}
            </button>
          ))}
        </div>
        <div
          className="navGroup"
          role="group"
          aria-labelledby={TRACE_CAPTION_ID}
        >
          <div className="navCaption" id={TRACE_CAPTION_ID}>
            추적
          </div>
          {traceNav.map((n) => (
            <button
              key={n.label}
              className={
                p.trace && navSelected(n, p.trace.selection) ? 'selected' : ''
              }
              onClick={() => p.openTrace(n)}
            >
              <span>{n.icon}</span>
              {n.label}
            </button>
          ))}
        </div>
      </nav>
      {p.trace && (
        <div className="connection">
          <i className={p.trace.connected ? 'lamp on' : 'lamp'} />
          {p.trace.connectionState}
        </div>
      )}
    </aside>
  )
}

/**
 * The shell: the sidebar and the choice between the two workspaces. The
 * address decides. A usage address shows the usage pages, anything else the
 * trace workspace, which is mounted only then.
 */
export function App() {
  // Only the shell's own navigation and the browser's history moves can
  // change the workspace, so the hash is read again on those and nowhere else.
  const [hash, setHash] = useState(() => location.hash)
  const usage = useMemo(() => usageAt(hash), [hash])
  const main = useRef<HTMLElement>(null),
    refocus = useRef(false)
  useEffect(() => {
    const sync = () => setHash(location.hash)
    window.addEventListener('popstate', sync)
    window.addEventListener('hashchange', sync)
    return () => {
      window.removeEventListener('popstate', sync)
      window.removeEventListener('hashchange', sync)
    }
  }, [])
  const openUsage = useCallback((next: UsageRoute) => {
    const target = usageHash(next),
      shown = usageAt(location.hash)
    if (shown && usageHash(shown) === target) return
    const state: UsageState = { usage: next }
    history.pushState(state, '', target)
    setHash(location.hash)
  }, [])
  const tracing = !usage
  // Switching workspaces redraws the sidebar, which drops the keyboard focus
  // of the entry that was just activated.
  useEffect(() => {
    if (!refocus.current) return
    refocus.current = false
    document.querySelector<HTMLElement>('aside nav button.selected')?.focus()
  }, [tracing])
  const page = usage ? usage.view + '/' + (usage.sessionId ?? '') : ''
  useEffect(() => {
    if (main.current) main.current.scrollTop = 0
  }, [page])
  const sidebar = (trace: TraceNav | null) => (
    <Sidebar
      usage={usage}
      trace={trace}
      openUsage={(view) => {
        if (trace) {
          trace.leave()
          refocus.current = true
        }
        // The period and the project are page-wide choices; they follow the
        // user from one usage page to the next.
        openUsage({ view, days: usage?.days, project: usage?.project })
      }}
      openTrace={(entry) => {
        const selection = navSelection(entry)
        if (trace) {
          trace.go(selection, { live: true })
          return
        }
        const route: Route = { selection }
        history.pushState(route, '', url(route))
        refocus.current = true
        setHash(location.hash)
      }}
    />
  )
  return (
    <div className="layout">
      {usage ? (
        <>
          {sidebar(null)}
          <div className="workspace">
            <header className="pageHeader">
              <h1>{usageTitle(usage)}</h1>
            </header>
            <main ref={main}>
              <UsagePage api={nativeApi} route={usage} navigate={openUsage} />
            </main>
          </div>
        </>
      ) : (
        <TraceWorkspace sidebar={sidebar} owned={traceOwnsAddress} />
      )}
    </div>
  )
}
