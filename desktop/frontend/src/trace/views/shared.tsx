// Building blocks the trace views share. The sibling panels outside this
// folder reach them through ../../Screen.
import type { ReactNode } from 'react'
import {
  obj,
  rows,
  str,
  num,
  short,
  code,
  date,
  list,
  type Row,
  type Filter,
  type Stamp,
} from '../../data'
import { entity, type Go, type Query, type Route } from '../../nav'
export interface Props {
  screen: Row
  stamp: Stamp
  filter: Filter
  route: Route
  go: Go
  connected: boolean
  query: Query
  refresh: () => void
  sources: Row[]
}
/** Task the selection points at, named directly or through an entity. */
export function taskOf(r: Route): string {
  return r.selection.task?.taskId ?? r.selection.entity?.taskId ?? ''
}
export function Group({
  title,
  children,
}: {
  title: string
  children: ReactNode
}) {
  return (
    <section className="panel">
      <h2>{title}</h2>
      {children}
    </section>
  )
}
export function Items({
  items,
  render,
  empty = '이 범위의 기록이 없습니다.',
}: {
  items: Row[]
  render: (r: Row) => ReactNode
  empty?: string
}) {
  return items.length ? (
    <div className="items">
      {items.map((r, i) => (
        <div className="row" key={str(r.id) || str(r.key) || i}>
          {render(r)}
        </div>
      ))}
    </div>
  ) : (
    <p className="empty">{empty}</p>
  )
}
export function ID({ value }: { value: unknown }) {
  return <code title={str(value)}>{short(value)}</code>
}
export function Badge({ value }: { value: unknown }) {
  return (
    <span className={'badge ' + str(value).toLowerCase()}>
      {code(value) || '불명'}
    </span>
  )
}
export function EvidenceLinks({ ids, go }: { ids: unknown; go: Go }) {
  return (
    <span className="actions">
      {list(ids).map((id) => (
        <button
          key={id}
          onClick={() => go({ kind: 'UNLINKED_DETAIL', evidenceId: id })}
        >
          근거 {short(id)}
        </button>
      ))}
    </span>
  )
}
export function evidenceRows(page: unknown, go: Go) {
  return (
    <Items
      items={rows(obj(page).items)}
      render={(e) => (
        <>
          <button
            onClick={() =>
              go({ kind: 'UNLINKED_DETAIL', evidenceId: str(e.id) })
            }
          >
            {str(e.recordKind) || '근거'} <ID value={e.id} />
          </button>
          <Badge value={e.link} />
          <small>{date(e.observed)}</small>
        </>
      )}
    />
  )
}
export function activityRows(page: unknown, go: Go, taskId: string) {
  return (
    <Items
      items={rows(obj(page).items)}
      render={(a) => (
        <>
          <time>{date(a.at)}</time>
          <b>{code(a.kind)}</b>
          <Badge value={a.grade} />
          {a.attemptId ? (
            <button
              onClick={() =>
                go({
                  kind: 'ATTEMPT',
                  entity: entity(taskId, str(a.attemptId)),
                })
              }
            >
              호출 상세
            </button>
          ) : null}
          <EvidenceLinks ids={a.evidenceIds} go={go} />
        </>
      )}
    />
  )
}
export function moduleRows(items: Row[], go: Go) {
  return (
    <Items
      items={items}
      render={(m) => (
        <>
          <button
            onClick={() =>
              go({ kind: 'MODULE', entity: entity(str(m.taskId), str(m.id)) })
            }
          >
            {str(m.name) || short(m.id)}
          </button>
          <Badge value={m.kind} />
          <span>시도 {num(obj(m.count).count)}회</span>
        </>
      )}
    />
  )
}
export function attemptRows(items: Row[], go: Go) {
  return (
    <Items
      items={items}
      render={(a) => (
        <>
          <button
            onClick={() =>
              go({ kind: 'ATTEMPT', entity: entity(str(a.taskId), str(a.id)) })
            }
          >
            시도 <ID value={a.id} />
          </button>
          <Badge value={a.phase} />
          <Badge value={a.result} />
          {a.moduleId ? (
            <button
              onClick={() =>
                go({
                  kind: 'MODULE',
                  entity: entity(str(a.taskId), str(a.moduleId)),
                })
              }
            >
              모듈 <ID value={a.moduleId} />
            </button>
          ) : null}
          <small>{date(a.started)}</small>
        </>
      )}
    />
  )
}
export function Fields({ value }: { value: unknown }) {
  return (
    <Items
      items={rows(value)}
      empty="이 범위에 제공된 본문이 없습니다."
      render={(f) => (
        <>
          <span>{str(f.purpose)}</span>
          <Badge value={f.state} />
          {typeof f.text === 'string' && <p className="body">{f.text}</p>}
        </>
      )}
    />
  )
}
