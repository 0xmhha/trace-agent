import { obj, rows, str, code, label } from '../../data'
import { task } from '../../nav'
import {
  Badge,
  EvidenceLinks,
  Group,
  ID,
  Items,
  evidenceRows,
  type Props,
} from './shared'
export function IdentityPage({ screen: s, go }: Props) {
  return (
    <Group title="에이전트 식별">
      <Items
        items={rows(s.items)}
        render={(i) => (
          <>
            <button
              onClick={() => go({ kind: 'IDENTITY', identityId: str(i.id) })}
            >
              <ID value={i.id} />
            </button>
            <span>{code(i.attribution)}</span>
            <span>
              부모 <ID value={i.parentId} />
            </span>
          </>
        )}
      />
    </Group>
  )
}
export function IdentityDetail({ screen: s, go }: Props) {
  return (
    <>
      <Group title="작업 배정">
        <Items
          items={rows(obj(s.assignments).items)}
          render={(a) => (
            <>
              <button
                onClick={() => go({ kind: 'TASK', task: task(str(a.taskId)) })}
              >
                작업 <ID value={a.taskId} />
              </button>
              <span>{label(a.mission)}</span>
              <Badge value={a.grade} />
              <EvidenceLinks ids={a.evidenceIds} go={go} />
            </>
          )}
        />
      </Group>
      <Group title="식별 근거">{evidenceRows(s.evidence, go)}</Group>
    </>
  )
}
