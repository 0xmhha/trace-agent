import { useEffect, useState } from 'react'
import { native } from './socket'
import type { Query } from './nav'

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
      setNotice(
        '수신기를 등록했습니다. 출처 목록에서 수신기를 열어 송신 설정을 복사하세요.',
      )
      refresh()
    } catch {
      setNotice(
        '수신기를 연결하지 못했습니다. 출처와 서버 이름을 확인해 주세요.',
      )
    } finally {
      setBusy(false)
    }
  }
  if (parent)
    return (
      <section className="panel">
        <h2>추가 관찰 출처</h2>
        {kind === 'claude-transcript' && (
          <button disabled={busy} onClick={() => attach('hook')}>
            Claude 도구 훅 연결
          </button>
        )}
        <label>
          MCP 서버 이름{' '}
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            maxLength={64}
          />
        </label>
        <button disabled={busy || !name.trim()} onClick={() => attach('mcp')}>
          MCP 수신기 연결
        </button>
        <p role="status">{notice}</p>
      </section>
    )
  if (!available) return null
  async function copy() {
    try {
      await native().CopyReceiverSettings(sourceId)
      setNotice('설정을 복사했습니다. 송신기를 시작할 터미널에 붙여 넣으세요.')
    } catch {
      setNotice('수집 상태와 키체인 권한을 확인해 주세요.')
    }
  }
  return (
    <section className="panel">
      <h2>로컬 수신 설정</h2>
      <p>
        이 Mac에서 새로 시작하는 송신기에 적용합니다. 인증값은 실행할 때
        키체인에서 읽습니다.
      </p>
      {kind === 'codex-otel' && (
        <p>
          {consent
            ? '복사할 설정에 사용자 입력 수집을 포함합니다.'
            : '복사할 설정에서 사용자 입력 수집을 끕니다.'}
        </p>
      )}
      {kind === 'claude-otel' && (
        <p>
          {consent
            ? '복사할 설정에 API 본문 수집을 포함합니다.'
            : '복사할 설정에서 API 본문 수집을 끕니다.'}
        </p>
      )}
      <button disabled={!enabled} onClick={copy}>
        터미널 설정 복사
      </button>
      <span role="status">{notice}</span>
    </section>
  )
}
