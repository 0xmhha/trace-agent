import { barPath, paintClass } from './parts'
import { useWidth } from './useWidth'

/** One ranked row: a name, a figure and a bar on a baseline shared by the list. */
export interface BarRow {
  key: string
  name: string
  /** Muted text after the name, e.g. the kind of a tool. */
  note?: string
  /** Length of the bar. */
  value: number
  /** Part of value drawn in the failure colour at the data end. */
  failed?: number
  /** Figure shown next to the name; the bar never is the only carrier of a value. */
  figure: string
}

const BAR_HEIGHT = 10
const BAR_RADIUS = 4
/** Surface-coloured gap between the plain and the failed part. */
const PART_GAP = 2
/** A failed part is kept this wide so that a small share stays visible. */
const FAILED_MIN = 3
const PLAIN_FILL = 'u-dash-fill ' + paintClass('s1')
const FAILED_FILL = 'u-dash-fill ' + paintClass('bad')

/**
 * Horizontal bars of one series. Every row uses the first series colour:
 * the rows are names, not separate series, and the length already ranks them.
 * The bar sits under its label so the list keeps working in a narrow card.
 */
export function BarList({ rows, label }: { rows: readonly BarRow[]; label: string }) {
  const [ref, width] = useWidth<HTMLUListElement>()
  const max = rows.reduce((m, r) => Math.max(m, r.value), 0)
  return (
    <ul ref={ref} className="u-dash-bars" aria-label={label}>
      {rows.map((row) => {
        const length = max > 0 ? (row.value / max) * width : 0
        const share = row.value > 0 ? Math.min(1, (row.failed ?? 0) / row.value) : 0
        const failed = share > 0 ? Math.min(length, Math.max(FAILED_MIN, share * length)) : 0
        const plain = failed > 0 ? Math.max(0, length - failed - PART_GAP) : length
        return (
          <li key={row.key}>
            <div className="u-dash-bar-head">
              <span className="u-dash-bar-name" title={row.name}>
                {row.name}
              </span>
              {row.note && <span className="u-dash-bar-note">{row.note}</span>}
              <span className="u-dash-bar-figure">{row.figure}</span>
            </div>
            {/* The figure above states every value the bar encodes. */}
            <svg className="u-dash-svg" width={width} height={BAR_HEIGHT} aria-hidden="true">
              {failed > 0 ? (
                <>
                  {plain > 0 && (
                    <rect className={PLAIN_FILL} width={plain} height={BAR_HEIGHT} />
                  )}
                  <path
                    className={FAILED_FILL}
                    d={barPath(length - failed, 0, failed, BAR_HEIGHT, BAR_RADIUS, 'right')}
                  />
                </>
              ) : (
                length > 0 && (
                  <path
                    className={PLAIN_FILL}
                    d={barPath(0, 0, length, BAR_HEIGHT, BAR_RADIUS, 'right')}
                  />
                )
              )}
            </svg>
          </li>
        )
      })}
    </ul>
  )
}
