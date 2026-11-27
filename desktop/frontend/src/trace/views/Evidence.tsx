import { obj, str, short, code, date } from '../../data'
import { TelemetryFields } from '../../TelemetryFields'
import { Fields, Group, evidenceRows, type Props } from './shared'
export function UnlinkedPage({ screen: s, go }: Props) {
  return <Group title="미연계 근거">{evidenceRows(s.evidence, go)}</Group>
}
export function EvidenceDetail({ screen: s }: Props) {
  const e = obj(s.evidence)
  return (
    <Group title={'근거 ' + short(e.id)}>
      <dl>
        {[
          ['종류', e.recordKind],
          ['관찰자', code(e.observer)],
          ['출처', short(e.sourceId)],
          ['발생', date(e.occurred)],
          ['관찰', date(e.observed)],
          ['연결', code(e.link)],
          ['판정 근거', e.reason ?? s.reason ?? '미제공'],
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
