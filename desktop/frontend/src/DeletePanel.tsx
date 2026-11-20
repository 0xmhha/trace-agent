import { useEffect, useState } from 'react'
import { list, obj, str, type Range } from './data'
import type { Query } from './nav'

type Target = 'SOURCE' | 'SESSION' | 'TASK'
type Kind = 'BODY' | 'ALL'
type Preview = {
  receipt: string
  evidence: number
  bodies: number
  expires: number
}
type Props = {
  sourceId: string
  interval: Range
  query: Query
  run: (action: () => Promise<void>) => Promise<void>
  refresh: () => void
}
const validID = (id: string) => /^[0-9a-f]{64}$/.test(id)
function count(value: unknown): number {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < 0)
    throw Error('삭제 건수를 확인하지 못했습니다. 다시 미리보기를 실행하세요.')
  return value
}
export function DeletePanel({
  sourceId,
  interval,
  query,
  run,
  refresh,
}: Props) {
  const [target, setTarget] = useState<Target>('SOURCE')
  const [kind, setKind] = useState<Kind>('BODY')
  const [id, setId] = useState('')
  const [ids, setIds] = useState<string[]>([])
  const [more, setMore] = useState(false)
  const [preview, setPreview] = useState<Preview | null>(null)
  const [notice, setNotice] = useState('')
  const eligible =
    validID(sourceId) &&
    (target === 'SOURCE' || (validID(id) && ids.includes(id)))
  const targetLabel =
    target === 'SESSION' ? '세션' : target === 'TASK' ? '작업' : '출처 전체'
  const clear = () => {
    setPreview(null)
    setNotice('')
  }

  useEffect(() => {
    if (!preview) return
    const timer = window.setTimeout(
      () => {
        setPreview(null)
        setNotice(
          '미리보기가 만료됐습니다. 삭제하려면 다시 미리보기를 실행하세요.',
        )
      },
      Math.max(0, preview.expires - Date.now()),
    )
    return () => window.clearTimeout(timer)
  }, [preview])

  async function loadTargets(mode: Target, append: boolean) {
    if (mode === 'SOURCE') return
    const result = await query('deleteTargets', {
      sourceId,
      interval,
      target: mode,
      ...(append ? { after: ids.at(-1) } : {}),
    })
    const items = list(result.ids)
    if (
      !Array.isArray(result.ids) ||
      items.length !== result.ids.length ||
      items.some((v) => !validID(v))
    )
      throw Error('삭제 대상 목록을 확인하지 못했습니다.')
    setIds(append ? [...new Set([...ids, ...items])] : items)
    setMore(result.hasNext === true)
    if (!items.length && !append)
      setNotice(
        '이 기간에 확인된 ' +
          (mode === 'SESSION' ? '세션' : '작업') +
          '이 없습니다.',
      )
  }

  async function requestPreview() {
    clear()
    if (!eligible) return
    const selector =
      target === 'SOURCE'
        ? {}
        : target === 'SESSION'
          ? { sessionId: id }
          : { taskId: id }
    const result = await query('previewDelete', {
      input: { sourceId, interval, kind, ...selector },
    })
    const scope = obj(result.scope)
    const receipt = str(result.receipt),
      expires = Date.parse(str(result.expiresAt))
    if (
      scope.sourceId !== sourceId ||
      scope.kind !== kind ||
      (scope.sessionId ?? '') !== (target === 'SESSION' ? id : '') ||
      (scope.taskId ?? '') !== (target === 'TASK' ? id : '') ||
      Date.parse(str(scope.from)) !== Date.parse(interval.from) ||
      Date.parse(str(scope.until)) !== Date.parse(interval.until) ||
      !receipt ||
      !Number.isFinite(expires) ||
      expires <= Date.now()
    )
      throw Error(
        '서버가 확인한 범위나 유효 시간이 다릅니다. 다시 미리보기를 실행하세요.',
      )
    setPreview({
      receipt,
      expires,
      evidence: count(result.evidenceCount),
      bodies: count(result.bodyCount),
    })
  }
  async function confirm() {
    if (!preview || !eligible) return
    setPreview(null)
    if (preview.expires <= Date.now()) throw Error('미리보기가 만료됐습니다.')
    const result = await query('confirmDelete', { receipt: preview.receipt })
    setNotice(
      '근거 ' +
        count(result.evidenceRemoved) +
        '건, 본문 ' +
        count(result.bodiesRemoved) +
        '건을 삭제했습니다.',
    )
    refresh()
  }
  return (
    <section className="panel">
      <h2>삭제</h2>
      <p>
        삭제 기간 (UTC) <time>{new Date(interval.from).toISOString()}</time>{' '}
        이상, <time>{new Date(interval.until).toISOString()}</time> 미만
      </p>
      <div className="toolbar">
        <label>
          삭제 대상{' '}
          <select
            value={target}
            onChange={(event) => {
              const next = event.target.value
              if (next !== 'SOURCE' && next !== 'SESSION' && next !== 'TASK')
                return
              clear()
              setId('')
              setIds([])
              setMore(false)
              setTarget(next)
              if (next !== 'SOURCE') void run(() => loadTargets(next, false))
            }}
          >
            <option value="SOURCE">출처 전체</option>
            <option value="SESSION">세션</option>
            <option value="TASK">작업</option>
          </select>
        </label>
        {target !== 'SOURCE' && (
          <label>
            {targetLabel} ID{' '}
            <select
              value={id}
              onChange={(event) => {
                clear()
                setId(event.target.value)
              }}
            >
              <option value="">선택하세요</option>
              {ids.map((value) => (
                <option key={value} value={value}>
                  {value}
                </option>
              ))}
            </select>
          </label>
        )}
        {more && (
          <button onClick={() => run(() => loadTargets(target, true))}>
            대상 더 보기
          </button>
        )}
        <label>
          삭제할 자료{' '}
          <select
            value={kind}
            onChange={(event) => {
              const next = event.target.value
              if (next === 'ALL' || next === 'BODY') {
                clear()
                setKind(next)
              }
            }}
          >
            <option value="BODY">본문만</option>
            <option value="ALL">모든 근거</option>
          </select>
        </label>
        <button disabled={!eligible} onClick={() => run(requestPreview)}>
          삭제 미리보기
        </button>
      </div>
      {preview && (
        <div className="notice">
          <p>
            출처 <code>{sourceId}</code>
            {target !== 'SOURCE' && (
              <>
                {' '}
                · {targetLabel} <code>{id}</code>
              </>
            )}
          </p>
          <p>
            범위에 포함된 근거 {preview.evidence}건. 삭제할 근거{' '}
            {kind === 'BODY' ? 0 : preview.evidence}건, 본문 {preview.bodies}건.
          </p>
          <p>
            {target !== 'SOURCE' &&
              '이 출처에서 소속을 확인할 수 없는 기록도 집계에 포함합니다. '}
            {kind === 'BODY'
              ? '삭제한 범위의 본문 재수집도 차단합니다.'
              : '삭제한 범위의 근거 재수집도 차단합니다.'}
          </p>
          <button className="danger" onClick={() => run(confirm)}>
            이 범위 삭제
          </button>
          <button onClick={clear}>취소</button>
        </div>
      )}
      {notice && <p role="status">{notice}</p>}
    </section>
  )
}
