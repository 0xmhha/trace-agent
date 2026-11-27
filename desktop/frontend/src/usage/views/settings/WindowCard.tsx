import { useState } from 'react'
import { Card, fmtDay } from '../../ui'
import { ACTION, type CardProps } from './actions'
import { Feedback } from './parts'
import { t } from '../../../i18n'

/** Collection windows offered, in days. The widest is usage.MaxWindowDays. */
const WINDOW_OPTIONS: readonly number[] = [7, 30, 90, 365]

const SELECT_ID = 'u-set-window-days'

/** Collection window: how far back transcripts are read. */
export function WindowCard({ api, status, actions, apply }: CardProps) {
  // The stored value stays the truth; the choice is shown only while its
  // request is in flight, so a declined confirmation falls back by itself.
  const [chosen, setChosen] = useState<number | null>(null)
  const busy = actions.pending?.id === ACTION.window
  const current = status.windowDays
  // A window set elsewhere (the command line, an older build) still shows.
  const options = WINDOW_OPTIONS.includes(current)
    ? WINDOW_OPTIONS
    : [...WINDOW_OPTIONS, current].sort((a, b) => a - b)

  const change = (days: number) => {
    if (days === current) return
    setChosen(days)
    actions.run('window', ACTION.window, async () => {
      try {
        const next = await api.setWindowDays(days)
        apply(next)
        if (next.windowDays !== days)
          return t('settings.window.unchanged')
        return days > current
          ? t('settings.window.widened', { count: days })
          : t('settings.window.narrowed', { count: days })
      } finally {
        setChosen(null)
      }
    })
  }

  const windowFrom = fmtDay(status.coverage.windowFrom)
  return (
    <Card title={t('settings.window.title')} hint={t('settings.window.hint')} className="u-span-6">
      <div className="u-set-field">
        <label htmlFor={SELECT_ID}>{t('settings.window.label')}</label>
        <select
          id={SELECT_ID}
          value={busy && chosen !== null ? chosen : current}
          disabled={actions.pending !== null}
          aria-busy={busy || undefined}
          onChange={(e) => change(Number(e.target.value))}
        >
          {options.map((days) => (
            <option key={days} value={days}>
              {t('settings.window.option', { count: days })}
            </option>
          ))}
        </select>
        {busy && (
          <span className="u-hint" role="status">
            {t('settings.window.busy')}
          </span>
        )}
      </div>
      {windowFrom && (
        <p className="u-set-line">{t('settings.window.from', { from: windowFrom })}</p>
      )}
      <Feedback actions={actions} scope="window" />
      <p className="u-hint">{t('settings.window.note')}</p>
    </Card>
  )
}
