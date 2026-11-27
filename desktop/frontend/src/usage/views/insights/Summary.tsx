import { useId } from 'react'
import type { InsightReport } from '../../types'
import { Card, fmtDateTime, fmtInt, fmtPct, ratio } from '../../ui'

type Outcome = 'fired' | 'passed' | 'skipped'

interface OutcomeCount {
  id: Outcome
  label: string
  hint: string
  /** Shown in the key so the three outcomes differ by more than color. */
  glyph: string
  count: number
}

function outcomes(report: InsightReport): OutcomeCount[] {
  return [
    {
      id: 'fired',
      label: '발견',
      hint: '개선할 점을 찾은 규칙',
      glyph: '!',
      count: report.rulesFired,
    },
    {
      id: 'passed',
      label: '통과',
      hint: '문제를 찾지 못한 규칙',
      glyph: '✓',
      count: report.rulesPassed,
    },
    {
      id: 'skipped',
      label: '판단 보류',
      hint: '자료가 모자라 판단하지 못한 규칙',
      glyph: '?',
      count: report.rulesSkipped,
    },
  ]
}

const BAR_HEIGHT = 8
const HATCH_SIZE = 6
const HATCH_INK = 2
const PERCENT = 100

/**
 * Thin segmented bar: one whole (all rules) split into the three outcomes.
 * The tiles above it are its legend and carry the numbers.
 */
function OutcomeBar(p: { parts: OutcomeCount[]; total: number }) {
  // useId may contain characters that are not valid inside url(#...).
  const hatchId = 'u-ins-hatch-' + useId().replace(/[^A-Za-z0-9]/g, '')
  const summary =
    `규칙 ${fmtInt(p.total)}개 가운데 ` +
    p.parts.map((part) => `${part.label} ${fmtInt(part.count)}개`).join(', ')
  let start = 0
  return (
    <div className="u-ins-bar">
      <svg role="img" aria-label={summary} width="100%" height={BAR_HEIGHT}>
        <title>{summary}</title>
        <defs>
          <pattern
            id={hatchId}
            width={HATCH_SIZE}
            height={HATCH_SIZE}
            patternUnits="userSpaceOnUse"
            patternTransform="rotate(45)"
          >
            <rect className="u-ins-hatch-ground" width={HATCH_SIZE} height={HATCH_SIZE} />
            <rect className="u-ins-hatch-ink" width={HATCH_INK} height={HATCH_SIZE} />
          </pattern>
        </defs>
        {p.parts.map((part) => {
          if (part.count <= 0) return null
          const width = (part.count / p.total) * PERCENT
          const x = start
          start += width
          return (
            <rect
              key={part.id}
              className={'u-ins-seg u-ins-seg-' + part.id}
              x={x + '%'}
              width={width + '%'}
              height={BAR_HEIGHT}
              fill={part.id === 'skipped' ? `url(#${hatchId})` : undefined}
            >
              <title>
                {part.label} {fmtInt(part.count)}개 ({fmtPct(ratio(part.count, p.total))})
              </title>
            </rect>
          )
        })}
      </svg>
    </div>
  )
}

// A skipped rule was not judged, so it must not read as "looked at and fine".
function leadSentence(report: InsightReport, total: number): string {
  const { rulesFired: fired, rulesPassed: passed, rulesSkipped: skipped } = report
  if (total === 0) return '이 범위에서 판단한 규칙이 없습니다.'
  if (fired > 0)
    return `규칙 ${fmtInt(total)}개 가운데 ${fmtInt(fired)}개에서 개선할 점을 찾았습니다.`
  if (skipped === 0) return `규칙 ${fmtInt(total)}개를 모두 통과했습니다.`
  if (passed === 0) return `규칙 ${fmtInt(total)}개 모두 자료가 모자라 판단하지 못했습니다.`
  return `판단할 수 있었던 규칙 ${fmtInt(passed)}개에서는 개선할 점을 찾지 못했습니다.`
}

/** How many rules fired, passed and could not be judged, and when. */
export function Summary({ report }: { report: InsightReport }) {
  const parts = outcomes(report)
  const total = parts.reduce((sum, part) => sum + part.count, 0)
  const lead = leadSentence(report, total)
  return (
    <Card
      title="규칙 판단 결과"
      hint={lead}
      action={
        <span className="u-ins-generated">분석 시각 {fmtDateTime(report.generatedAt)}</span>
      }
    >
      <ul className="u-ins-counts">
        {parts.map((part) => (
          <li key={part.id} className="u-ins-count">
            <span className="u-ins-count-label">
              <span className={'u-ins-key u-ins-key-' + part.id} aria-hidden="true">
                {part.glyph}
              </span>
              {part.label}
            </span>
            <strong className="u-ins-count-value">
              {fmtInt(part.count)}
              <span className="u-ins-count-unit">개</span>
            </strong>
            <span className="u-hint">{part.hint}</span>
          </li>
        ))}
      </ul>
      {total > 0 && <OutcomeBar parts={parts} total={total} />}
    </Card>
  )
}
