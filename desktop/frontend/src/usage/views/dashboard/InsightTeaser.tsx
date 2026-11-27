import type { InsightReport, Severity } from '../../types'
import { Card, Empty, ErrorNote, Pill, type Loaded, type Tone } from '../../ui'
import { UNIT, withUnit } from './format'

/** How many insights the dashboard previews. The insights view lists all of them. */
const TEASER_COUNT = 3

/** Severities from most to least severe, with the label and tone of their pill. */
const SEVERITIES: readonly { id: Severity; label: string; tone: Tone }[] = [
  { id: 'critical', label: '심각', tone: 'bad' },
  { id: 'warn', label: '주의', tone: 'warn' },
  { id: 'info', label: '참고', tone: 'muted' },
]

function rank(severity: Severity): number {
  const index = SEVERITIES.findIndex((s) => s.id === severity)
  return index < 0 ? SEVERITIES.length : index
}

function Body({ report }: { report: InsightReport }) {
  const judged = report.rulesFired + report.rulesPassed
  if (!report.items.length) {
    // Before the scan finished, "nothing fired" would be a false all-clear.
    const state = report.coverage.state
    if (state === 'scanning' || state === 'disabled') return <Empty coverage={report.coverage} />
    return (
      <p className="u-dash-clear">
        눈에 띄는 문제가 없습니다. 규칙 {withUnit(judged, UNIT.items)}를 확인했고, 표본이 모자란{' '}
        {withUnit(report.rulesSkipped, UNIT.items)}는 건너뛰었습니다.
      </p>
    )
  }
  // sort is stable, so insights of one severity keep the order of the report.
  const top = [...report.items]
    .sort((a, b) => rank(a.severity) - rank(b.severity))
    .slice(0, TEASER_COUNT)
  return (
    <>
      <ul className="u-dash-insights">
        {top.map((item) => {
          const severity = SEVERITIES[rank(item.severity)]
          return (
            <li key={item.id}>
              <Pill tone={severity?.tone ?? 'muted'}>{severity?.label ?? item.severity}</Pill>
              <span>{item.title}</span>
            </li>
          )
        })}
      </ul>
      <p className="u-hint">
        규칙 {withUnit(judged, UNIT.items)}를 확인해 {withUnit(report.rulesFired, UNIT.items)}가
        해당했습니다. 표본이 모자란 {withUnit(report.rulesSkipped, UNIT.items)}는 건너뛰었습니다.
      </p>
    </>
  )
}

/** The most severe insights of the range, with a way into the insights view. */
export function InsightTeaser(p: {
  insights: Loaded<InsightReport>
  onOpen: () => void
  className?: string
}) {
  const { data, error, loading, reload } = p.insights
  return (
    <Card
      title="인사이트"
      hint={`심각한 순서로 ${TEASER_COUNT}건`}
      className={p.className}
      action={
        <button type="button" className="u-dash-link" onClick={p.onOpen}>
          전체 보기
        </button>
      }
    >
      <ErrorNote error={error} retry={reload} />
      {data ? (
        <Body report={data} />
      ) : (
        loading && (
          <p className="u-empty" role="status">
            인사이트를 불러오는 중입니다.
          </p>
        )
      )}
    </Card>
  )
}
