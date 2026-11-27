import { useEffect, useState } from 'react'
import { Flow } from './Flow'
import { Group, Items, Badge, EvidenceLinks, type Props } from './Screen'
import { obj, str, num, short, code, date, rows, label, type Row } from './data'
import { t } from './i18n'
export function Sequence({
  screen,
  stamp,
  filter,
  route,
  go,
  query,
  connected,
}: Props) {
  const task = route.selection.task!,
    lanes = rows(screen.lanes),
    events = rows(screen.events)
  const before = route.scene === 'before',
    event = before
      ? undefined
      : (events.find((e) => e.id === route.scene) ?? events.at(-1)),
    id = str(event?.id),
    lane = route.lane ?? str(event?.toLaneId ?? event?.fromLaneId)
  const [moment, setMoment] = useState<Row>({}),
    [inspector, inspect] = useState<Row>({}),
    [err, setErr] = useState('')
  const key = JSON.stringify([stamp.point, id, lane])
  useEffect(() => {
    let valid = true
    setMoment({})
    inspect({})
    setErr('')
    if (!id || !connected) return
    const scope = {
      filter: { ...filter, interval: stamp.effectiveInterval },
      point: stamp.point,
      cohort: stamp.cohort,
    }
    const checks = [
      query('sequenceMoment', {
        scope,
        key: task,
        eventId: id,
        laneIds: lanes.map((l) => l.id),
      }),
      ...(lane
        ? [
            query('sequenceInspector', {
              scope,
              key: task,
              eventId: id,
              laneId: lane,
            }),
          ]
        : []),
    ]
    Promise.all(checks)
      .then(([m, i]) => {
        if (valid) {
          setMoment(obj(m?.data))
          inspect(obj(i?.data))
        }
      })
      .catch(() => {
        if (valid) setErr(t('trace.sequence.loadFailed'))
      })
    return () => {
      valid = false
    }
  }, [key, connected])
  const metrics = obj(
    before
      ? screen.metricsBeforePage
      : (moment.metrics ?? (!route.scene ? screen.latestMetrics : undefined)),
  )
  const select = (scene: string, l?: string) =>
    go(route.selection, { scene, lane: l })
  const ix = events.findIndex((e) => e.id === id),
    at = rows(moment.lanes)
  return (
    <>
      <div className="metrics">
        {[
          [t('trace.subagents'), 'childAgents'],
          [t('trace.running'), 'runningChildAgents'],
          [t('trace.waiting'), 'waitingAttempts'],
        ].map(([name, k]) => (
          <div className="metric" key={k}>
            <span>{name}</span>
            <strong>{metrics[k!] ? num(obj(metrics[k!]).count) : '—'}</strong>
          </div>
        ))}
      </div>
      <Group title={t('trace.sequence.participants')}>
        <div className="toolbar">
          <button
            disabled={!events.length}
            onClick={() =>
              go(
                {
                  ...route.selection,
                  sequenceWindow: 'FIRST',
                  page: undefined,
                },
                { scene: 'before' },
              )
            }
          >
            {t('trace.sequence.fromStart')}
          </button>
          <button
            disabled={ix <= 0}
            onClick={() => select(str(events[ix - 1]?.id))}
          >
            {t('trace.sequence.previousEvent')}
          </button>
          <button
            disabled={!id}
            onClick={() =>
              route.fixed
                ? go(
                    { ...route.selection, sequenceWindow: 'LATEST' },
                    { live: true },
                  )
                : select(id)
            }
          >
            {route.fixed
              ? t('trace.sequence.resumeLive')
              : t('trace.sequence.freeze')}
          </button>
          <button
            disabled={!events.length || ix >= events.length - 1}
            onClick={() => select(str(events[ix + 1]?.id))}
          >
            {t('trace.sequence.nextEvent')}
          </button>
          {screen.previousPage ? (
            <button
              onClick={() =>
                go({
                  ...route.selection,
                  sequenceWindow: route.selection.sequenceWindow ?? 'LATEST',
                  page: { first: 50, after: screen.previousPage },
                })
              }
            >
              {t('trace.page.previous')}
            </button>
          ) : null}
        </div>
        <div className="lanes">
          {lanes.map((l) => (
            <button
              key={str(l.id)}
              className={l.id === lane ? 'active' : ''}
              onClick={() => select(id, str(l.id))}
            >
              <b>{code(l.kind)}</b>
              <small>{short(l.id)}</small>
              <Badge
                value={obj(at.find((a) => a.laneId === l.id)?.status).activity}
              />
            </button>
          ))}
        </div>
        <Flow lanes={lanes} events={events} selected={id} onSelect={select} />
        <details>
          <summary>{t('trace.sequence.eventTable')}</summary>
          <Items
            items={events}
            render={(e) => (
              <button
                className={'event ' + (e.id === id ? 'picked' : '')}
                onClick={() => select(str(e.id))}
              >
                <time>{date(e.occurred)}</time>
                <b>{code(e.action)}</b>
                <span>
                  {short(e.fromLaneId)} → {short(e.toLaneId)}
                </span>
                <small>{code(e.observedSide)}</small>
              </button>
            )}
          />
        </details>
      </Group>
      {err && <p role="alert">{err}</p>}
      {event && (
        <Group title={t('trace.sequence.selected', { action: code(event.action) })}>
          <p>
            {date(event.occurred)}　{code(event.grade)}　
            {code(event.observedSide)}
          </p>
          <p>{t('trace.mission', { mission: label(inspector.mission) })}</p>
          <Badge value={obj(inspector.status).activity} />
          <p>
            {t('trace.sequence.waitCount', {
              count: num(obj(obj(inspector.waits).page).total),
            })}
          </p>
          <Items
            items={rows(obj(inspector.waits).items)}
            empty={t('trace.sequence.noWaits')}
            render={(w) => (
              <>
                <span>
                  {t('trace.sequence.waitTarget', {
                    id: short(w.laneId ?? w.attemptId),
                  })}
                </span>
                <EvidenceLinks ids={w.evidenceIds} go={go} />
              </>
            )}
          />
          <EvidenceLinks ids={event.evidenceIds} go={go} />
          {event.attemptId ? (
            <button
              onClick={() =>
                go({
                  kind: 'ATTEMPT',
                  entity: { taskId: task.taskId, id: str(event.attemptId) },
                })
              }
            >
              {t('trace.sequence.openCall')}
            </button>
          ) : null}
          <Items
            items={rows(screen.relations).filter(
              (r) => r.toEventId === id || r.fromEventId === id,
            )}
            empty={t('trace.sequence.noRelations')}
            render={(r) => (
              <span>
                {code(r.kind)} · {short(r.fromEventId)} → {short(r.toEventId)}
              </span>
            )}
          />
        </Group>
      )}
    </>
  )
}
