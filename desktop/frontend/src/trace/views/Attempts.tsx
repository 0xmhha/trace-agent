import { obj, rows, str, num, short, code, date } from '../../data'
import { entity } from '../../nav'
import { t } from '../../i18n'
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
  return <Group title={t('trace.attempts.title')}>{attemptRows(rows(s.items), go)}</Group>
}
export function AttemptDetail({ screen: s, go, route }: Props) {
  const id = taskOf(route),
    a = obj(s.attempt)
  return (
    <>
      <Group title={t('trace.attempt.title', { id: short(a.id) })}>
        <Badge value={a.phase} /> <Badge value={a.result} />
        <p>
          {date(a.started)} → {date(a.ended)}
        </p>
        <div className="actions">
          {a.moduleId ? (
            <button
              onClick={() =>
                go({ kind: 'MODULE', entity: entity(id, str(a.moduleId)) })
              }
            >
              {t('trace.attempt.openModule')}
            </button>
          ) : null}
          {a.agentId ? (
            <button
              onClick={() =>
                go({ kind: 'AGENT', entity: entity(id, str(a.agentId)) })
              }
            >
              {t('trace.attempt.openAgent')}
            </button>
          ) : null}
        </div>
      </Group>
      <Group title={t('trace.attempt.delivery')}>
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
      <Group title={t('trace.attempt.latency')}>
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
      <Group title={t('trace.attempt.io')}>
        <Fields
          value={[
            ...rows(s.request),
            ...rows(s.response),
            ...rows(s.conversation),
          ]}
        />
      </Group>
      <Group title={t('trace.evidenceRecords')}>{evidenceRows(s.evidence, go)}</Group>
    </>
  )
}
