import { useMemo } from 'react'
import type { UsageApi } from '../../api'
import type { AgentRow, SourceState, TimelineKind, TimelinePage } from '../../types'
import { Card, Empty, ErrorNote, fmtInt } from '../../ui'
import { describeError } from './errors'
import { ROOT_AGENT_LABEL, agentLabel, kindLabels, kindOrder } from './format'
import type { SessionView } from './route'
import { TimelineRow, isErrorItem } from './TimelineRow'
import { usePaged } from './usePaged'

const sourceEmpty: Record<Exclude<SourceState, 'available'>, string> = {
  missing: '원본 세션 기록 파일이 없어 대화를 보여 줄 수 없습니다.',
  unreadable: '원본 세션 기록 파일을 읽지 못해 대화를 보여 줄 수 없습니다.',
}

/** The 대화 tab: one transcript stream as a paged, filterable timeline. */
export function Conversation(p: {
  api: UsageApi
  sessionId: string
  agents: AgentRow[]
  view: SessionView
  onView: (view: SessionView) => void
  refresh: number
}) {
  const { api, sessionId, agents, view, onView } = p
  const { agentId, hidden, errorsOnly } = view
  const paged = usePaged<TimelinePage>(
    (cursor) => api.timeline({ sessionId, agentId: agentId || undefined, cursor }),
    [api, sessionId, agentId],
    p.refresh,
    'tail',
  )
  const { items, skipped, counts } = useMemo(() => {
    const all = paged.pages.flatMap((page) => page.items)
    const perKind = new Map<TimelineKind, number>()
    for (const item of all) perKind.set(item.kind, (perKind.get(item.kind) ?? 0) + 1)
    return {
      items: all,
      skipped: paged.pages.reduce((sum, page) => sum + page.skipped, 0),
      counts: perKind,
    }
  }, [paged.pages])
  const source: SourceState = paged.pages.at(-1)?.source ?? 'available'

  // The position in the loaded list is part of the key, so a row keeps its
  // open or closed state when a filter hides its neighbours.
  const shown = items
    .map((item, index) => ({ item, index }))
    .filter(({ item }) => !hidden.has(item.kind) && (!errorsOnly || isErrorItem(item)))
  const filtered = hidden.size > 0 || errorsOnly
  const hasItems = items.length > 0
  const known = agents.some((agent) => agent.agentId === agentId)
  const subagents = agents.filter((agent) => agent.agentId)

  const toggleKind = (kind: TimelineKind) => {
    const next = new Set(hidden)
    if (!next.delete(kind)) next.add(kind)
    onView({ ...view, hidden: next })
  }

  return (
    <Card>
      <div className="u-sd-controls">
        <label className="u-sd-control">
          에이전트
          <select value={agentId} onChange={(e) => onView({ ...view, agentId: e.target.value })}>
            <option value="">{ROOT_AGENT_LABEL}</option>
            {subagents.map((agent) => (
              <option key={agent.agentId} value={agent.agentId}>
                {agentLabel(agent)}
              </option>
            ))}
            {agentId && !known && <option value={agentId}>알 수 없는 에이전트 · {agentId}</option>}
          </select>
        </label>
        {hasItems && (
          <label className="u-sd-check">
            <input
              type="checkbox"
              checked={errorsOnly}
              onChange={(e) => onView({ ...view, errorsOnly: e.target.checked })}
            />
            오류만 보기
          </label>
        )}
        {hasItems && filtered && (
          <button
            className="u-sd-small"
            onClick={() => onView({ ...view, hidden: new Set(), errorsOnly: false })}
          >
            필터 지우기
          </button>
        )}
      </div>
      {/* Filters and the masking notice only matter once there is text to show. */}
      {hasItems && (
        <>
          <fieldset className="u-sd-kinds">
            <legend>항목 종류</legend>
            {kindOrder.map((kind) => (
              <label key={kind} className="u-sd-check">
                <input type="checkbox" checked={!hidden.has(kind)} onChange={() => toggleKind(kind)} />
                {kindLabels[kind]}
                <span className="u-count">{fmtInt(counts.get(kind) ?? 0)}</span>
              </label>
            ))}
          </fieldset>
          <p className="u-note u-sd-mask-note">
            비밀 값으로 보이는 문자열은 가려서 보여 줍니다. 가리기는 보조 장치라서 모든 비밀 값을
            잡지는 못합니다. 이메일 주소, IP 주소, 호스트 이름은 가리지 않습니다.
          </p>
        </>
      )}
      {skipped > 0 && (
        <p className="u-note u-note-warn" role="status">
          읽은 구간에서 해석하지 못해 건너뛴 줄이 {fmtInt(skipped)}개 있습니다. 그 줄의 내용은 아래
          목록에 없습니다.
        </p>
      )}
      <ErrorNote error={describeError(paged.error)} retry={paged.retry} />

      {paged.loading && !hasItems ? (
        <p className="u-empty" role="status">
          대화를 읽는 중입니다.
        </p>
      ) : !hasItems ? (
        source !== 'available' ? (
          <Empty>{sourceEmpty[source]}</Empty>
        ) : (
          !paged.error && <Empty>이 기록에는 보여 줄 항목이 없습니다.</Empty>
        )
      ) : !shown.length ? (
        <Empty>
          불러온 {fmtInt(items.length)}개 항목 중 필터에 맞는 것이 없습니다.
          {paged.nextCursor ? ' 더 보기로 뒤쪽 기록을 불러올 수 있습니다.' : ''}
        </Empty>
      ) : (
        <ol className="u-sd-timeline" aria-label="대화 타임라인">
          {shown.map(({ item, index }) => (
            <TimelineRow key={index + ':' + item.id} item={item} agentId={agentId} />
          ))}
        </ol>
      )}

      {hasItems && (
        <div className="u-sd-more">
          <span className="u-hint" role="status">
            {paged.loadingMore
              ? '뒤쪽 기록을 읽는 중입니다.'
              : filtered
                ? `불러온 ${fmtInt(items.length)}개 중 ${fmtInt(shown.length)}개를 보여 줍니다.`
                : `${fmtInt(items.length)}개 항목을 불러왔습니다.`}
            {!paged.loadingMore && !paged.nextCursor && ' 기록의 끝입니다.'}
          </span>
          {paged.nextCursor && (
            <button onClick={paged.more} disabled={paged.loadingMore}>
              더 보기
            </button>
          )}
        </div>
      )}
    </Card>
  )
}
