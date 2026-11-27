import { obj, rows, str, num, short, code, date } from '../../data'
import { entity } from '../../nav'
import {
  Badge,
  EvidenceLinks,
  Fields,
  Group,
  Items,
  attemptRows,
  evidenceRows,
  taskOf,
  type Props,
} from './shared'
export function AttemptPage({ screen: s, go }: Props) {
  return <Group title="호출 시도">{attemptRows(rows(s.items), go)}</Group>
}
export function AttemptDetail({ screen: s, go, route }: Props) {
  const t = taskOf(route),
    a = obj(s.attempt)
  return (
    <>
      <Group title={'시도 ' + short(a.id)}>
        <Badge value={a.phase} /> <Badge value={a.result} />
        <p>
          {date(a.started)} → {date(a.ended)}
        </p>
        <div className="actions">
          {a.moduleId ? (
            <button
              onClick={() =>
                go({ kind: 'MODULE', entity: entity(t, str(a.moduleId)) })
              }
            >
              모듈 열기
            </button>
          ) : null}
          {a.agentId ? (
            <button
              onClick={() =>
                go({ kind: 'AGENT', entity: entity(t, str(a.agentId)) })
              }
            >
              에이전트 열기
            </button>
          ) : null}
        </div>
      </Group>
      <Group title="전달 과정">
        <Items
          items={rows(s.edges)}
          render={(e) => (
            <>
              <time>{date(e.at)}</time>
              <b>
                {code(e.actor)} · {code(e.action)}
              </b>
              <Badge value={e.grade} />
              <EvidenceLinks ids={e.evidenceIds} go={go} />
            </>
          )}
        />
      </Group>
      <Group title="지연 근거">
        <Items
          items={rows(s.durations)}
          render={(d) => (
            <>
              <span>
                {code(d.basis)} · {num(d.ms)} ms
              </span>
              <EvidenceLinks ids={d.evidenceIds} go={go} />
            </>
          )}
        />
      </Group>
      <Group title="입력·응답">
        <Fields
          value={[
            ...rows(s.request),
            ...rows(s.response),
            ...rows(s.conversation),
          ]}
        />
      </Group>
      <Group title="근거 기록">{evidenceRows(s.evidence, go)}</Group>
    </>
  )
}
