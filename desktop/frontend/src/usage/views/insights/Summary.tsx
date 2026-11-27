import { useId } from 'react'
import type { InsightReport } from '../../types'
import { Card, fmtDateTime, fmtInt, fmtPct, ratio } from '../../ui'
import { t } from '../../../i18n'

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
      label: t('insights.outcome.fired'),
      hint: t('insights.outcome.fired.hint'),
      glyph: '!',
      count: report.rulesFired,
    },
    {
      id: 'passed',
      label: t('insights.outcome.passed'),
      hint: t('insights.outcome.passed.hint'),
      glyph: '✓',
      count: report.rulesPassed,
    },
    {
      id: 'skipped',
      label: t('insights.outcome.skipped'),
      hint: t('insights.outcome.skipped.hint'),
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
  const summary = t('insights.bar.summary', {
    total: fmtInt(p.total),
    parts: p.parts
      .map((part) => t('insights.bar.part', { label: part.label, n: fmtInt(part.count) }))
      .join(', '),
  })
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
                {t('insights.bar.segment', {
                  label: part.label,
                  n: fmtInt(part.count),
                  share: fmtPct(ratio(part.count, p.total)),
                })}
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
  if (total === 0) return t('insights.noneJudged')
  if (fired > 0)
    return t('insights.lead.fired', { total: fmtInt(total), fired: fmtInt(fired) })
  if (skipped === 0)
    return t('insights.lead.allPassed', { count: total, total: fmtInt(total) })
  if (passed === 0)
    return t('insights.lead.allSkipped', { count: total, total: fmtInt(total) })
  return t('insights.lead.passedRest', { count: passed, passed: fmtInt(passed) })
}

/** How many rules fired, passed and could not be judged, and when. */
export function Summary({ report }: { report: InsightReport }) {
  const parts = outcomes(report)
  const total = parts.reduce((sum, part) => sum + part.count, 0)
  const lead = leadSentence(report, total)
  return (
    <Card
      title={t('insights.summary.title')}
      hint={lead}
      action={
        <span className="u-ins-generated">
          {t('insights.summary.generated', { at: fmtDateTime(report.generatedAt) })}
        </span>
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
              <span className="u-ins-count-unit">
                {t('insights.summary.unit', { count: part.count })}
              </span>
            </strong>
            <span className="u-hint">{part.hint}</span>
          </li>
        ))}
      </ul>
      {total > 0 && <OutcomeBar parts={parts} total={total} />}
    </Card>
  )
}
