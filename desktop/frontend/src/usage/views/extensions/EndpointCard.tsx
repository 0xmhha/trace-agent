import { useId, useState } from 'react'
import type { UsageApi } from '../../api'
import type { ExtEndpoint, Status } from '../../types'
import { Card, ErrorNote } from '../../ui'
import { TOKEN_ENV, URL_ENV, displayUrl } from './model'
import { withCode } from './rich'
import { errorText, t } from '../../../i18n'

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
          setNotice({
            tone: 'good',
            text: want ? t('extensions.endpoint.turnedOn') : t('extensions.endpoint.turnedOff'),
          })
        else
          setNotice({
            tone: 'warn',
            text: want ? t('extensions.endpoint.notOn') : t('extensions.endpoint.stillOn'),
          })
      })
      .catch((reason: unknown) =>
        setError(errorText(reason, 'extensions.endpoint.toggleFailed')),
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
          text: t('extensions.endpoint.copied'),
        }),
      )
      .catch((reason: unknown) => setError(errorText(reason, 'extensions.endpoint.copyFailed')))
      .finally(() => setPending(null))
  }

  return (
    <Card title={t('extensions.endpoint.title')}>
      <p className="u-ext-lead">{t('extensions.endpoint.lead')}</p>
      <div className="u-ext-endpoint">
        <div className="u-ext-toggle">
          <span id={labelId}>{t('extensions.endpoint.receive')}</span>
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
            {endpoint === null
              ? t('extensions.endpoint.checking')
              : enabled
                ? t('extensions.endpoint.on')
                : t('extensions.endpoint.off')}
          </strong>
        </div>
        <div className="u-ext-url">
          <span className="u-ext-url-label">{t('extensions.endpoint.address')}</span>
          {enabled && url ? (
            <code>{url}</code>
          ) : (
            <span className="u-tone-muted">
              {enabled
                ? t('extensions.endpoint.urlUnknown')
                : t('extensions.endpoint.urlWhenOn')}
            </span>
          )}
        </div>
        <button type="button" disabled={!enabled || pending !== null} onClick={copy}>
          {t('extensions.endpoint.copy')}
        </button>
      </div>
      <p className="u-hint u-ext-endpoint-hint">
        {withCode(t('extensions.endpoint.copyHint'), { url: URL_ENV, token: TOKEN_ENV })}
        {!enabled && endpoint !== null && ' ' + t('extensions.endpoint.copyAfterOn')}
      </p>
      {pending === 'on' && (
        <p className="u-ext-feedback" role="status">
          {t('extensions.endpoint.confirmPending')}
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
