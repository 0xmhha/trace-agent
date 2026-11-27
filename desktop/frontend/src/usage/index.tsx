import { useEffect, useMemo, useRef, useState } from 'react'
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

/** Sidebar entries, in display order. The session page is reached from the list. */
export const usageNav: readonly { view: UsageView; label: string; icon: string }[] = [
  { view: 'dashboard', label: '대시보드', icon: '◔' },
  { view: 'sessions', label: '세션', icon: '☰' },
  { view: 'tools', label: '도구와 확장', icon: '⚒' },
  { view: 'insights', label: '인사이트', icon: '✦' },
  { view: 'extensions', label: '확장 이벤트', icon: '⇄' },
  { view: 'settings', label: '수집 설정', icon: '⚙' },
]

const titles: Record<UsageView, string> = {
  dashboard: '대시보드',
  sessions: '세션',
  session: '세션 상세',
  tools: '도구와 확장',
  insights: '인사이트',
  extensions: '확장 이벤트',
  settings: '수집 설정',
}
export function usageTitle(route: UsageRoute): string {
  return titles[route.view]
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

function ConsentCard(p: { api: UsageApi; root: string; done: () => void }) {
  const [error, setError] = useState('')
  return (
    <Card title="세션 기록 수집을 시작할까요?" className="u-consent">
      <p>
        이 Mac의 Claude Code 세션 기록 폴더(<code>{p.root || '~/.claude/projects'}</code>)를
        읽어 사용 현황을 집계합니다.
      </p>
      <ul>
        <li>토큰, 모델, 도구, 스킬, 훅 같은 수치와 이름만 앱의 저장소에 남깁니다.</li>
        <li>대화 원문은 저장하지 않고, 세션 상세를 열 때 원본 파일에서 바로 읽습니다.</li>
        <li>읽은 내용을 이 Mac 밖으로 보내지 않습니다.</li>
        <li>프로젝트별 제외와 수집 중지는 수집 설정에서 할 수 있습니다.</li>
      </ul>
      <ErrorNote error={error} />
      <button
        className="u-primary"
        onClick={() =>
          p.api
            .enableWatch()
            .then((granted) => granted && p.done())
            .catch((e: unknown) =>
              setError(e instanceof Error ? e.message : '수집을 켜지 못했습니다.'),
            )
        }
      >
        수집 시작
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
          if (!stop) setStatusError(e instanceof Error ? e.message : '상태를 읽지 못했습니다.')
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
            기간
            <select
              value={days}
              onChange={(e) => navigate({ ...route, days: Number(e.target.value) })}
            >
              {dayOptions.map((d) => (
                <option key={d} value={d}>
                  최근 {d}일
                </option>
              ))}
            </select>
          </label>
          <label>
            프로젝트
            <select
              value={route.project ?? ''}
              onChange={(e) => navigate({ ...route, project: e.target.value || undefined })}
            >
              <option value="">전체</option>
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
              수집 중 {status.filesDone}/{status.filesTotal}
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
