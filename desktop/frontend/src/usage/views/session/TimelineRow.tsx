import type { ReactNode } from 'react'
import type { ExtEvent, TimelineItem } from '../../types'
import { Pill, fmtInt, fmtMs } from '../../ui'
import { ExtAttributes } from './ExtAttributes'
import {
  categoryLabels,
  extKindLabels,
  extLevelLabels,
  extStatusLabels,
  extStatusTones,
  fmtClock,
  fmtFull,
  hookStatusLabels,
  hookStatusTones,
  kindLabels,
  labelOf,
  tokenParts,
  toneOf,
} from './format'

/** True for the rows the "오류만" toggle keeps. */
export function isErrorItem(item: TimelineItem): boolean {
  if (item.tool?.isError) return true
  if (item.hook && (item.hook.status === 'error' || item.hook.status === 'blocked')) return true
  if (item.ext && (item.ext.status === 'error' || item.ext.level === 'error')) return true
  return false
}

/**
 * Transcript text. It is always a plain text node: nothing in it is parsed
 * as markup or turned into a link.
 */
function TextBlock(p: { text: string; mono?: boolean }) {
  return <pre className={'u-sd-text' + (p.mono ? ' u-sd-mono' : '')}>{p.text}</pre>
}

function Fold(p: { summary: string; open?: boolean; children: ReactNode }) {
  return (
    <details className="u-sd-fold" open={p.open}>
      <summary>{p.summary}</summary>
      {p.children}
    </details>
  )
}

/** Markers every kind may carry: cut text and masked values. */
function Flags({ item }: { item: TimelineItem }) {
  if (!item.truncated && !item.masked) return null
  return (
    <p className="u-sd-flags">
      {item.truncated && (
        <Pill tone="warn" title="원문이 길어 앞부분만 보여 줍니다.">
          잘림
        </Pill>
      )}
      {!!item.masked && (
        <Pill tone="muted" title="비밀 값으로 보이는 문자열을 가렸습니다.">
          가림 {fmtInt(item.masked)}곳
        </Pill>
      )}
    </p>
  )
}

function ExtBody({ ext }: { ext: ExtEvent }) {
  return (
    <>
      {ext.message && <TextBlock text={ext.message} />}
      {ext.value != null && (
        <p className="u-sd-line">
          값 <b className="u-sd-num">{ext.value.toLocaleString('ko-KR')}</b>
          {ext.unit ? ' ' + ext.unit : ''}
        </p>
      )}
      <ExtAttributes attributes={ext.attributes} />
      <p className="u-sd-line u-sd-dim">
        {ext.toolUseId ? (
          <>
            연결된 도구 호출 <code className="u-sd-name">{ext.toolUseId}</code>
          </>
        ) : (
          '도구 호출에 연결되지 않은 이벤트입니다.'
        )}
      </p>
    </>
  )
}

interface Parts {
  /** Shown right after the kind label. */
  title: ReactNode
  body: ReactNode
}

function partsOf(item: TimelineItem): Parts {
  const { tool, hook, ext } = item
  switch (item.kind) {
    case 'thinking':
      return {
        title: null,
        body: (
          <p className="u-sd-line">
            생각한 내용은 보여 주지 않습니다. 생각 토큰{' '}
            <b className="u-sd-num">{fmtInt(item.tokens?.output ?? 0)}개</b>
          </p>
        ),
      }
    case 'tool_call':
      return {
        title: tool && (
          <>
            <code className="u-sd-name">{tool.name}</code>
            <Pill tone="muted">{labelOf(categoryLabels, tool.category)}</Pill>
          </>
        ),
        body: tool?.input ? (
          <Fold summary="입력 보기">
            <TextBlock text={tool.input} mono />
          </Fold>
        ) : (
          <p className="u-sd-line u-sd-dim">입력이 없습니다.</p>
        ),
      }
    case 'tool_result':
      return {
        title: tool && (
          <>
            <code className="u-sd-name">{tool.name}</code>
            <Pill tone="muted">{labelOf(categoryLabels, tool.category)}</Pill>
            <Pill tone={tool.isError ? 'bad' : 'good'}>{tool.isError ? '오류' : '성공'}</Pill>
            {tool.durationMs != null && <span className="u-sd-num">{fmtMs(tool.durationMs)}</span>}
          </>
        ),
        body: item.text ? (
          <Fold summary={`결과 보기 (${fmtInt(item.text.length)}자)`} open={!!tool?.isError}>
            <TextBlock text={item.text} mono />
          </Fold>
        ) : (
          <p className="u-sd-line u-sd-dim">결과 글이 없습니다.</p>
        ),
      }
    case 'hook':
      return {
        title: hook && (
          <>
            <span>{hook.event}</span>
            <code className="u-sd-name">{hook.name}</code>
            <Pill tone={toneOf(hookStatusTones, hook.status)}>
              {labelOf(hookStatusLabels, hook.status)}
            </Pill>
            <span className="u-sd-num">종료 코드 {hook.exitCode}</span>
            <span className="u-sd-num">{fmtMs(hook.durationMs)}</span>
          </>
        ),
        body: item.text ? <TextBlock text={item.text} mono /> : null,
      }
    case 'ext':
      return {
        title: ext && (
          <>
            <Pill tone="muted">{ext.source}</Pill>
            <code className="u-sd-name">{ext.name}</code>
            <span>{labelOf(extKindLabels, ext.kind)}</span>
            <Pill tone={toneOf(extStatusTones, ext.status)}>
              {labelOf(extStatusLabels, ext.status)}
            </Pill>
            <span>수준 {labelOf(extLevelLabels, ext.level)}</span>
            {ext.durationMs != null && <span className="u-sd-num">{fmtMs(ext.durationMs)}</span>}
          </>
        ),
        body: ext ? <ExtBody ext={ext} /> : null,
      }
    case 'system':
      return { title: null, body: item.text ? <TextBlock text={item.text} /> : null }
    default:
      // prompt, assistant and any kind added later: show the text as it is.
      return { title: null, body: item.text ? <TextBlock text={item.text} /> : null }
  }
}

/**
 * One timeline item. agentId is the stream being shown, so only foreign rows
 * name theirs. fullTime adds the date for lists that span more than a session.
 */
export function TimelineRow({
  item,
  agentId,
  fullTime,
}: {
  item: TimelineItem
  agentId: string
  fullTime?: boolean
}) {
  const { title, body } = partsOf(item)
  const failed = isErrorItem(item)
  // A thinking row already states its only number in the body.
  const tokens = item.kind === 'thinking' ? undefined : item.tokens
  return (
    <li className={`u-sd-item u-sd-k-${item.kind}` + (failed ? ' u-sd-failed' : '')}>
      <div className="u-sd-item-head">
        <span className="u-sd-kind">{labelOf(kindLabels, item.kind)}</span>
        {title}
        {item.label && <span className="u-sd-label">{item.label}</span>}
        {item.agentId && item.agentId !== agentId && (
          <span className="u-sd-dim">에이전트 {item.agentId}</span>
        )}
        <span className="u-sd-grow" />
        {item.model && <Pill tone="muted">{item.model}</Pill>}
        <time dateTime={item.at} title={fmtFull(item.at)}>
          {fullTime ? fmtFull(item.at) : fmtClock(item.at)}
        </time>
      </div>
      {tokens && <p className="u-sd-tokens">{tokenParts(tokens)}</p>}
      {body}
      <Flags item={item} />
    </li>
  )
}
