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
import { t, type MessageKey } from '../i18n'

type Snapshot = { stamp: Stamp; screen: Row }
// Changing the inspector or reselecting an open view keeps the same subscription.
function subscriptionKey(r: Route, defaultFilter: Filter) {
  return JSON.stringify([r.selection, r.fixed, r.filter ?? defaultFilter])
}
// Page title of every selection kind.
const names: Record<string, MessageKey> = {
  OVERVIEW: 'shell.titleOverview',
  TASKS: 'shell.titleTasks',
  TASK: 'shell.titleTask',
  AGENTS: 'shell.titleAgents',
  AGENT: 'shell.titleAgent',
  ATTEMPTS: 'shell.titleAttempts',
  ATTEMPT: 'shell.titleAttempt',
  MODULES: 'shell.titleModules',
  MODULE: 'shell.titleModule',
  MODULE_GROUPS: 'shell.titleModuleGroups',
  MODULE_GROUP: 'shell.titleModuleGroup',
  SOURCES: 'shell.titleSources',
  SOURCE: 'shell.titleSource',
  UNLINKED: 'shell.titleUnlinked',
  UNLINKED_DETAIL: 'shell.titleUnlinkedDetail',
  PLAN: 'shell.titlePlan',
  STEP_ACTIVITIES: 'shell.titleStepActivities',
  SEQUENCE: 'shell.titleSequence',
  IDENTITIES: 'shell.titleIdentities',
  IDENTITY: 'shell.titleIdentity',
}
// State of the connection to the local collector, worded at render.
const connectionText = {
  connecting: 'shell.connecting',
  connected: 'shell.connected',
  lost: 'shell.disconnected',
} as const satisfies Record<string, MessageKey>
type ConnectionState = keyof typeof connectionText
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
    [connectionState, setConnectionState] =
      useState<ConnectionState>('connecting')
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
      setConnectionState('lost')
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
          setConnectionState('connected')
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
      .catch(() => setError(t('shell.recheckFailed')))
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
            setError(t('shell.frozenExpired'))
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
          setError(t('shell.badScreen'))
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
                notice: 'shell.taskNotFound',
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
      ? t('shell.titleLiveTasks')
      : t(names[route.selection.kind] ?? 'shell.titleActivity')
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
        connectionState: t(connectionText[connectionState]),
        go,
        leave,
      })}
      <div className="workspace">
        <header>
          <div className="filters">
            <button
              aria-label={t('shell.back')}
              disabled={history.length <= 1}
              onClick={() => history.back()}
            >
              ←
            </button>
            <label>
              {t('shell.period')}{' '}
              <select
                value={hours}
                onChange={(e) =>
                  filters(Number(e.target.value), filter.clients?.[0] ?? '')
                }
              >
                <option value={1}>{t('shell.lastHour')}</option>
                <option value={24}>{t('shell.last24Hours')}</option>
                <option value={168}>{t('shell.last7Days')}</option>
              </select>
            </label>
            <label>
              {t('shell.client')}{' '}
              <select
                value={filter.clients?.[0] ?? ''}
                onChange={(e) => filters(hours, e.target.value)}
              >
                <option value="">{t('shell.all')}</option>
                <option value="CODEX">Codex</option>
                <option value="CLAUDE">Claude Code</option>
              </select>
            </label>
            {src.length > 0 && (
              <label>
                {t('shell.source')}{' '}
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
                  <option value="">{t('shell.all')}</option>
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
                {t('shell.taskStats')}
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
                {t('shell.clearTaskFilter', { task: short(filter.tasks[0]) })}
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
            {route.fixed ? t('shell.unfreeze') : t('shell.freeze')}
          </button>
        </header>
        <main ref={scroll}>
          <div className="heading">
            <div className="eyebrow">
              {route.fixed ? t('shell.modeFrozen') : t('shell.modeLive')}{' '}
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
              {t(route.notice)}
            </div>
          )}
          {route.caption && (
            <div className="notice">
              {route.caption}
              <button onClick={() => go(route.selection, { all: true })}>
                {t('shell.showAllAtPoint')}
              </button>
            </div>
          )}
          {route.fixed &&
            current &&
            current.revision !== route.fixed.point.revision && (
              <div className="notice">
                {t('shell.newData')}
                <button onClick={() => go(route.selection, { live: true })}>
                  {t('shell.toLatest')}
                </button>
              </div>
            )}
          {error && (
            <div role="alert" className="notice error">
              {error}
              <button onClick={() => reload((n) => n + 1)}>
                {t('shell.reload')}
              </button>
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
                  : Promise.reject(Error(t('shell.connectionLost')))
              }
              refresh={() => reload((n) => n + 1)}
              sources={src}
            />
          ) : (
            !error && (
              <div role="status" className="empty">
                {connection
                  ? t('shell.loading')
                  : t('shell.waitingCollector')}
              </div>
            )
          )}
        </main>
        <footer>
          <span>{t('shell.sourceCount', { count: src.length })}</span>
          <span>
            {src
              .map((s) =>
                t('shell.sourceHealth', {
                  health: code(s.health),
                  time: date(s.checkedAt),
                }),
              )
              .join(' / ')}
          </span>
          <span>
            {current
              ? t('shell.storedRevision', { revision: current.revision })
              : ''}
          </span>
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
