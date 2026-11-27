import type { ExtEndpoint } from '../../types'
import { Card, Pill } from '../../ui'
import { Fact, Facts } from './parts'
import { t } from '../../../i18n'

/** Extension receiver: state only. Its switch and setup live on the extension events page. */
export function ReceiverCard({ endpoint, open }: { endpoint: ExtEndpoint; open: () => void }) {
  return (
    <Card
      title={t('settings.receiver.title')}
      hint={t('settings.receiver.hint')}
      className="u-span-6"
    >
      <Facts>
        <Fact label={t('settings.state')}>
          <Pill tone={endpoint.enabled ? 'good' : 'muted'}>
            {endpoint.enabled ? t('settings.on') : t('settings.off')}
          </Pill>
        </Fact>
        {endpoint.enabled && endpoint.url && (
          <Fact label={t('settings.receiver.address')}>
            <code className="u-set-path">{endpoint.url}</code>
          </Fact>
        )}
      </Facts>
      <p className="u-set-line">{t('settings.receiver.line')}</p>
      <button type="button" onClick={open}>
        {t('settings.receiver.open')}
      </button>
    </Card>
  )
}
