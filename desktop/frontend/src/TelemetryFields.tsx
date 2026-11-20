import { obj } from './data'

export function TelemetryFields({ value }: { value: unknown }) {
  const meta = obj(value)
  const rows: [string, string][] = []
  for (const [key, label, suffix] of [
    ['attempt', 'API 시도 번호 (0부터)', ''],
    ['httpStatus', 'HTTP 상태 코드', ''],
    ['durationMs', '클라이언트가 보고한 시간', ' ms'],
  ] as const) {
    const n = meta[key]
    if (typeof n === 'number' && Number.isSafeInteger(n) && n >= 0)
      rows.push([label, String(n) + suffix])
  }
  if (typeof meta.toolSuccess === 'boolean')
    rows.push(['도구 결과', meta.toolSuccess ? '성공' : '실패'])
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
