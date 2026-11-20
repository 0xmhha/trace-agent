import { useEffect, useRef, useState } from 'react'
import { native } from './socket'
import { ReceiverSettings } from './ReceiverSettings'
import { DeletePanel } from './DeletePanel'
import {
  Badge,
  EvidenceLinks,
  Fields,
  Group,
  Items,
  evidenceRows,
  type Props,
} from './Screen'
import {
  code,
  date,
  num,
  obj,
  rows,
  short,
  str,
  stamp as parseStamp,
  type Fixed,
  type Row,
} from './data'
import type { Go } from './nav'
export function SourcePanel({
  screen: s,
  go,
  query,
  connected,
  refresh,
  filter,
  stamp,
}: Props) {
  const [kind, setKind] = useState('codex-app-server'),
    [retention, setDays] = useState(7),
    [modelPage, setModelPage] = useState<{
      scope: Fixed
      items: Row[]
      detail?: Row
    } | null>(null),
    [err, setErr] = useState(''),
    [busy, setBusy] = useState(false)
  const source = obj(s.source),
    id = str(source.id),
    alive = useRef(true)
  useEffect(() => {
    alive.current = true
    return () => {
      alive.current = false
    }
  }, [])
  async function run(fn: () => Promise<void>) {
    if (busy || !connected) return
    setBusy(true)
    setErr('')
    try {
      await fn()
    } catch (e) {
      if (alive.current)
        setErr(e instanceof Error ? e.message : '요청에 실패했습니다.')
    } finally {
      if (alive.current) setBusy(false)
    }
  }
  const fixed = {
    filter: { ...filter, interval: stamp.effectiveInterval },
    point: stamp.point,
    cohort: stamp.cohort,
  }
  const page =
    connected &&
    modelPage?.scope.point.revision === stamp.point.revision &&
    modelPage?.scope.point.epoch === stamp.point.epoch
      ? modelPage
      : null
  const detail = page?.detail
  const openModelEvidence: Go = (selection) => {
    if (page) go(selection, { scope: page.scope })
  }
  async function sourceScope() {
    const stamp = parseStamp(
      await query('sourceScope', { scope: fixed, sourceId: id }),
    )
    return {
      filter: { interval: fixed.filter.interval, sources: [id] },
      point: stamp.point,
      cohort: stamp.cohort,
    }
  }
  return (
    <>
      <fieldset disabled={busy || !connected}>
        {!id ? (
          <>
            <Group title="출처 연결">
              <div className="toolbar">
                <label>
                  자료원{' '}
                  <select
                    value={kind}
                    onChange={(e) => setKind(e.target.value)}
                  >
                    <option value="codex-app-server">
                      Codex · 프로젝트 이력
                    </option>
                    <option value="codex-otlp">Codex · 로컬 OTel 수신기</option>
                    <option value="codex-rollout">Codex · rollout JSONL</option>
                    <option value="claude-project">
                      Claude Code · 세션 폴더
                    </option>
                    <option value="claude-transcript">
                      Claude Code · 세션 JSONL
                    </option>
                    <option value="claude-otlp">
                      Claude Code · 로컬 OTel 수신기
                    </option>
                  </select>
                </label>
                <button
                  className="primary"
                  onClick={() =>
                    run(async () => {
                      const token = await native().Pick(kind)
                      if (!token) return
                      await query('attachSource', {
                        input: {
                          client: kind.startsWith('codex') ? 'CODEX' : 'CLAUDE',
                          pickerToken: token,
                          retentionDays: 7,
                        },
                      })
                      refresh()
                    })
                  }
                >
                  출처 선택
                </button>
              </div>
            </Group>
            <Group title="연결한 출처">
              <Items
                items={rows(s.items)}
                render={(r) => (
                  <>
                    <button
                      onClick={() =>
                        go(
                          { kind: 'SOURCE', sourceId: str(r.id) },
                          { live: true },
                        )
                      }
                    >
                      {code(r.client)} · {str(r.kind)}
                    </button>
                    <Badge value={r.asOfHealth} />
                    <small>{date(r.lastAt)}</small>
                  </>
                )}
              />
            </Group>
          </>
        ) : (
          <>
            <Group title={code(source.client) + ' · ' + str(source.kind)}>
              <dl>
                <div>
                  <dt>클라이언트</dt>
                  <dd>
                    {code(source.client)}{' '}
                    {str(source.clientVersion) || '버전 미확인'}
                  </dd>
                </div>
                <div>
                  <dt>어댑터</dt>
                  <dd>{str(source.adapterVersion)}</dd>
                </div>
                <div>
                  <dt>마지막 기록</dt>
                  <dd>{date(source.lastAt)}</dd>
                </div>
                <div>
                  <dt>연결 점검</dt>
                  <dd>
                    <Badge value={source.asOfHealth} />{' '}
                    {date(source.asOfCheckedAt)}
                  </dd>
                </div>
              </dl>
              <Items
                items={rows(s.capabilities)}
                render={(c) => (
                  <>
                    <b>{str(c.code)}</b>
                    <Badge value={c.state} />
                    <small>{str(c.reason)}</small>
                  </>
                )}
              />
              <Items
                items={rows(s.gaps)}
                empty="보고된 수집 공백이 없습니다."
                render={(g) => (
                  <span>
                    {date(g.from)} – {date(g.until)} {str(g.reason)}
                  </span>
                )}
              />
              <button
                onClick={() =>
                  run(async () => {
                    const scope = await sourceScope()
                    if (alive.current)
                      go({ kind: 'UNLINKED', sourceId: id }, { scope })
                  })
                }
              >
                미연계 근거 {num(s.unlinkedCount)}건
              </button>
            </Group>
            <Group title="모델 요청">
              <button
                onClick={() =>
                  run(async () => {
                    const scope = await sourceScope()
                    const r = await query('sourceModelAttempts', {
                      scope,
                      sourceId: id,
                    })
                    if (alive.current)
                      setModelPage({ scope, items: rows(obj(r.data).items) })
                  })
                }
              >
                출처 범위 모델 시도 조회
              </button>
              <Items
                items={page?.items ?? []}
                render={(m) => (
                  <button
                    onClick={() =>
                      run(async () => {
                        if (!page) return
                        const r = await query('sourceModelAttempt', {
                          scope: page.scope,
                          sourceId: id,
                          id: m.id,
                        })
                        if (alive.current)
                          setModelPage({ ...page, detail: obj(r.data) })
                      })
                    }
                  >
                    {short(m.id)} · {code(m.phase)}
                  </button>
                )}
              />
            </Group>
            {detail && (
              <>
                <Group title="모델 요청 본문">
                  <Fields value={detail.request} />
                </Group>
                <Group title="모델 응답 본문">
                  <Fields value={detail.response} />
                </Group>
                {rows(detail.conversation).length > 0 && (
                  <Group title="모델 대화">
                    <Fields value={detail.conversation} />
                  </Group>
                )}
                <Group title="모델 전달 과정">
                  <Items
                    items={rows(detail.edges)}
                    render={(e) => (
                      <>
                        <time>{date(e.at)}</time>
                        <b>
                          {code(e.actor)} · {code(e.action)}
                        </b>
                        <span>
                          {code(e.direction)} · {code(e.observer)}
                        </span>
                        <Badge value={e.grade} />
                        <EvidenceLinks
                          ids={e.evidenceIds}
                          go={openModelEvidence}
                        />
                      </>
                    )}
                  />
                </Group>
                <Group title="모델 근거 기록">
                  {evidenceRows(detail.evidence, openModelEvidence)}
                </Group>
              </>
            )}
            <ReceiverSettings
              sourceId={id}
              kind={str(source.kind)}
              consent={s.consent === true}
              enabled={s.enabled === true}
              query={query}
              refresh={refresh}
            />
            <Group title="수집 설정">
              <div className="toolbar">
                <button
                  onClick={() =>
                    run(async () => {
                      await query('setSourceEnabled', {
                        sourceId: id,
                        enabled: !s.enabled,
                      })
                      refresh()
                    })
                  }
                >
                  {s.enabled ? '수집 중지' : '수집 재개'}
                </button>
                <button
                  onClick={() =>
                    run(async () => {
                      await query('setConsent', {
                        sourceId: id,
                        enabled: !s.consent,
                      })
                      refresh()
                    })
                  }
                >
                  {s.consent ? '본문 동의 철회' : '본문 수집 동의'}
                </button>
                <label>
                  보존 {num(s.retentionDays)}일 →{' '}
                  <input
                    type="number"
                    min="1"
                    max="3650"
                    value={retention}
                    onChange={(e) => setDays(Number(e.target.value))}
                  />
                  일
                </label>
                <button
                  disabled={retention < 1 || retention > 3650}
                  onClick={() =>
                    run(async () => {
                      await query('setRetention', {
                        sourceId: id,
                        days: retention,
                      })
                      refresh()
                    })
                  }
                >
                  보존 기간 저장
                </button>
              </div>
            </Group>
            {[
              'claude-transcript',
              'codex-app-server',
              'codex-rollout',
            ].includes(str(source.kind)) && (
              <Group title="이력 범위">
                <p>
                  {date(s.historyFrom)} – {date(s.historyUntil)}
                </p>
                <button
                  onClick={() =>
                    run(async () => {
                      const pickerToken = await native().PickHistory(id)
                      if (pickerToken) {
                        await query('setHistory', {
                          sourceId: id,
                          pickerToken,
                          interval: fixed.filter.interval,
                        })
                        refresh()
                      }
                    })
                  }
                >
                  같은 출처를 선택해 현재 기간 적용
                </button>
              </Group>
            )}
            <DeletePanel
              key={JSON.stringify([
                id,
                fixed.filter.interval.from,
                fixed.filter.interval.until,
                stamp.point.epoch,
                connected,
              ])}
              sourceId={id}
              interval={fixed.filter.interval}
              query={query}
              run={run}
              refresh={refresh}
            />
          </>
        )}
      </fieldset>
      {err && (
        <div className="notice error" role="alert">
          {err}
        </div>
      )}
    </>
  )
}
