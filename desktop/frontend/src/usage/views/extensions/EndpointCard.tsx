import { useId, useState } from 'react'
import type { UsageApi } from '../../api'
import type { ExtEndpoint, Status } from '../../types'
import { Card, ErrorNote } from '../../ui'
import { TOKEN_ENV, URL_ENV, displayUrl, errorText } from './model'

type Notice = { tone: 'good' | 'warn'; text: string }
type Pending = 'on' | 'off' | 'copy' | null

/**
 * Shows whether the local event endpoint is on, lets the user switch it and
 * copies its settings. The credential never reaches this component: copying
 * happens on the native side.
 */
export function EndpointCard(p: {
  api: UsageApi
  /** null while neither the status nor the page data has arrived. */
  endpoint: ExtEndpoint | null
  onStatus: (status: Status) => void
  onChanged: () => void
}) {
  const { api, endpoint, onStatus, onChanged } = p
  const [pending, setPending] = useState<Pending>(null),
    [notice, setNotice] = useState<Notice | null>(null),
    [error, setError] = useState('')
  const labelId = useId()
  const enabled = endpoint?.enabled ?? false
  const url = endpoint ? displayUrl(endpoint.url) : ''

  const toggle = () => {
    const want = !enabled
    setPending(want ? 'on' : 'off')
    setNotice(null)
    setError('')
    api
      .setExtensionEnabled(want)
      .then((next) => {
        onStatus(next)
        onChanged()
        // Turning on goes through a native confirmation. Declining it is not
        // an error: the call resolves with the endpoint still off.
        if (next.extension.enabled === want)
          setNotice({ tone: 'good', text: want ? '수신기를 켰습니다.' : '수신기를 껐습니다.' })
        else
          setNotice({
            tone: 'warn',
            text: want
              ? '수신기를 켜지 않았습니다. 확인 창에서 허용해야 켜집니다.'
              : '수신기가 아직 켜져 있습니다. 잠시 뒤 다시 시도해 주세요.',
          })
      })
      .catch((reason: unknown) =>
        setError(errorText(reason, '수신기 상태를 바꾸지 못했습니다.')),
      )
      .finally(() => setPending(null))
  }

  const copy = () => {
    setPending('copy')
    setNotice(null)
    setError('')
    api
      .copyExtensionSettings()
      .then(() =>
        setNotice({
          tone: 'good',
          text: '설정을 클립보드에 복사했습니다. 토큰이 들어 있으니 붙여 넣는 곳을 확인해 주세요.',
        }),
      )
      .catch((reason: unknown) => setError(errorText(reason, '설정을 복사하지 못했습니다.')))
      .finally(() => setPending(null))
  }

  return (
    <Card title="이벤트 수신기">
      <p className="u-ext-lead">
        플러그인, MCP 서버, 셸 스크립트가 도구 안에서 일어난 일을 이 주소로 보내면 세션과 도구
        호출에 이어서 보여 줍니다. 수신기는 이 Mac 안에서만(127.0.0.1) 듣고, 받은 이벤트를
        밖으로 보내지 않습니다.
      </p>
      <div className="u-ext-endpoint">
        <div className="u-ext-toggle">
          <span id={labelId}>이벤트 받기</span>
          <button
            type="button"
            role="switch"
            className="u-ext-switch"
            aria-checked={enabled}
            aria-labelledby={labelId}
            disabled={endpoint === null || pending !== null}
            onClick={toggle}
          >
            <span className="u-ext-switch-thumb" aria-hidden="true" />
          </button>
          <strong className={enabled ? 'u-tone-good' : 'u-tone-muted'}>
            {endpoint === null ? '확인 중' : enabled ? '켜짐' : '꺼짐'}
          </strong>
        </div>
        <div className="u-ext-url">
          <span className="u-ext-url-label">주소</span>
          {enabled && url ? (
            <code>{url}</code>
          ) : (
            <span className="u-tone-muted">
              {enabled ? '주소를 확인하지 못했습니다.' : '수신기를 켜면 주소가 정해집니다.'}
            </span>
          )}
        </div>
        <button type="button" disabled={!enabled || pending !== null} onClick={copy}>
          설정 복사
        </button>
      </div>
      <p className="u-hint u-ext-endpoint-hint">
        설정 복사는 <code>{URL_ENV}</code> 와 <code>{TOKEN_ENV}</code> 에 넣을 주소와 토큰을
        클립보드에 넣습니다. 토큰은 이 화면에 표시하지 않습니다.
        {!enabled && endpoint !== null && ' 수신기를 켠 뒤에 복사할 수 있습니다.'}
      </p>
      {pending === 'on' && (
        <p className="u-ext-feedback" role="status">
          확인 창에서 허용하면 수신기가 켜집니다.
        </p>
      )}
      {notice && (
        <p className={'u-ext-feedback u-tone-' + notice.tone} role="status">
          {notice.text}
        </p>
      )}
      <ErrorNote error={error} />
    </Card>
  )
}
