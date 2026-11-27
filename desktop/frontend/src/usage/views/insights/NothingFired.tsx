import type { InsightReport } from '../../types'
import { Card, Empty, Pill, fmtInt } from '../../ui'
import { t } from '../../../i18n'

/**
 * Shown when no rule fired. "Every rule passed" and "some rules could not be
 * judged yet" are different answers, and neither is the same as "still
 * reading" or "collection is off", which Empty words from the coverage.
 */
export function NothingFired({ report }: { report: InsightReport }) {
  const { coverage, rulesPassed: passed, rulesSkipped: skipped } = report
  const title = t('insights.nothing.title')
  if (coverage.state === 'scanning' || coverage.state === 'disabled')
    return (
      <Card title={title}>
        <Empty coverage={coverage} />
      </Card>
    )
  // Unread files or skipped lines may hide something a rule would have caught.
  const caveat = coverage.state === 'partial' ? t('insights.nothing.caveat') : ''
  const sentences = (...parts: string[]) => [...parts, caveat].filter(Boolean).join(' ')
  if (skipped > 0)
    return (
      <Card title={title} action={<Pill tone="warn">{t('insights.nothing.pendingPill')}</Pill>}>
        <Empty>
          {sentences(
            passed > 0
              ? t('insights.nothing.passedSome', { count: passed, n: fmtInt(passed) })
              : '',
            t('insights.nothing.skipped', { count: skipped, n: fmtInt(skipped) }),
          )}
        </Empty>
      </Card>
    )
  if (passed > 0)
    return (
      <Card title={title} action={<Pill tone="good">{t('insights.nothing.allPassedPill')}</Pill>}>
        <Empty>
          {sentences(t('insights.nothing.allPassed', { count: passed, n: fmtInt(passed) }))}
        </Empty>
      </Card>
    )
  return (
    <Card title={title}>
      <Empty>{sentences(t('insights.noneJudged'))}</Empty>
    </Card>
  )
}
