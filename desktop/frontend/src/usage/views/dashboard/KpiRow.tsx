import { formatNumber, t } from '../../../i18n'
import type { Overview } from '../../types'
import { Stat, fmtCompact, fmtCost, fmtInt, fmtPct, ratio, sumTokens } from '../../ui'
import { DAY_MS, UNIT, withUnit } from './format'
import { isCacheHitLow, toolFailureTone } from './tones'

const AVERAGE_DIGITS: Intl.NumberFormatOptions = { minimumFractionDigits: 1, maximumFractionDigits: 1 }

/** Headline numbers of the range. Two tiles lead to the view that explains them. */
export function KpiRow(p: { overview: Overview; onSessions: () => void; onTools: () => void }) {
  const o = p.overview
  const inputSide = o.tokens.input + o.tokens.cacheRead + o.tokens.cacheCreate
  const failureRate = ratio(o.toolErrors, o.toolCalls)
  const rangeDays = Math.max(
    1,
    Math.round((Date.parse(o.range.until) - Date.parse(o.range.from)) / DAY_MS),
  )
  const average = o.sessions > 0 ? o.prompts / o.sessions : 0
  return (
    <div className="u-dash-kpis">
      <Stat
        label={t('dashboard.kpiSessions')}
        value={withUnit(o.sessions, UNIT.items)}
        hint={t('dashboard.kpiSessionsHint')}
        onClick={p.onSessions}
      />
      <Stat
        label={t('dashboard.kpiPrompts')}
        value={withUnit(o.prompts, UNIT.times)}
        hint={
          o.sessions > 0
            ? t('dashboard.kpiPromptsHint', {
                average: withUnit(average, UNIT.times, formatNumber(average, AVERAGE_DIGITS)),
              })
            : undefined
        }
      />
      <Stat
        label={t('dashboard.kpiTokens')}
        value={fmtCompact(sumTokens(o.tokens))}
        hint={t('dashboard.kpiTokensHint', {
          input: fmtCompact(inputSide),
          output: fmtCompact(o.tokens.output),
        })}
      />
      <Stat
        label={t('dashboard.cacheTitle')}
        value={fmtPct(o.cacheHitRatio)}
        hint={t('dashboard.kpiCacheHint')}
        tone={o.cacheHitRatio == null ? 'muted' : isCacheHitLow(o.cacheHitRatio) ? 'warn' : 'plain'}
      />
      <Stat
        label={t('dashboard.kpiToolCalls')}
        value={withUnit(o.toolCalls, UNIT.times)}
        hint={
          failureRate == null
            ? t('dashboard.kpiNoCalls')
            : t('dashboard.kpiToolsHint', {
                failed: withUnit(o.toolErrors, UNIT.times),
                rate: fmtPct(failureRate),
              })
        }
        tone={toolFailureTone(failureRate)}
        onClick={p.onTools}
      />
      <Stat
        label={t('dashboard.kpiCost')}
        value={fmtCost(o.costUsd)}
        hint={t('dashboard.kpiCostHint', {
          count: o.sessions,
          sessions: fmtInt(o.sessions),
          reported: fmtInt(o.costSessions),
        })}
        tone={o.costUsd == null ? 'muted' : 'plain'}
      />
      <Stat
        label={t('dashboard.kpiActiveDays')}
        value={withUnit(o.activeDays, UNIT.days)}
        hint={t('dashboard.kpiActiveDaysHint', { days: withUnit(rangeDays, UNIT.days) })}
      />
      <Stat
        label={t('dashboard.kpiSubagents')}
        value={withUnit(o.subagents, UNIT.items)}
        hint={t('dashboard.kpiSubagentsHint')}
      />
      <Stat
        label={t('dashboard.kpiCompactions')}
        value={withUnit(o.compactions, UNIT.times)}
        hint={t('dashboard.kpiCompactionsHint')}
      />
    </div>
  )
}
