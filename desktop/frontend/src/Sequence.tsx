import { useEffect, useState } from 'react'
import { Flow } from './Flow'
import { Group, Items, Badge, EvidenceLinks, type Props } from './Screen'
import { obj, str, num, short, code, date, rows, label, type Row } from './data'
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
        if (valid) setErr('선택한 사건을 읽지 못했습니다.')
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
          ['하위 에이전트', 'childAgents'],
          ['실행 중', 'runningChildAgents'],
          ['응답 대기', 'waitingAttempts'],
        ].map(([name, k]) => (
          <div className="metric" key={k}>
            <span>{name}</span>
            <strong>{metrics[k!] ? num(obj(metrics[k!]).count) : '—'}</strong>
          </div>
        ))}
      </div>
      <Group title="참여자와 사건">
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
            처음부터
          </button>
          <button
            disabled={ix <= 0}
            onClick={() => select(str(events[ix - 1]?.id))}
          >
            이전 사건
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
            {route.fixed ? '실시간 재개' : '장면 고정'}
          </button>
          <button
            disabled={!events.length || ix >= events.length - 1}
            onClick={() => select(str(events[ix + 1]?.id))}
          >
            다음 사건
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
              앞 페이지
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
          <summary>사건 표</summary>
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
        <Group title={'선택한 사건 · ' + code(event.action)}>
          <p>
            {date(event.occurred)}　{code(event.grade)}　
            {code(event.observedSide)}
          </p>
          <p>미션 {label(inspector.mission)}</p>
          <Badge value={obj(inspector.status).activity} />
          <p>응답 대기 {num(obj(obj(inspector.waits).page).total)}건</p>
          <Items
            items={rows(obj(inspector.waits).items)}
            empty="대기 대상이 없습니다."
            render={(w) => (
              <>
                <span>대상 {short(w.laneId ?? w.attemptId)}</span>
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
              호출 열기
            </button>
          ) : null}
          <Items
            items={rows(screen.relations).filter(
              (r) => r.toEventId === id || r.fromEventId === id,
            )}
            empty="명시된 사건 관계가 없습니다."
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
