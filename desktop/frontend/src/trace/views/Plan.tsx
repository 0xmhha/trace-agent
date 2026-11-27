import { rows, str, num, label, date } from '../../data'
import { task } from '../../nav'
import {
  Badge,
  EvidenceLinks,
  Group,
  Items,
  activityRows,
  taskOf,
  type Props,
} from './shared'
export function PlanPage({ screen: s, go, route }: Props) {
  const t = taskOf(route)
  return (
    <Group title="계획 버전">
      {rows(s.versions).length ? (
        rows(s.versions).map((v) => (
          <div key={str(v.id)}>
            <h3>{date(v.at)}</h3>
            <Items
              items={rows(v.steps)}
              render={(step) => (
                <>
                  <button
                    onClick={() =>
                      go({
                        kind: 'STEP_ACTIVITIES',
                        task: task(t),
                        planId: str(v.id),
                        stepId: str(step.key),
                      })
                    }
                  >
                    {num(step.ordinal) + 1}. {label(step.title)}
                  </button>
                  <Badge value={step.state} />
                  <EvidenceLinks ids={step.evidenceIds} go={go} />
                </>
              )}
            />
          </div>
        ))
      ) : (
        <p className="empty">계획을 제공하지 않았습니다.</p>
      )}
    </Group>
  )
}
/** Activities of one plan step. */
export function ActivityPage({ screen: s, go, route }: Props) {
  const t = taskOf(route)
  return <Group title="단계 활동">{activityRows(s, go, t)}</Group>
}
