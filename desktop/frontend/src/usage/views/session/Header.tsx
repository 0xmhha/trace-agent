import { t } from '../../../i18n'
import type { SessionDetail } from '../../types'
import { Pill, Stat, fmtCompact, fmtCost, fmtDateTime, fmtMs, fmtPct, ratio, sumTokens } from '../../ui'
import { countVars, elapsedMs, fmtItems, fmtTimes, tokenParts } from './format'

export function BackButton({ onBack }: { onBack: () => void }) {
  return (
    <button className="u-sd-back" onClick={onBack}>
      <span aria-hidden="true">←</span> {t('session.back')}
    </button>
  )
}

function Meta(p: { label: string; value: string; title?: string }) {
  return (
    <li>
      <span className="u-sd-meta-key">{p.label}</span>
      <span className="u-sd-meta-value" title={p.title}>
        {p.value || t('session.unknown')}
      </span>
    </li>
  )
}

/** Title, identity and headline numbers of one session. */
export function Header({ detail, onBack }: { detail: SessionDetail; onBack: () => void }) {
  const { session } = detail
  const elapsed = elapsedMs(session.startedAt, session.lastAt)
  const failureRate = ratio(session.toolErrors, session.toolCalls)
  const span = { from: fmtDateTime(session.startedAt), until: fmtDateTime(session.lastAt) }
  return (
    <>
      <div className="u-sd-head">
        <BackButton onBack={onBack} />
        <h2 className="u-sd-title">{session.title || t('session.untitled')}</h2>
        <ul className="u-sd-meta">
          <Meta label={t('session.metaProject')} value={session.projectPath || session.projectKey} />
          <Meta label={t('session.metaBranch')} value={session.gitBranch} />
          <Meta label={t('session.metaEntrypoint')} value={session.entrypoint} />
          <Meta
            label={t('session.metaTime')}
            value={
              elapsed == null
                ? t('session.timeRange', span)
                : t('session.timeRangeElapsed', { ...span, elapsed: fmtMs(elapsed) })
            }
          />
          <Meta label={t('session.metaSessionId')} value={session.id} />
        </ul>
        <div className="u-sd-models">
          <span className="u-sd-meta-key">{t('session.models')}</span>
          {detail.models.length ? (
            detail.models.map((model) => (
              <Pill key={model.model} title={tokenParts(model.tokens)}>
                {t('session.modelPill', { ...countVars(model.calls), model: model.model })}
              </Pill>
            ))
          ) : session.models.length ? (
            session.models.map((model) => <Pill key={model}>{model}</Pill>)
          ) : (
            <span className="u-sd-dim">{t('session.noRecord')}</span>
          )}
        </div>
      </div>
      <div className="u-sd-stats">
        <Stat label={t('session.statPrompts')} value={fmtTimes(session.prompts)} />
        <Stat label={t('session.statModelCalls')} value={fmtTimes(session.modelCalls)} />
        <Stat
          label={t('session.statTokens')}
          value={fmtCompact(sumTokens(session.tokens))}
          hint={tokenParts(session.tokens)}
        />
        <Stat
          label={t('session.statCost')}
          value={fmtCost(session.costUsd)}
          tone={session.costUsd == null ? 'muted' : 'plain'}
          hint={session.costUsd == null ? t('session.costUnrecorded') : t('session.costRecorded')}
        />
        <Stat label={t('session.statToolCalls')} value={fmtTimes(session.toolCalls)} />
        <Stat
          label={t('session.statToolFailures')}
          value={fmtTimes(session.toolErrors)}
          tone={session.toolErrors > 0 ? 'bad' : 'plain'}
          hint={
            failureRate == null
              ? t('session.noToolCalls')
              : t('session.failureRate', { pct: fmtPct(failureRate) })
          }
        />
        <Stat label={t('session.statSubagents')} value={fmtItems(session.subagents)} />
        <Stat label={t('session.statCompactions')} value={fmtTimes(session.compactions)} />
      </div>
    </>
  )
}
