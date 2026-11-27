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
  interleave,
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
import { errorText, hasMessage, t, type MessageKey } from './i18n'

// Source kinds the capability catalog groups together, because the server
// words some reasons the same way for every kind of the group
// (internal/collect/registry.go: OTLP and Observer).
const OTEL_KINDS = ['claude-otel', 'codex-otel']
const OBSERVER_KINDS = ['claude-hook', 'mcp-observer']
const CLIENT_VERSION_CODE = 'client_version'

// The source kinds offered when connecting a source, with their labels.
const SOURCE_CHOICES = [
  { kind: 'codex-app-server', label: 'trace.source.codexAppServer' },
  { kind: 'codex-otlp', label: 'trace.source.codexOtlp' },
  { kind: 'codex-rollout', label: 'trace.source.codexRollout' },
  { kind: 'claude-project', label: 'trace.source.claudeProject' },
  { kind: 'claude-transcript', label: 'trace.source.claudeTranscript' },
  { kind: 'claude-otlp', label: 'trace.source.claudeOtlp' },
] as const satisfies readonly { kind: string; label: MessageKey }[]

/**
 * Reason text of one capability in the page's language. The catalog is keyed
 * by code and state, narrowed by source kind, kind group or client version
 * where the server words them differently; the most specific key wins. When
 * no key matches, the server's own reason is shown.
 */
function capabilityReason(c: Row, source: Row): string {
  const code = str(c.code),
    state = str(c.state),
    kind = str(source.kind)
  const group = OTEL_KINDS.includes(kind)
    ? 'otel'
    : OBSERVER_KINDS.includes(kind)
      ? 'observer'
      : ''
  const variants =
    code === CLIENT_VERSION_CODE
      ? [str(source.clientVersion) ? 'different' : 'missing']
      : [kind, group]
  const keys = [
    ...variants.filter(Boolean).map((v) => `capabilities.${code}.${state}.${v}`),
    `capabilities.${code}.${state}`,
  ]
  const key = keys.find(hasMessage)
  return key ? t(key as MessageKey) : str(c.reason)
}
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
        setErr(errorText(e, 'trace.source.requestFailed'))
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
            <Group title={t('trace.source.connect')}>
              <div className="toolbar">
                <label>
                  {t('trace.source.type')}{' '}
                  <select
                    value={kind}
                    onChange={(e) => setKind(e.target.value)}
                  >
                    {SOURCE_CHOICES.map((c) => (
                      <option key={c.kind} value={c.kind}>
                        {t(c.label)}
                      </option>
                    ))}
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
                  {t('trace.source.pick')}
                </button>
              </div>
            </Group>
            <Group title={t('trace.source.connected')}>
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
                  <dt>{t('trace.source.client')}</dt>
                  <dd>
                    {code(source.client)}{' '}
                    {str(source.clientVersion) || t('trace.source.versionUnknown')}
                  </dd>
                </div>
                <div>
                  <dt>{t('trace.source.adapter')}</dt>
                  <dd>{str(source.adapterVersion)}</dd>
                </div>
                <div>
                  <dt>{t('trace.source.lastRecord')}</dt>
                  <dd>{date(source.lastAt)}</dd>
                </div>
                <div>
                  <dt>{t('trace.source.health')}</dt>
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
                    <small>{capabilityReason(c, source)}</small>
                  </>
                )}
              />
              <Items
                items={rows(s.gaps)}
                empty={t('trace.source.noGaps')}
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
                {t('trace.source.unlinked', { count: num(s.unlinkedCount) })}
              </button>
            </Group>
            <Group title={t('trace.source.modelRequests')}>
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
                {t('trace.source.queryModelAttempts')}
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
                <Group title={t('trace.source.modelRequestBody')}>
                  <Fields value={detail.request} />
                </Group>
                <Group title={t('trace.source.modelResponseBody')}>
                  <Fields value={detail.response} />
                </Group>
                {rows(detail.conversation).length > 0 && (
                  <Group title={t('trace.source.modelConversation')}>
                    <Fields value={detail.conversation} />
                  </Group>
                )}
                <Group title={t('trace.source.modelDelivery')}>
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
                <Group title={t('trace.source.modelEvidence')}>
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
            <Group title={t('trace.source.collection')}>
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
                  {s.enabled ? t('trace.source.pause') : t('trace.source.resume')}
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
                  {s.consent
                    ? t('trace.source.revokeConsent')
                    : t('trace.source.grantConsent')}
                </button>
                <label>
                  {interleave(t('trace.source.retention'), {
                    days: num(s.retentionDays),
                    input: (
                      <input
                        type="number"
                        min="1"
                        max="3650"
                        value={retention}
                        onChange={(e) => setDays(Number(e.target.value))}
                      />
                    ),
                  })}
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
                  {t('trace.source.saveRetention')}
                </button>
              </div>
            </Group>
            {[
              'claude-transcript',
              'codex-app-server',
              'codex-rollout',
            ].includes(str(source.kind)) && (
              <Group title={t('trace.source.historyRange')}>
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
                  {t('trace.source.applyHistory')}
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
