import { useEffect, useState } from 'react'
import { list, obj, str, type Range } from './data'
import type { Query } from './nav'
import { interleave } from './Screen'
import { t } from './i18n'

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
    throw Error(t('trace.delete.badCount'))
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
    target === 'SESSION'
      ? t('trace.delete.targetSession')
      : target === 'TASK'
        ? t('trace.delete.targetTask')
        : t('trace.delete.targetSource')
  const clear = () => {
    setPreview(null)
    setNotice('')
  }

  useEffect(() => {
    if (!preview) return
    const timer = window.setTimeout(
      () => {
        setPreview(null)
        setNotice(t('trace.delete.expiredRetry'))
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
      throw Error(t('trace.delete.badTargets'))
    setIds(append ? [...new Set([...ids, ...items])] : items)
    setMore(result.hasNext === true)
    if (!items.length && !append)
      setNotice(
        mode === 'SESSION'
          ? t('trace.delete.noSessions')
          : t('trace.delete.noTasks'),
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
      throw Error(t('trace.delete.scopeMismatch'))
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
    if (preview.expires <= Date.now()) throw Error(t('trace.delete.expired'))
    const result = await query('confirmDelete', { receipt: preview.receipt })
    setNotice(
      t('trace.delete.done', {
        evidence: count(result.evidenceRemoved),
        bodies: count(result.bodiesRemoved),
      }),
    )
    refresh()
  }
  return (
    <section className="panel">
      <h2>{t('trace.delete.title')}</h2>
      <p>
        {interleave(t('trace.delete.period'), {
          from: <time>{new Date(interval.from).toISOString()}</time>,
          until: <time>{new Date(interval.until).toISOString()}</time>,
        })}
      </p>
      <div className="toolbar">
        <label>
          {t('trace.delete.target')}{' '}
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
            <option value="SOURCE">{t('trace.delete.targetSource')}</option>
            <option value="SESSION">{t('trace.delete.targetSession')}</option>
            <option value="TASK">{t('trace.delete.targetTask')}</option>
          </select>
        </label>
        {target !== 'SOURCE' && (
          <label>
            {t('trace.delete.targetId', { target: targetLabel })}{' '}
            <select
              value={id}
              onChange={(event) => {
                clear()
                setId(event.target.value)
              }}
            >
              <option value="">{t('trace.delete.choose')}</option>
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
            {t('trace.delete.moreTargets')}
          </button>
        )}
        <label>
          {t('trace.delete.kind')}{' '}
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
            <option value="BODY">{t('trace.delete.bodyOnly')}</option>
            <option value="ALL">{t('trace.delete.allEvidence')}</option>
          </select>
        </label>
        <button disabled={!eligible} onClick={() => run(requestPreview)}>
          {t('trace.delete.preview')}
        </button>
      </div>
      {preview && (
        <div className="notice">
          <p>
            {t('trace.delete.source')} <code>{sourceId}</code>
            {target !== 'SOURCE' && (
              <>
                {' '}
                · {targetLabel} <code>{id}</code>
              </>
            )}
          </p>
          <p>
            {t('trace.delete.counts', {
              inScope: preview.evidence,
              evidence: kind === 'BODY' ? 0 : preview.evidence,
              bodies: preview.bodies,
            })}
          </p>
          <p>
            {target !== 'SOURCE' && t('trace.delete.unattributed') + ' '}
            {kind === 'BODY'
              ? t('trace.delete.blockBodies')
              : t('trace.delete.blockEvidence')}
          </p>
          <button className="danger" onClick={() => run(confirm)}>
            {t('trace.delete.confirm')}
          </button>
          <button onClick={clear}>{t('trace.delete.cancel')}</button>
        </div>
      )}
      {notice && <p role="status">{notice}</p>}
    </section>
  )
}
