import { useEffect, useRef, useState, type ReactNode } from 'react'
import { Connection } from '../socket'
import {
  obj,
  str,
  point,
  stamp,
  date,
  withoutBodies,
  code,
  rows,
  short,
  type Row,
  type Stamp,
  type Filter,
} from '../data'
import {
  initial,
  interval,
  isLive,
  url,
  task,
  type Route,
  type Go,
  type Selection,
} from '../nav'
import { Screen } from './views/Screen'

type Snapshot = { stamp: Stamp; screen: Row }
// Changing the inspector or reselecting an open view keeps the same subscription.
function subscriptionKey(r: Route, defaultFilter: Filter) {
  return JSON.stringify([r.selection, r.fixed, r.filter ?? defaultFilter])
}
// Page title of every selection kind.
const names: Record<string, string> = {
  OVERVIEW: '개요',
  TASKS: '작업',
  TASK: '작업 상세',
  AGENTS: '하위 에이전트',
  AGENT: '에이전트 상세',
  ATTEMPTS: '호출 시도',
  ATTEMPT: '호출 상세',
  MODULES: '모듈',
  MODULE: '모듈 상세',
  MODULE_GROUPS: '모듈',
  MODULE_GROUP: '모듈 그룹',
  SOURCES: '출처와 설정',
  SOURCE: '출처 상세',
  UNLINKED: '미연계 근거',
  UNLINKED_DETAIL: '근거 상세',
  PLAN: '계획',
  STEP_ACTIVITIES: '단계 활동',
  SEQUENCE: '작업 시퀀스',
  IDENTITIES: '에이전트 식별',
  IDENTITY: '식별 상세',
}
/** What the shell's sidebar needs from the mounted trace workspace. */
export interface TraceNav {
  selection: Selection
  connected: boolean
  connectionState: string
  go: Go
  /**
   * Keeps the scroll position with the current history entry. The shell calls
   * it before it opens another workspace.
   */
  leave: () => void
}
/**
 * The trace application: one connection to the local collector, the watch
 * subscription of the selected screen and the history of trace routes.
 * It rewrites the browser history while it is mounted, so the shell mounts it
 * on trace addresses only.
 */
export function TraceWorkspace(p: {
  /** Draws the shell's sidebar, which shows the selection and connection. */
  sidebar: (nav: TraceNav) => ReactNode
  /** Whether the current address still belongs to the trace workspace. */
  owned: () => boolean
}) {
  const [connection, setConn] = useState<Connection | null>(null),
    [connectionState, setConnectionState] = useState('연결 중')
  const [route, setRoute] = useState<Route>(initial),
    [defaultFilter] = useState<Filter>(() => interval(24))
  const filter = route.filter ?? defaultFilter,
    hours = route.hours ?? 24
  const [snapshot, setSnapshot] = useState<Snapshot | null>(null),
    [status, setStatus] = useState<Row>({}),
    [error, setError] = useState(''),
    [cycle, reload] = useState(0)
  const [privacyGeneration, setPrivacyGeneration] = useState(0)
  const epoch = useRef(''),
    scroll = useRef<HTMLElement>(null),
    latest = useRef<Snapshot | null>(null),
    resume = useRef<{ key: string; value: string } | null>(null)
  const routeRef = useRef(route)
  routeRef.current = route
  const owned = useRef(p.owned)
  owned.current = p.owned
  const watchKey = subscriptionKey(route, defaultFilter)
  const clear = () => {
    latest.current = null
    setSnapshot(null)
  }
  // The entry that is about to be left remembers how far it was scrolled.
  function leave() {
    history.replaceState(
      {
        filter,
        hours,
        ...routeRef.current,
        scroll: scroll.current?.scrollTop ?? 0,
      },
      '',
      url(routeRef.current),
    )
  }
  function navigate(r: Route, replace = false) {
    r = { filter, hours, ...r }
    if (!replace) leave()
    if (
      subscriptionKey(routeRef.current, defaultFilter) !==
      subscriptionKey(r, defaultFilter)
    )
      clear()
    history[replace ? 'replaceState' : 'pushState'](r, '', url(r))
    setRoute(r)
    setError('')
  }
  const go: Go = (selection, opts = {}) => {
    const s = latest.current?.stamp
    const base =
      opts.scope ??
      route.fixed ??
      (s
        ? {
            filter: { ...filter, interval: s.effectiveInterval },
            point: s.point,
            cohort: s.cohort,
          }
        : undefined)
    const all = route.all ?? base
    const fixed = opts.live
      ? undefined
      : opts.all
        ? all
        : base
          ? { ...base, cohort: opts.cohort ?? base.cohort }
          : undefined
    navigate({
      selection,
      fixed,
      all: opts.live ? undefined : all,
      caption:
        opts.caption ?? (opts.live || opts.all ? undefined : route.caption),
      play:
        opts.live && route.fixed && selection.kind === 'SEQUENCE'
          ? true
          : undefined,
      scene: opts.scene,
      lane: opts.lane,
    })
  }
  useEffect(() => {
    function pop() {
      // The address left the trace routes and the shell is about to unmount
      // this workspace. Reading it as a trace route would start a new watch.
      if (!owned.current()) return
      const r = initial()
      if (
        subscriptionKey(routeRef.current, defaultFilter) !==
        subscriptionKey(r, defaultFilter)
      )
        clear()
      setRoute(r)
      setError('')
    }
    window.addEventListener('popstate', pop)
    return () => window.removeEventListener('popstate', pop)
  }, [])
  useEffect(() => {
    let stop = false,
      conn: Connection | undefined,
      timer: ReturnType<typeof setTimeout>,
      delay = 250
    const retry = () => {
      if (stop) return
      setConn(null)
      setConnectionState('연결 끊김')
      setPrivacyGeneration((n) => n + 1)
      setSnapshot((s) =>
        s ? { ...s, screen: obj(withoutBodies(s.screen)) } : null,
      )
      latest.current = null
      timer = setTimeout(connect, delay)
      delay = Math.min(5000, delay * 2)
    }
    const connect = () => {
      Connection.open(retry)
        .then((c) => {
          if (stop) {
            c.close()
            return
          }
          conn = c
          delay = 250
          setConn(c)
          setConnectionState('연결됨')
          setError('')
        })
        .catch(retry)
    }
    connect()
    return () => {
      stop = true
      clearTimeout(timer)
      conn?.close()
    }
  }, [])
  function invalidate(e: string) {
    epoch.current = e
    localStorage.removeItem('lastTask')
    clear()
    resume.current = null
    setPrivacyGeneration((n) => n + 1)
    const r = { selection: { kind: 'TASKS', activeTasksOnly: true } }
    history.replaceState(r, '', url(r))
    setRoute(r)
    connection
      ?.query('acknowledgeEpoch', { epoch: e })
      .then(() => reload((n) => n + 1))
      .catch(() => setError('조회 권한을 다시 확인하지 못했습니다.'))
  }
  useEffect(() => {
    if (!connection) return
    return connection.subscribe(
      'currentStatus',
      {},
      (data) => {
        const p = point(data.latest)
        if (epoch.current && BigInt(p.epoch) < BigInt(epoch.current)) return
        if (epoch.current && epoch.current !== p.epoch) invalidate(p.epoch)
        epoch.current = p.epoch
        setStatus(data)
      },
      setError,
    )
  }, [connection])
  useEffect(() => {
    if (!connection) return
    let waiting = true
    const currentRoute = routeRef.current,
      frozen = currentRoute.fixed
    const scope = frozen
      ? { ...frozen, mode: 'FROZEN' }
      : { filter, mode: 'LIVE', liveWindow: 'FOLLOW_END' }
    const restore =
      resume.current?.key === watchKey && latest.current
        ? resume.current.value
        : undefined
    const cancel = connection.subscribe(
      'watch',
      { input: { scope, selection: currentRoute.selection, resume: restore } },
      (frame) => {
        if (!waiting) return
        if (frame.__typename === 'Reset') {
          const e = point(frame.current).epoch
          if (
            frame.reason === 'EPOCH_CHANGED' ||
            (epoch.current && e !== epoch.current)
          ) {
            invalidate(e)
            return
          }
          resume.current = null
          clear()
          if (currentRoute.fixed) {
            navigate(
              { selection: { kind: 'TASKS', activeTasksOnly: true } },
              true,
            )
            setError('고정한 조회 시점이 만료됐습니다.')
          } else reload((n) => n + 1)
          return
        }
        const value =
          frame.__typename === 'Replacement' ? obj(frame.snapshot) : frame
        const s = stamp(value.stamp)
        if (epoch.current && BigInt(s.point.epoch) < BigInt(epoch.current))
          return
        if (epoch.current && epoch.current !== s.point.epoch) {
          invalidate(s.point.epoch)
          return
        }
        if (
          frame.__typename === 'Replacement' &&
          (!latest.current ||
            point(frame.base).revision !== latest.current.stamp.point.revision)
        ) {
          clear()
          resume.current = null
          reload((n) => n + 1)
          return
        }
        epoch.current = s.point.epoch
        const v = { stamp: s, screen: obj(value.screen) }
        if (!str(v.screen.__typename)) {
          setError('화면 응답이 올바르지 않습니다.')
          return
        }
        resume.current = { key: watchKey, value: s.resume }
        latest.current = v
        setSnapshot(v)
        setError('')
        if (currentRoute.selection.kind === 'SEQUENCE' && !frozen) {
          if (currentRoute.selection.task)
            localStorage.setItem('lastTask', currentRoute.selection.task.taskId)
          if (
            !currentRoute.play &&
            matchMedia('(prefers-reduced-motion: reduce)').matches
          ) {
            const fixed = {
              filter: { ...filter, interval: s.effectiveInterval },
              point: s.point,
              cohort: s.cohort,
            }
            navigate({ ...currentRoute, fixed, all: fixed }, true)
            return
          }
        }
        if (
          !currentRoute.notice &&
          currentRoute.selection.activeTasksOnly === true &&
          v.screen.__typename === 'TaskPage'
        ) {
          const tasks = rows(v.screen.items),
            last = localStorage.getItem('lastTask')
          const target =
            tasks.find((t) => t.id === last) ??
            (obj(v.screen.page).total === 1 ? tasks[0] : undefined)
          if (target)
            navigate(
              {
                selection: {
                  kind: 'SEQUENCE',
                  task: task(str(target.id)),
                  sequenceWindow: 'LATEST',
                },
              },
              true,
            )
        }
      },
      (reason) => {
        if (waiting) {
          if (currentRoute.explicit && !frozen) {
            navigate(
              {
                selection: { kind: 'TASKS', activeTasksOnly: true },
                notice:
                  '해당 작업을 현재 범위에서 찾지 못했습니다. 진행 중인 작업을 선택해 주세요.',
              },
              true,
            )
          } else {
            setError(reason)
            clear()
          }
        }
      },
    )
    return () => {
      waiting = false
      cancel()
    }
  }, [connection, watchKey, cycle])
  const loaded = !!snapshot
  useEffect(() => {
    if (loaded && scroll.current) scroll.current.scrollTop = route.scroll ?? 0
  }, [route, loaded])
  const current = pointOrEmpty(status.latest),
    src = rows(status.sources)
  const title =
    isLive(route.selection) && !route.selection.task
      ? '실시간 작업'
      : names[route.selection.kind] || '활동'
  function filters(h: number, client: string) {
    navigate({
      selection: { kind: 'OVERVIEW' },
      filter: {
        ...filter,
        ...interval(h),
        clients: client ? [client] : undefined,
      },
      hours: h,
    })
  }
  const selectedTask =
    route.selection.task?.taskId ?? route.selection.entity?.taskId
  return (
    <>
      {p.sidebar({
        selection: route.selection,
        connected: !!connection,
        connectionState,
        go,
        leave,
      })}
      <div className="workspace">
        <header>
          <div className="filters">
            <button
              aria-label="이전 화면"
              disabled={history.length <= 1}
              onClick={() => history.back()}
            >
              ←
            </button>
            <label>
              기간{' '}
              <select
                value={hours}
                onChange={(e) =>
                  filters(Number(e.target.value), filter.clients?.[0] ?? '')
                }
              >
                <option value={1}>최근 1시간</option>
                <option value={24}>최근 24시간</option>
                <option value={168}>최근 7일</option>
              </select>
            </label>
            <label>
              클라이언트{' '}
              <select
                value={filter.clients?.[0] ?? ''}
                onChange={(e) => filters(hours, e.target.value)}
              >
                <option value="">전체</option>
                <option value="CODEX">Codex</option>
                <option value="CLAUDE">Claude Code</option>
              </select>
            </label>
            {src.length > 0 && (
              <label>
                출처{' '}
                <select
                  value={filter.sources?.[0] ?? ''}
                  onChange={(e) => {
                    navigate({
                      selection: { kind: 'OVERVIEW' },
                      filter: {
                        ...filter,
                        sources: e.target.value ? [e.target.value] : undefined,
                      },
                    })
                  }}
                >
                  <option value="">전체</option>
                  {src.map((s) => (
                    <option key={str(s.sourceId)} value={str(s.sourceId)}>
                      {short(s.sourceId)}
                    </option>
                  ))}
                </select>
              </label>
            )}
            {selectedTask && !filter.tasks?.includes(selectedTask) && (
              <button
                onClick={() =>
                  navigate({
                    selection: { kind: 'OVERVIEW' },
                    filter: { ...filter, tasks: [selectedTask] },
                  })
                }
              >
                이 작업의 통계 보기
              </button>
            )}
            {filter.tasks?.length ? (
              <button
                onClick={() =>
                  navigate({
                    selection: { kind: 'OVERVIEW' },
                    filter: { ...filter, tasks: undefined },
                  })
                }
              >
                작업 필터 해제 · {short(filter.tasks[0])}
              </button>
            ) : null}
          </div>
          <button
            disabled={!snapshot}
            className="mode"
            onClick={() => {
              if (route.fixed) go(route.selection, { live: true })
              else go(route.selection)
            }}
          >
            {route.fixed ? '고정 해제' : '시점 고정'}
          </button>
        </header>
        <main ref={scroll}>
          <div className="heading">
            <div className="eyebrow">
              {route.fixed ? '고정' : '실시간'}{' '}
              {snapshot && ' / #' + snapshot.stamp.point.revision}
            </div>
            <h1>{title}</h1>
            {snapshot && (
              <span className="sub">
                {date(snapshot.stamp.effectiveInterval.from)} –{' '}
                {date(snapshot.stamp.effectiveInterval.until)} (
                {Intl.DateTimeFormat().resolvedOptions().timeZone})
              </span>
            )}
          </div>
          {route.notice && (
            <div role="status" className="notice">
              {route.notice}
            </div>
          )}
          {route.caption && (
            <div className="notice">
              {route.caption}
              <button onClick={() => go(route.selection, { all: true })}>
                같은 시점 전체 보기
              </button>
            </div>
          )}
          {route.fixed &&
            current &&
            current.revision !== route.fixed.point.revision && (
              <div className="notice">
                새 자료가 있습니다.
                <button onClick={() => go(route.selection, { live: true })}>
                  최신으로
                </button>
              </div>
            )}
          {error && (
            <div role="alert" className="notice error">
              {error}
              <button onClick={() => reload((n) => n + 1)}>다시 읽기</button>
            </div>
          )}
          {snapshot ? (
            <Screen
              key={watchKey + privacyGeneration}
              screen={snapshot.screen}
              stamp={snapshot.stamp}
              filter={route.fixed?.filter ?? filter}
              route={route}
              go={go}
              connected={!!connection}
              query={(n, v) =>
                connection
                  ? connection.query(n, v)
                  : Promise.reject(Error('연결이 끊겼습니다.'))
              }
              refresh={() => reload((n) => n + 1)}
              sources={src}
            />
          ) : (
            !error && (
              <div role="status" className="empty">
                {connection
                  ? '자료를 읽는 중입니다.'
                  : '로컬 수집기 연결을 기다립니다.'}
              </div>
            )
          )}
        </main>
        <footer>
          <span>현재 출처 {src.length}개</span>
          <span>
            {src
              .map((s) => code(s.health) + ' · 점검 ' + date(s.checkedAt))
              .join(' / ')}
          </span>
          <span>{current ? '저장 리비전 ' + current.revision : ''}</span>
        </footer>
      </div>
    </>
  )
}
function pointOrEmpty(value: unknown) {
  try {
    return point(value)
  } catch {
    return null
  }
}
