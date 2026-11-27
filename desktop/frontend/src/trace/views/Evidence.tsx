import { obj, str, short, code, date } from '../../data'
import { TelemetryFields } from '../../TelemetryFields'
import { t } from '../../i18n'
import { Fields, Group, evidenceRows, type Props } from './shared'
export function UnlinkedPage({ screen: s, go }: Props) {
  return <Group title={t('trace.evidence.unlinked')}>{evidenceRows(s.evidence, go)}</Group>
}
export function EvidenceDetail({ screen: s }: Props) {
  const e = obj(s.evidence)
  return (
    <Group title={t('trace.evidence.id', { id: short(e.id) })}>
      <dl>
        {[
          [t('trace.evidence.kind'), e.recordKind],
          [t('trace.evidence.observer'), code(e.observer)],
          [t('trace.evidence.source'), short(e.sourceId)],
          [t('trace.evidence.occurred'), date(e.occurred)],
          [t('trace.evidence.observed'), date(e.observed)],
          [t('trace.evidence.link'), code(e.link)],
          [
            t('trace.evidence.reason'),
            e.reason ?? s.reason ?? t('trace.evidence.notProvided'),
          ],
        ].map(([k, v]) => (
          <div key={str(k)}>
            <dt>{str(k)}</dt>
            <dd>{str(v)}</dd>
          </div>
        ))}
      </dl>
      <TelemetryFields value={s.safeMetadata} />
      <Fields value={s.fields} />
    </Group>
  )
}
