import { useEffect, useState } from 'react'
import { native } from './socket'
import type { Query } from './nav'
import { t } from './i18n'

type Props = {
  sourceId: string
  kind: string
  consent: boolean
  enabled: boolean
  query: Query
  refresh: () => void
}

export function ReceiverSettings({
  sourceId,
  kind,
  consent,
  enabled,
  query,
  refresh,
}: Props) {
  const [available, setAvailable] = useState(false)
  const [notice, setNotice] = useState('')
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const parent = [
    'claude-transcript',
    'codex-rollout',
    'codex-app-server',
  ].includes(kind)
  useEffect(() => {
    let active = true
    setAvailable(false)
    setNotice('')
    setName('')
    if (!parent)
      native()
        .HasReceiver(sourceId)
        .then((value) => {
          if (active) setAvailable(value)
        })
        .catch(() => {})
    return () => {
      active = false
    }
  }, [sourceId, parent])

  async function attach(mode: 'hook' | 'mcp') {
    setBusy(true)
    try {
      const ticket = await native().PickObserver(
        sourceId,
        mode,
        mode === 'mcp' ? name : '',
      )
      if (!ticket) return
      await query('attachSource', {
        input: {
          client: mode === 'mcp' ? 'MCP' : 'CLAUDE',
          pickerToken: ticket,
          retentionDays: 7,
        },
      })
      setNotice(t('trace.receiver.registered'))
      refresh()
    } catch {
      setNotice(t('trace.receiver.attachFailed'))
    } finally {
      setBusy(false)
    }
  }
  if (parent)
    return (
      <section className="panel">
        <h2>{t('trace.receiver.extraSources')}</h2>
        {kind === 'claude-transcript' && (
          <button disabled={busy} onClick={() => attach('hook')}>
            {t('trace.receiver.connectHook')}
          </button>
        )}
        <label>
          {t('trace.receiver.mcpName')}{' '}
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            maxLength={64}
          />
        </label>
        <button disabled={busy || !name.trim()} onClick={() => attach('mcp')}>
          {t('trace.receiver.connectMcp')}
        </button>
        <p role="status">{notice}</p>
      </section>
    )
  if (!available) return null
  async function copy() {
    try {
      await native().CopyReceiverSettings(sourceId)
      setNotice(t('trace.receiver.copied'))
    } catch {
      setNotice(t('trace.receiver.copyFailed'))
    }
  }
  return (
    <section className="panel">
      <h2>{t('trace.receiver.local')}</h2>
      <p>{t('trace.receiver.note')}</p>
      {kind === 'codex-otel' && (
        <p>
          {consent
            ? t('trace.receiver.codexConsentOn')
            : t('trace.receiver.codexConsentOff')}
        </p>
      )}
      {kind === 'claude-otel' && (
        <p>
          {consent
            ? t('trace.receiver.claudeConsentOn')
            : t('trace.receiver.claudeConsentOff')}
        </p>
      )}
      <button disabled={!enabled} onClick={copy}>
        {t('trace.receiver.copy')}
      </button>
      <span role="status">{notice}</span>
    </section>
  )
}
