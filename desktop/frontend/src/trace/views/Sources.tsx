import { SourcePanel } from '../../SourcePanel'
import { t } from '../../i18n'
import type { Props } from './shared'
/** Source list and source detail: the panel tells them apart itself. */
export function Sources(p: Props) {
  return (
    <>
      <SourcePanel {...p} />
      <button onClick={() => p.go({ kind: 'IDENTITIES' })}>
        {t('trace.identities.list')}
      </button>
    </>
  )
}
