import { obj, rows, str, short, label, date } from '../../data'
import { entity } from '../../nav'
import { t } from '../../i18n'
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
  const id = taskOf(route)
  return (
    <Group title={t('trace.agents.lineage')}>
      <Items
        items={rows(s.items)}
        render={(a) => (
          <>
            <button
              onClick={() =>
                go({ kind: 'AGENT', entity: entity(id, str(a.id)) })
              }
            >
              <ID value={a.id} />
            </button>
            <span>
              {t('trace.parent')} <ID value={a.parentId} />
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
  const id = taskOf(route)
  return (
    <>
      <Group title={t('trace.agent.title', { id: short(obj(s.agent).id) })}>
        <Badge value={obj(s.agent).state} />
        <p>{t('trace.mission', { mission: label(s.mission) })}</p>
        <p>
          {t('trace.agent.model')} <ID value={s.modelId} />
        </p>
      </Group>
      <Group title={t('trace.activity')}>
        {activityRows(s.activities, go, id)}
      </Group>
    </>
  )
}
