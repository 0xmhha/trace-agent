import { useState } from 'react'
import type { Evidence, Insight, Recommendation } from '../../types'
import { Pill, fmtInt, fmtPct } from '../../ui'
import {
  evidenceFigure,
  kindLabel,
  knownSeverity,
  severityLabel,
  severityTone,
  shortId,
} from './model'
import { t } from '../../../i18n'

const METER_HEIGHT = 6
const METER_RADIUS = METER_HEIGHT / 2
const PERCENT = 100

/**
 * One number behind an insight. A value out of a total also gets a meter;
 * the text next to it says the same thing, so the meter is hidden from
 * assistive technology instead of being announced twice.
 */
function EvidenceRow({ evidence }: { evidence: Evidence }) {
  const figure = evidenceFigure(evidence)
  return (
    <li className="u-ins-ev">
      <span className="u-ins-ev-label">{evidence.label}</span>
      <span className="u-ins-ev-value">
        {figure.text}
        {figure.share != null && (
          <span className="u-ins-ev-share"> ({fmtPct(figure.share)})</span>
        )}
      </span>
      {figure.share != null && (
        <svg className="u-ins-meter" width="100%" height={METER_HEIGHT} aria-hidden="true">
          <title>
            {figure.text} ({fmtPct(figure.share)})
          </title>
          <rect className="u-ins-meter-track" width="100%" height={METER_HEIGHT} rx={METER_RADIUS} />
          {figure.share > 0 && (
            <rect
              className="u-ins-meter-fill"
              width={figure.share * PERCENT + '%'}
              height={METER_HEIGHT}
              rx={METER_RADIUS}
            />
          )}
        </svg>
      )}
    </li>
  )
}

/** Sessions shown before the rest is folded behind a button. */
const SESSION_PREVIEW = 6

function SessionLinks(p: { ids: string[]; onOpen: (sessionId: string) => void }) {
  const [expanded, setExpanded] = useState(false)
  const shown = expanded ? p.ids : p.ids.slice(0, SESSION_PREVIEW)
  const hidden = p.ids.length - shown.length
  return (
    <div className="u-ins-sessions">
      <span className="u-ins-label">
        {t('insights.card.sessions', { count: p.ids.length, n: fmtInt(p.ids.length) })}
      </span>
      <ul className="u-ins-session-list">
        {shown.map((id) => (
          <li key={id}>
            <button
              type="button"
              className="u-ins-session"
              title={id}
              aria-label={t('insights.card.openSession', { id })}
              onClick={() => p.onOpen(id)}
            >
              {shortId(id)}
              <span aria-hidden="true"> →</span>
            </button>
          </li>
        ))}
        {p.ids.length > SESSION_PREVIEW && (
          <li>
            <button
              type="button"
              className="u-ins-more"
              aria-expanded={expanded}
              onClick={() => setExpanded((open) => !open)}
            >
              {expanded ? t('insights.card.less') : t('insights.card.more', { n: fmtInt(hidden) })}
            </button>
          </li>
        )}
      </ul>
    </div>
  )
}

function RecommendationBox({ recommendation }: { recommendation: Recommendation }) {
  return (
    <div className="u-ins-rec">
      <div className="u-ins-rec-head">
        <span className="u-ins-label">{t('insights.card.recommendation')}</span>
        <span className="u-ins-kind">{kindLabel(recommendation.kind)}</span>
      </div>
      <p className="u-ins-rec-title">{recommendation.title}</p>
      {recommendation.action && <p className="u-ins-rec-action">{recommendation.action}</p>}
      {recommendation.target && (
        <p className="u-ins-rec-target">
          <span className="u-ins-label">{t('insights.card.target')}</span>
          <code>{recommendation.target}</code>
        </p>
      )}
    </div>
  )
}

/** One finding: severity, what was seen, the numbers behind it and what to do. */
export function InsightCard(p: { insight: Insight; onOpenSession: (sessionId: string) => void }) {
  const { insight } = p
  const sessionIds = insight.sessionIds ?? []
  return (
    <article className={'u-card u-ins-item u-ins-sev-' + knownSeverity(insight.severity)}>
      <div className="u-ins-item-head">
        <Pill tone={severityTone(insight.severity)}>{severityLabel(insight.severity)}</Pill>
        <h3>{insight.title}</h3>
      </div>
      {insight.detail && <p className="u-ins-detail">{insight.detail}</p>}
      {insight.evidence.length > 0 && (
        <div>
          <span className="u-ins-label">{t('insights.card.evidence')}</span>
          <ul className="u-ins-evs">
            {insight.evidence.map((evidence, index) => (
              // Labels are not guaranteed unique within one insight.
              <EvidenceRow key={index + ':' + evidence.label} evidence={evidence} />
            ))}
          </ul>
        </div>
      )}
      {sessionIds.length > 0 && <SessionLinks ids={sessionIds} onOpen={p.onOpenSession} />}
      {insight.recommendation && <RecommendationBox recommendation={insight.recommendation} />}
    </article>
  )
}
