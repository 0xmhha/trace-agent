/** Attribute list of an extension event, sorted by key so the order is stable. */
export function ExtAttributes({ attributes }: { attributes: Record<string, string> | undefined }) {
  const entries = Object.entries(attributes ?? {}).sort(([a], [b]) => a.localeCompare(b))
  if (!entries.length) return null
  return (
    <ul className="u-sd-attrs" aria-label="속성">
      {entries.map(([key, value]) => (
        <li key={key}>
          <span className="u-sd-attr-key">{key}</span>
          <span className="u-sd-attr-value">{value}</span>
        </li>
      ))}
    </ul>
  )
}
