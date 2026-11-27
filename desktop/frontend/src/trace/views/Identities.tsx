import { obj, rows, str, code, label } from '../../data'
import { task } from '../../nav'
import { t } from '../../i18n'
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
    <Group title={t('trace.identities.title')}>
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
              {t('trace.parent')} <ID value={i.parentId} />
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
      <Group title={t('trace.identity.assignments')}>
        <Items
          items={rows(obj(s.assignments).items)}
          render={(a) => (
            <>
              <button
                onClick={() => go({ kind: 'TASK', task: task(str(a.taskId)) })}
              >
                {t('trace.task.label')} <ID value={a.taskId} />
              </button>
              <span>{label(a.mission)}</span>
              <Badge value={a.grade} />
              <EvidenceLinks ids={a.evidenceIds} go={go} />
            </>
          )}
        />
      </Group>
      <Group title={t('trace.identity.evidence')}>{evidenceRows(s.evidence, go)}</Group>
    </>
  )
}
