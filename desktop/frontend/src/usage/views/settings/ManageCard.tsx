import { useEffect, useRef, useState } from 'react'
import { Card } from '../../ui'
import { ACTION, type CardProps } from './actions'
import { Feedback } from './parts'
import { t } from '../../../i18n'

const CONFIRM_TITLE_ID = 'u-set-clear-title'

/** Manage: read everything again, or delete everything that was stored. */
export function ManageCard({ api, status, actions, apply }: CardProps) {
  const [confirming, setConfirming] = useState(false)
  const openButton = useRef<HTMLButtonElement>(null)
  const cancelButton = useRef<HTMLButtonElement>(null)
  const locked = actions.pending !== null
  // Focus follows the confirmation: onto the safe choice when it opens and
  // back to the button that opened it when it closes. That button is disabled
  // while a request runs, so the return waits until it can take focus.
  const wasConfirming = useRef(false)
  const returnFocus = useRef(false)
  useEffect(() => {
    if (confirming && !wasConfirming.current) cancelButton.current?.focus()
    if (!confirming && wasConfirming.current) returnFocus.current = true
    wasConfirming.current = confirming
    if (returnFocus.current && !confirming && !locked) {
      returnFocus.current = false
      openButton.current?.focus()
    }
  }, [confirming, locked])

  const rescanning = actions.pending?.id === ACTION.rescan
  const clearing = actions.pending?.id === ACTION.clear

  const rescan = () =>
    actions.run('manage', ACTION.rescan, async () => {
      apply(await api.rescan())
      return t('settings.manage.rescan.done')
    })

  const clear = () =>
    actions.run('manage', ACTION.clear, async () => {
      try {
        const next = await api.clearData()
        apply(next)
        // A declined native confirmation returns the Status unchanged, and so
        // does clearing an already empty store with collection off.
        const changed =
          next.consent !== status.consent || next.dataVersion !== status.dataVersion
        if (!changed) return t('settings.manage.unchanged')
        const cleared = t('settings.manage.cleared')
        return next.consent ? cleared : cleared + ' ' + t('settings.manage.collectionOff')
      } finally {
        setConfirming(false)
      }
    })

  return (
    <Card title={t('settings.manage.title')} className="u-span-6">
      <Feedback actions={actions} scope="manage" />
      <div className="u-set-task">
        <div>
          <h3>{t('settings.manage.rescan')}</h3>
          <p className="u-hint">
            {t('settings.manage.rescan.note')}
            {!status.consent && ' ' + t('settings.manage.rescan.off')}
          </p>
        </div>
        <button
          type="button"
          onClick={rescan}
          disabled={locked || !status.consent}
          aria-busy={rescanning || undefined}
        >
          {rescanning ? t('settings.manage.rescan.busy') : t('settings.manage.rescan')}
        </button>
      </div>
      <div className="u-set-task">
        <div>
          <h3>{t('settings.manage.clear')}</h3>
          <p className="u-hint">{t('settings.manage.clear.note')}</p>
        </div>
        <button
          type="button"
          ref={openButton}
          className="u-set-danger"
          onClick={() => setConfirming(true)}
          disabled={locked || confirming}
          aria-expanded={confirming}
        >
          {t('settings.manage.clear')}
        </button>
      </div>
      {confirming && (
        <div className="u-set-confirm" role="group" aria-labelledby={CONFIRM_TITLE_ID}>
          <p id={CONFIRM_TITLE_ID} className="u-set-confirm-title">
            {t('settings.manage.confirm.title')}
          </p>
          <ul>
            <li>{t('settings.manage.confirm.numbers')}</li>
            <li>{t('settings.manage.confirm.stop')}</li>
            <li>{t('settings.manage.confirm.events')}</li>
          </ul>
          <p className="u-hint">{t('settings.manage.confirm.hint')}</p>
          <div className="u-set-confirm-actions">
            <button
              type="button"
              ref={cancelButton}
              onClick={() => setConfirming(false)}
              disabled={clearing}
            >
              {t('settings.manage.cancel')}
            </button>
            <button
              type="button"
              className="u-set-danger-solid"
              onClick={clear}
              disabled={locked}
              aria-busy={clearing || undefined}
            >
              {clearing ? t('settings.manage.clearing') : t('settings.manage.confirm.button')}
            </button>
          </div>
        </div>
      )}
    </Card>
  )
}
