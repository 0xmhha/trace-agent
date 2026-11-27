import type { SessionDetail } from '../../types'
import { Pill, Stat, fmtCompact, fmtCost, fmtDateTime, fmtMs, fmtPct, ratio, sumTokens } from '../../ui'
import { elapsedMs, fmtCount, tokenParts } from './format'

export function BackButton({ onBack }: { onBack: () => void }) {
  return (
    <button className="u-sd-back" onClick={onBack}>
      <span aria-hidden="true">←</span> 세션 목록
    </button>
  )
}

function Meta(p: { label: string; value: string; title?: string }) {
  return (
    <li>
      <span className="u-sd-meta-key">{p.label}</span>
      <span className="u-sd-meta-value" title={p.title}>
        {p.value || '미상'}
      </span>
    </li>
  )
}

/** Title, identity and headline numbers of one session. */
export function Header({ detail, onBack }: { detail: SessionDetail; onBack: () => void }) {
  const { session } = detail
  const elapsed = elapsedMs(session.startedAt, session.lastAt)
  const failureRate = ratio(session.toolErrors, session.toolCalls)
  return (
    <>
      <div className="u-sd-head">
        <BackButton onBack={onBack} />
        <h2 className="u-sd-title">{session.title || '제목 없는 세션'}</h2>
        <ul className="u-sd-meta">
          <Meta label="프로젝트" value={session.projectPath || session.projectKey} />
          <Meta label="브랜치" value={session.gitBranch} />
          <Meta label="진입점" value={session.entrypoint} />
          <Meta
            label="시간"
            value={
              `${fmtDateTime(session.startedAt)} ~ ${fmtDateTime(session.lastAt)}` +
              (elapsed == null ? '' : ` (${fmtMs(elapsed)})`)
            }
          />
          <Meta label="세션 ID" value={session.id} />
        </ul>
        <div className="u-sd-models">
          <span className="u-sd-meta-key">모델</span>
          {detail.models.length ? (
            detail.models.map((model) => (
              <Pill key={model.model} title={tokenParts(model.tokens)}>
                {model.model} · {fmtCount(model.calls, '회')}
              </Pill>
            ))
          ) : session.models.length ? (
            session.models.map((model) => <Pill key={model}>{model}</Pill>)
          ) : (
            <span className="u-sd-dim">기록 없음</span>
          )}
        </div>
      </div>
      <div className="u-sd-stats">
        <Stat label="프롬프트" value={fmtCount(session.prompts, '회')} />
        <Stat label="모델 호출" value={fmtCount(session.modelCalls, '회')} />
        <Stat label="토큰" value={fmtCompact(sumTokens(session.tokens))} hint={tokenParts(session.tokens)} />
        <Stat
          label="비용"
          value={fmtCost(session.costUsd)}
          tone={session.costUsd == null ? 'muted' : 'plain'}
          hint={
            session.costUsd == null
              ? '클라이언트가 비용을 기록하지 않았습니다. 추정하지 않습니다.'
              : '클라이언트가 기록한 값입니다.'
          }
        />
        <Stat label="도구 호출" value={fmtCount(session.toolCalls, '회')} />
        <Stat
          label="도구 실패"
          value={fmtCount(session.toolErrors, '회')}
          tone={session.toolErrors > 0 ? 'bad' : 'plain'}
          hint={failureRate == null ? '도구 호출이 없습니다.' : `실패율 ${fmtPct(failureRate)}`}
        />
        <Stat label="하위 에이전트" value={fmtCount(session.subagents, '개')} />
        <Stat label="맥락 압축" value={fmtCount(session.compactions, '회')} />
      </div>
    </>
  )
}
