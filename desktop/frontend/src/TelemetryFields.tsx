import { obj } from './data'
import { t } from './i18n'

export function TelemetryFields({ value }: { value: unknown }) {
  const meta = obj(value)
  const rows: [string, string][] = []
  for (const [key, label, suffix] of [
    ['attempt', 'trace.telemetry.attempt', ''],
    ['httpStatus', 'trace.telemetry.httpStatus', ''],
    ['durationMs', 'trace.telemetry.duration', ' ms'],
  ] as const) {
    const n = meta[key]
    if (typeof n === 'number' && Number.isSafeInteger(n) && n >= 0)
      rows.push([t(label), String(n) + suffix])
  }
  if (typeof meta.toolSuccess === 'boolean')
    rows.push([
      t('trace.telemetry.toolResult'),
      meta.toolSuccess ? t('trace.telemetry.success') : t('trace.telemetry.failure'),
    ])
  if (!rows.length) return null
  return (
    <dl>
      {rows.map(([label, text]) => (
        <div key={label}>
          <dt>{label}</dt>
          <dd>{text}</dd>
        </div>
      ))}
    </dl>
  )
}
