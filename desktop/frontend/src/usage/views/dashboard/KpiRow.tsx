import type { Overview } from '../../types'
import { Stat, fmtCompact, fmtCost, fmtInt, fmtPct, ratio, sumTokens } from '../../ui'
import { DAY_MS, UNIT, withUnit } from './format'
import { isCacheHitLow, toolFailureTone } from './tones'

/** Headline numbers of the range. Two tiles lead to the view that explains them. */
export function KpiRow(p: { overview: Overview; onSessions: () => void; onTools: () => void }) {
  const o = p.overview
  const inputSide = o.tokens.input + o.tokens.cacheRead + o.tokens.cacheCreate
  const failureRate = ratio(o.toolErrors, o.toolCalls)
  const rangeDays = Math.max(
    1,
    Math.round((Date.parse(o.range.until) - Date.parse(o.range.from)) / DAY_MS),
  )
  return (
    <div className="u-dash-kpis">
      <Stat
        label="세션"
        value={withUnit(o.sessions, UNIT.items)}
        hint="세션 목록 보기"
        onClick={p.onSessions}
      />
      <Stat
        label="프롬프트"
        value={withUnit(o.prompts, UNIT.times)}
        hint={
          o.sessions > 0 ? `세션당 평균 ${(o.prompts / o.sessions).toFixed(1)}${UNIT.times}` : undefined
        }
      />
      <Stat
        label="토큰 합계"
        value={fmtCompact(sumTokens(o.tokens))}
        hint={`입력 ${fmtCompact(inputSide)} · 출력 ${fmtCompact(o.tokens.output)}`}
      />
      <Stat
        label="캐시 적중률"
        value={fmtPct(o.cacheHitRatio)}
        hint="입력 중 캐시에서 읽은 비율"
        tone={o.cacheHitRatio == null ? 'muted' : isCacheHitLow(o.cacheHitRatio) ? 'warn' : 'plain'}
      />
      <Stat
        label="도구 호출"
        value={withUnit(o.toolCalls, UNIT.times)}
        hint={
          failureRate == null
            ? '호출 없음'
            : `실패 ${withUnit(o.toolErrors, UNIT.times)} (${fmtPct(failureRate)})`
        }
        tone={toolFailureTone(failureRate)}
        onClick={p.onTools}
      />
      <Stat
        label="비용"
        value={fmtCost(o.costUsd)}
        hint={`세션 ${fmtInt(o.sessions)}개 중 ${fmtInt(o.costSessions)}개가 보고`}
        tone={o.costUsd == null ? 'muted' : 'plain'}
      />
      <Stat
        label="활동 일수"
        value={withUnit(o.activeDays, UNIT.days)}
        hint={`기간 ${withUnit(rangeDays, UNIT.days)} 중`}
      />
      <Stat label="하위 에이전트" value={withUnit(o.subagents, UNIT.items)} hint="실행한 수" />
      <Stat
        label="맥락 압축"
        value={withUnit(o.compactions, UNIT.times)}
        hint="대화를 줄여 이어 간 횟수"
      />
    </div>
  )
}
