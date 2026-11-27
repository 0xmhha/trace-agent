import type { InsightReport } from '../../types'
import { Card, Empty, Pill, fmtInt } from '../../ui'

const TITLE = '발견한 항목 없음'

/**
 * Shown when no rule fired. "Every rule passed" and "some rules could not be
 * judged yet" are different answers, and neither is the same as "still
 * reading" or "collection is off", which Empty words from the coverage.
 */
export function NothingFired({ report }: { report: InsightReport }) {
  const { coverage, rulesPassed: passed, rulesSkipped: skipped } = report
  if (coverage.state === 'scanning' || coverage.state === 'disabled')
    return (
      <Card title={TITLE}>
        <Empty coverage={coverage} />
      </Card>
    )
  // Unread files or skipped lines may hide something a rule would have caught.
  const caveat =
    coverage.state === 'partial' ? ' 읽지 못한 기록이 있어 결과가 달라질 수 있습니다.' : ''
  if (skipped > 0)
    return (
      <Card title={TITLE} action={<Pill tone="warn">판단 보류 있음</Pill>}>
        <Empty>
          {passed > 0 ? `판단할 수 있었던 규칙 ${fmtInt(passed)}개는 모두 통과했습니다. ` : ''}
          규칙 {fmtInt(skipped)}개는 자료가 모자라 아직 판단하지 못했습니다. 기록이 더 쌓이면
          다시 판단합니다.{caveat}
        </Empty>
      </Card>
    )
  if (passed > 0)
    return (
      <Card title={TITLE} action={<Pill tone="good">모두 통과</Pill>}>
        <Empty>
          규칙 {fmtInt(passed)}개를 모두 통과했습니다. 이 범위에서는 개선할 점을 찾지
          못했습니다.{caveat}
        </Empty>
      </Card>
    )
  return (
    <Card title={TITLE}>
      <Empty>이 범위에서 판단한 규칙이 없습니다.{caveat}</Empty>
    </Card>
  )
}
