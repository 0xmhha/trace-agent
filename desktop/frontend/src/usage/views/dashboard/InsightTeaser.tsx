import { t, type MessageKey } from '../../../i18n'
import type { InsightReport, Severity } from '../../types'
import { Card, Empty, ErrorNote, Pill, type Loaded, type Tone } from '../../ui'
import { UNIT, withUnit } from './format'

/** How many insights the dashboard previews. The insights view lists all of them. */
const TEASER_COUNT = 3

/** Severities from most to least severe, with the label and tone of their pill. */
const SEVERITIES: readonly { id: Severity; label: MessageKey; tone: Tone }[] = [
  { id: 'critical', label: 'dashboard.severityCritical', tone: 'bad' },
  { id: 'warn', label: 'dashboard.severityWarn', tone: 'warn' },
  { id: 'info', label: 'dashboard.severityInfo', tone: 'muted' },
]

function rank(severity: Severity): number {
  const index = SEVERITIES.findIndex((s) => s.id === severity)
  return index < 0 ? SEVERITIES.length : index
}

function Body({ report }: { report: InsightReport }) {
  const judged = report.rulesFired + report.rulesPassed
  const counts = {
    checked: withUnit(judged, UNIT.items),
    fired: withUnit(report.rulesFired, UNIT.items),
    skipped: withUnit(report.rulesSkipped, UNIT.items),
  }
  if (!report.items.length) {
    // Before the scan finished, "nothing fired" would be a false all-clear.
    const state = report.coverage.state
    if (state === 'scanning' || state === 'disabled') return <Empty coverage={report.coverage} />
    return <p className="u-dash-clear">{t('dashboard.insightsClear', counts)}</p>
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
              <Pill tone={severity?.tone ?? 'muted'}>
                {severity ? t(severity.label) : item.severity}
              </Pill>
              <span>{item.title}</span>
            </li>
          )
        })}
      </ul>
      <p className="u-hint">{t('dashboard.insightsSummary', counts)}</p>
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
      title={t('dashboard.insightsTitle')}
      hint={t('dashboard.insightsHint', { count: TEASER_COUNT })}
      className={p.className}
      action={
        <button type="button" className="u-dash-link" onClick={p.onOpen}>
          {t('dashboard.viewAll')}
        </button>
      }
    >
      <ErrorNote error={error} retry={reload} />
      {data ? (
        <Body report={data} />
      ) : (
        loading && (
          <p className="u-empty" role="status">
            {t('dashboard.insightsLoading')}
          </p>
        )
      )}
    </Card>
  )
}
