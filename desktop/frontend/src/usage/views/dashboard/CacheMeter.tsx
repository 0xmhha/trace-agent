import type { Coverage, TokenTotals } from '../../types'
import { Empty, Pill, fmtCompact, fmtInt, fmtPct, ratio } from '../../ui'
import { UNIT } from './format'
import { paintClass } from './parts'
import { isCacheHitLow } from './tones'
import { useWidth } from './useWidth'

const METER_HEIGHT = 12

function Meter({ value, low }: { value: number; low: boolean }) {
  const [ref, width] = useWidth<HTMLDivElement>()
  const share = Math.max(0, Math.min(1, value))
  // The track is a light step of the fill colour, so the state reads across
  // the whole bar and not only on the filled part.
  const paint = paintClass(low ? 'warn' : 's1')
  return (
    <div ref={ref} className="u-dash-meter">
      <svg className="u-dash-svg" width={width} height={METER_HEIGHT} aria-hidden="true">
        <rect
          className={'u-dash-fill u-dash-track ' + paint}
          width={width}
          height={METER_HEIGHT}
          rx={METER_HEIGHT / 2}
        />
        {share > 0 && (
          <rect
            className={'u-dash-fill ' + paint}
            width={Math.max(METER_HEIGHT, share * width)}
            height={METER_HEIGHT}
            rx={METER_HEIGHT / 2}
          />
        )}
      </svg>
    </div>
  )
}

/**
 * Cache hit ratio as a meter, with the three kinds of input tokens it is
 * computed from. The ratio comes from the store; the shares of the parts are
 * derived here from the same totals.
 */
export function CacheMeter(p: {
  hitRatio: number | null
  tokens: TokenTotals
  coverage: Coverage
}) {
  const { hitRatio, tokens } = p
  if (hitRatio == null) return <Empty coverage={p.coverage} />
  const input = tokens.input + tokens.cacheRead + tokens.cacheCreate
  const low = isCacheHitLow(hitRatio)
  const parts = [
    { key: 'read', label: '캐시 읽기', hint: '캐시에서 다시 읽은 입력', value: tokens.cacheRead },
    { key: 'create', label: '캐시 생성', hint: '캐시에 새로 쓴 입력', value: tokens.cacheCreate },
    { key: 'input', label: '입력', hint: '캐시를 거치지 않은 입력', value: tokens.input },
  ]
  return (
    <div className="u-dash-cache">
      <div className="u-dash-cache-head">
        <strong className="u-dash-cache-value">{fmtPct(hitRatio)}</strong>
        {low && <Pill tone="warn">낮음</Pill>}
        <span className="u-hint">
          입력 {fmtCompact(input)} {UNIT.tokens} 중 캐시에서 읽은 비율
        </span>
      </div>
      <Meter value={hitRatio} low={low} />
      <table className="u-table u-dash-cache-parts">
        <caption className="u-dash-sr">입력 토큰의 구성</caption>
        <thead>
          <tr>
            <th scope="col">입력 구성</th>
            <th scope="col" className="num">
              토큰
            </th>
            <th scope="col" className="num">
              비율
            </th>
          </tr>
        </thead>
        <tbody>
          {parts.map((part) => (
            <tr key={part.key}>
              <th scope="row">
                {part.label}
                <small className="u-hint">{part.hint}</small>
              </th>
              <td className="num">{fmtInt(part.value)}</td>
              <td className="num">{fmtPct(ratio(part.value, input))}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
