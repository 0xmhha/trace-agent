import { obj, rows, str, short, label, date } from '../../data'
import { entity } from '../../nav'
import {
  Badge,
  Group,
  ID,
  Items,
  activityRows,
  taskOf,
  type Props,
} from './shared'
export function AgentPage({ screen: s, go, route }: Props) {
  const t = taskOf(route)
  return (
    <Group title="에이전트 계보">
      <Items
        items={rows(s.items)}
        render={(a) => (
          <>
            <button
              onClick={() =>
                go({ kind: 'AGENT', entity: entity(t, str(a.id)) })
              }
            >
              <ID value={a.id} />
            </button>
            <span>
              부모 <ID value={a.parentId} />
            </span>
            <Badge value={a.state} />
            <time>{date(a.lastEvidenceAt)}</time>
          </>
        )}
      />
    </Group>
  )
}
export function AgentDetail({ screen: s, go, route }: Props) {
  const t = taskOf(route)
  return (
    <>
      <Group title={'에이전트 ' + short(obj(s.agent).id)}>
        <Badge value={obj(s.agent).state} />
        <p>미션 {label(s.mission)}</p>
        <p>
          모델 <ID value={s.modelId} />
        </p>
      </Group>
      <Group title="활동">{activityRows(s.activities, go, t)}</Group>
    </>
  )
}
