import { rows, str, num, label, date } from '../../data'
import { task } from '../../nav'
import { t } from '../../i18n'
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
  const id = taskOf(route)
  return (
    <Group title={t('trace.plan.versions')}>
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
                        task: task(id),
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
        <p className="empty">{t('trace.plan.none')}</p>
      )}
    </Group>
  )
}
/** Activities of one plan step. */
export function ActivityPage({ screen: s, go, route }: Props) {
  const id = taskOf(route)
  return (
    <Group title={t('trace.plan.stepActivity')}>
      {activityRows(s, go, id)}
    </Group>
  )
}
