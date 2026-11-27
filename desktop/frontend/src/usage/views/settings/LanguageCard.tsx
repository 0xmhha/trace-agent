import { Card } from '../../ui'
import { LanguageSelect, t } from '../../../i18n'

const SELECT_ID = 'u-set-language'

/** Language of the interface. The choice is kept in this Mac's web storage. */
export function LanguageCard() {
  return (
    <Card title={t('settings.language.title')} className="u-span-6">
      <div className="u-set-field">
        <label htmlFor={SELECT_ID}>{t('settings.language.label')}</label>
        <LanguageSelect id={SELECT_ID} />
      </div>
      <p className="u-hint">{t('settings.language.hint')}</p>
    </Card>
  )
}
