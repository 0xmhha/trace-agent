import { useEffect, useMemo, useRef, useState } from 'react'
import { errorText, formatNumber, t, type MessageKey } from '../i18n'
import type { UsageApi } from './api'
import type { Range, Status } from './types'
import { Card, ErrorNote, lastDays, useLoad } from './ui'
import { Dashboard } from './views/Dashboard'
import { Sessions } from './views/Sessions'
import { SessionDetail } from './views/SessionDetail'
import { ToolsView } from './views/Tools'
import { Insights } from './views/Insights'
import { Extensions } from './views/Extensions'
import { Settings } from './views/Settings'

export type UsageView =
  | 'dashboard'
  | 'sessions'
  | 'session'
  | 'tools'
  | 'insights'
  | 'extensions'
  | 'settings'

/** Location inside the usage pages. tab is owned by the view it belongs to. */
export interface UsageRoute {
  view: UsageView
  sessionId?: string
  tab?: string
  days?: number
  project?: string
}

/**
 * Props every view receives. refresh changes when stored data changed. A view
 * that shows range data renders CoverageNote with the coverage of its own
 * response; status.coverage describes the whole window, not the page's range.
 */
export interface ViewProps {
  api: UsageApi
  route: UsageRoute
  navigate: (route: UsageRoute) => void
  range: Range
  status: Status | null
  refresh: number
  /** Hands the Status returned by a mutation to the page at once. */
  onStatus: (status: Status) => void
}

/** Message of each view's name, used for the sidebar and the page title. */
const titles: Record<UsageView, MessageKey> = {
  dashboard: 'usage.navDashboard',
  sessions: 'usage.navSessions',
  session: 'usage.navSession',
  tools: 'usage.navTools',
  insights: 'usage.navInsights',
  extensions: 'usage.navExtensions',
  settings: 'usage.navSettings',
}

/** A sidebar entry whose label is looked up in the current locale when read. */
function navEntry(view: UsageView, icon: string): { view: UsageView; label: string; icon: string } {
  return {
    view,
    icon,
    get label() {
      return t(titles[view])
    },
  }
}

/** Sidebar entries, in display order. The session page is reached from the list. */
export const usageNav: readonly { view: UsageView; label: string; icon: string }[] = [
  navEntry('dashboard', '◔'),
  navEntry('sessions', '☰'),
  navEntry('tools', '⚒'),
  navEntry('insights', '✦'),
  navEntry('extensions', '⇄'),
  navEntry('settings', '⚙'),
]

export function usageTitle(route: UsageRoute): string {
  return t(titles[route.view])
}

const DEFAULT_DAYS = 7
const dayOptions = [1, 7, 30, 90] as const
const STATUS_POLL_MS = 3000
const REFRESH_MIN_MS = 10_000
const sessionIdPattern = /^[A-Za-z0-9._-]{1,128}$/
const views = new Set<string>(Object.keys(titles))

/** Parses "#view", "#session/<id>" and an optional "?days=7&project=x&tab=y". */
export function parseUsageHash(hash: string): UsageRoute | null {
  const [path = '', query = ''] = hash.replace(/^#/, '').split('?')
  const [view = '', id = ''] = path.split('/')
  if (!views.has(view)) return null
  const route: UsageRoute = { view: view as UsageView }
  if (view === 'session') {
    let sessionId = ''
    try {
      sessionId = decodeURIComponent(id)
    } catch {
      // A malformed escape in the address must not take the page down.
      return { view: 'sessions' }
    }
    if (!sessionIdPattern.test(sessionId)) return { view: 'sessions' }
    route.sessionId = sessionId
  }
  const params = new URLSearchParams(query)
  const days = Number(params.get('days'))
  if ((dayOptions as readonly number[]).includes(days)) route.days = days
  const project = params.get('project')
  if (project) route.project = project
  const tab = params.get('tab')
  if (tab) route.tab = tab
  return route
}

export function usageHash(route: UsageRoute): string {
  const params = new URLSearchParams()
  if (route.days && route.days !== DEFAULT_DAYS) params.set('days', String(route.days))
  if (route.project) params.set('project', route.project)
  if (route.tab) params.set('tab', route.tab)
  const query = params.toString()
  return (
    '#' +
    route.view +
    (route.view === 'session' && route.sessionId
      ? '/' + encodeURIComponent(route.sessionId)
      : '') +
    (query ? '?' + query : '')
  )
}

/** Stands in for the folder path so the sentence can wrap it in <code>. */
const PATH_MARK = '\u0000'
const DEFAULT_ROOT = '~/.claude/projects'
const consentPoints: readonly MessageKey[] = [
  'usage.consentKeepsCounts',
  'usage.consentNoText',
  'usage.consentLocal',
  'usage.consentSettings',
]

function ConsentCard(p: { api: UsageApi; root: string; done: () => void }) {
  const [error, setError] = useState('')
  const [before = '', after = ''] = t('usage.consentIntro', { path: PATH_MARK }).split(PATH_MARK)
  return (
    <Card title={t('usage.consentTitle')} className="u-consent">
      <p>
        {before}
        <code>{p.root || DEFAULT_ROOT}</code>
        {after}
      </p>
      <ul>
        {consentPoints.map((key) => (
          <li key={key}>{t(key)}</li>
        ))}
      </ul>
      <ErrorNote error={error} />
      <button
        className="u-primary"
        onClick={() =>
          p.api
            .enableWatch()
            .then((granted) => granted && p.done())
            .catch((e: unknown) => setError(errorText(e, 'usage.consentFailed')))
        }
      >
        {t('usage.consentStart')}
      </button>
    </Card>
  )
}

/** Pages that work before the user allowed transcript collection. */
const openWithoutConsent = new Set<UsageView>(['settings', 'extensions'])

export function UsagePage(p: {
  api: UsageApi
  route: UsageRoute
  navigate: (route: UsageRoute) => void
}) {
  const { api, route, navigate } = p
  const [status, setStatus] = useState<Status | null>(null),
    [statusError, setStatusError] = useState(''),
    [poll, setPoll] = useState(0)
  useEffect(() => {
    let stop = false
    const read = () =>
      api
        .status()
        .then((s) => {
          if (stop) return
          setStatus(s)
          setStatusError('')
        })
        .catch((e: unknown) => {
          if (!stop) setStatusError(errorText(e, 'usage.statusFailed'))
        })
    void read()
    const timer = setInterval(read, STATUS_POLL_MS)
    return () => {
      stop = true
      clearInterval(timer)
    }
  }, [api, poll])

  const days = route.days ?? DEFAULT_DAYS
  // A live session changes the data every few seconds. Pages follow the
  // version at most once per REFRESH_MIN_MS so they are not refetched on
  // every scan.
  const version = status?.dataVersion ?? 0
  const [refresh, setRefresh] = useState(0)
  const lastRefresh = useRef(0)
  useEffect(() => {
    const wait = Math.max(0, lastRefresh.current + REFRESH_MIN_MS - Date.now())
    const timer = setTimeout(() => {
      lastRefresh.current = Date.now()
      setRefresh(version)
    }, wait)
    return () => clearTimeout(timer)
  }, [version])
  // The range end moves only when the data or the selection changes, so a
  // status poll alone does not refetch every page.
  const range = useMemo(() => lastDays(days), [days, refresh])
  const projects = useLoad(() => api.projects({ range }), [api, range])

  const props: ViewProps = { api, route, navigate, range, status, refresh, onStatus: setStatus }
  const ranged = route.view !== 'session' && route.view !== 'settings'
  const gated = status !== null && !status.consent && !openWithoutConsent.has(route.view)

  return (
    <div className="u-page">
      {ranged && !gated && (
        <div className="u-toolbar">
          <label>
            {t('usage.period')}
            <select
              value={days}
              onChange={(e) => navigate({ ...route, days: Number(e.target.value) })}
            >
              {dayOptions.map((d) => (
                <option key={d} value={d}>
                  {t('usage.lastDays', { count: d })}
                </option>
              ))}
            </select>
          </label>
          <label>
            {t('usage.project')}
            <select
              value={route.project ?? ''}
              onChange={(e) => navigate({ ...route, project: e.target.value || undefined })}
            >
              <option value="">{t('usage.allProjects')}</option>
              {(projects.data?.items ?? []).map((item) => (
                <option key={item.key} value={item.key}>
                  {item.path || item.key}
                </option>
              ))}
            </select>
          </label>
          <span className="u-spacer" />
          {status?.scanning && (
            <span className="u-hint" role="status">
              {t('usage.scanProgress', {
                done: formatNumber(status.filesDone),
                total: formatNumber(status.filesTotal),
              })}
            </span>
          )}
        </div>
      )}
      <ErrorNote error={statusError} retry={() => setPoll((n) => n + 1)} />
      {gated ? (
        <ConsentCard api={api} root={status?.root ?? ''} done={() => setPoll((n) => n + 1)} />
      ) : (
        <>
          {route.view === 'dashboard' && <Dashboard {...props} />}
          {route.view === 'sessions' && <Sessions {...props} />}
          {route.view === 'session' && <SessionDetail {...props} />}
          {route.view === 'tools' && <ToolsView {...props} />}
          {route.view === 'insights' && <Insights {...props} />}
          {route.view === 'extensions' && <Extensions {...props} />}
          {route.view === 'settings' && <Settings {...props} />}
        </>
      )}
    </div>
  )
}
