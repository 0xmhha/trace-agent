import type { Status } from '../../types'
import { Card, Pill, fmtDateTime, fmtInt } from '../../ui'
import { ACTION, type CardProps } from './actions'
import { Fact, Facts, Feedback } from './parts'
import { happened } from './time'
import { t, type MessageKey } from '../../../i18n'

const DEFAULT_ROOT = '~/.claude/projects'

// Message keys, not text, so the lists follow the current locale.
const stored: readonly MessageKey[] = [
  'settings.watch.stored.numbers',
  'settings.watch.stored.names',
  'settings.watch.stored.paths',
  'settings.watch.stored.titles',
]

const notStored: readonly MessageKey[] = [
  'settings.watch.notStored.content',
  'settings.watch.notStored.onOpen',
  'settings.watch.notStored.local',
]

function progressText(status: Status): string {
  const files = t('settings.watch.files', {
    done: fmtInt(status.filesDone),
    total: fmtInt(status.filesTotal),
  })
  if (!status.consent) return t('settings.watch.progress.off')
  if (status.scanning)
    return status.filesTotal > 0
      ? t('settings.watch.progress.reading', { files })
      : t('settings.watch.progress.finding')
  const idle = t('settings.watch.progress.idle')
  return status.filesTotal > 0
    ? idle + ' ' + t('settings.watch.progress.lastPass', { files })
    : idle
}

/** Collection: whether transcripts are read, from where, how far, and what is kept. */
export function WatchCard({ api, status, actions, apply }: CardProps) {
  const busy = actions.pending?.id === ACTION.watch
  const locked = actions.pending !== null

  const start = () =>
    actions.run('watch', ACTION.watch, async () => {
      // The native side asks the user; false means they declined and nothing
      // changed, so there is no new Status to fetch.
      if (!(await api.enableWatch())) return t('settings.watch.declined')
      const next = await api.status()
      apply(next)
      return next.consent ? t('settings.watch.started') : undefined
    })
  const stop = () =>
    actions.run('watch', ACTION.watch, async () => {
      await api.disableWatch()
      const next = await api.status()
      apply(next)
      return next.consent ? undefined : t('settings.watch.stopped')
    })

  const action = status.consent ? (
    <button type="button" onClick={stop} disabled={locked} aria-busy={busy || undefined}>
      {busy ? t('settings.watch.stopping') : t('settings.watch.stop')}
    </button>
  ) : (
    <button
      type="button"
      className="u-set-primary"
      onClick={start}
      disabled={locked}
      aria-busy={busy || undefined}
    >
      {busy ? t('settings.watch.waiting') : t('settings.watch.start')}
    </button>
  )

  return (
    <Card
      title={t('settings.watch.title')}
      hint={t('settings.watch.hint')}
      action={action}
      className="u-span-12"
    >
      <Feedback actions={actions} scope="watch" />
      <div className="u-set-watch">
        <div>
          <Facts>
            <Fact label={t('settings.state')}>
              <Pill tone={status.consent ? 'good' : 'muted'}>
                {status.consent ? t('settings.on') : t('settings.off')}
              </Pill>
            </Fact>
            <Fact label={t('settings.watch.folder')}>
              <code className="u-set-path">{status.root || DEFAULT_ROOT}</code>
            </Fact>
            <Fact label={t('settings.watch.progress')}>
              <p className="u-set-progress-text" role="status">
                {progressText(status)}
              </p>
              {status.consent && status.scanning && (
                <progress
                  className="u-set-progress"
                  aria-label={t('settings.watch.progressAria')}
                  max={status.filesTotal > 0 ? status.filesTotal : undefined}
                  value={
                    status.filesTotal > 0
                      ? Math.min(status.filesDone, status.filesTotal)
                      : undefined
                  }
                />
              )}
            </Fact>
            <Fact label={t('settings.watch.lastScan')}>
              {happened(status.coverage.lastScanAt)
                ? fmtDateTime(status.coverage.lastScanAt)
                : t('settings.watch.never')}
            </Fact>
          </Facts>
          <p className="u-hint">
            {status.consent
              ? t('settings.watch.hintOn')
              : t('settings.watch.hintOff')}
          </p>
        </div>
        <div className="u-set-kept">
          <section>
            <h3>{t('settings.watch.stored')}</h3>
            <ul>
              {stored.map((key) => (
                <li key={key}>{t(key)}</li>
              ))}
            </ul>
          </section>
          <section>
            <h3>{t('settings.watch.notStored')}</h3>
            <ul>
              {notStored.map((key) => (
                <li key={key}>{t(key)}</li>
              ))}
            </ul>
          </section>
        </div>
      </div>
    </Card>
  )
}
