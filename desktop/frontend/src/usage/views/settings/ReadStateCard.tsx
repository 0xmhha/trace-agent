import type { Coverage, CoverageState } from '../../types'
import { Card, Pill, Stat, fmtInt, type Tone } from '../../ui'
import { t, type MessageKey } from '../../../i18n'

// Message keys, not text, so the wording follows the current locale.
const stateLabel: Record<CoverageState, { text: MessageKey; tone: Tone }> = {
  complete: { text: 'settings.read.state.complete', tone: 'good' },
  partial: { text: 'settings.read.state.partial', tone: 'warn' },
  scanning: { text: 'settings.read.state.scanning', tone: 'plain' },
  disabled: { text: 'settings.read.state.disabled', tone: 'muted' },
}

const stateSentence: Record<CoverageState, MessageKey> = {
  complete: 'settings.read.sentence.complete',
  partial: 'settings.read.sentence.partial',
  scanning: 'settings.read.sentence.scanning',
  disabled: 'settings.read.sentence.disabled',
}

/** Wording for the reason codes of usage.Coverage. An unknown code is shown as is. */
const reasonSentence: Record<string, MessageKey> = {
  unreadable_files: 'settings.read.reason.unreadable_files',
  skipped_lines: 'settings.read.reason.skipped_lines',
  outside_window: 'settings.read.reason.outside_window',
}

function reasonText(reason: string): string {
  const key = Object.hasOwn(reasonSentence, reason) ? reasonSentence[reason] : undefined
  return key ? t(key) : reason
}

/** Read status: how complete the read of the whole collection window is. */
export function ReadStateCard({ coverage }: { coverage: Coverage }) {
  // The state comes from another process; a value this build does not know
  // is shown as it is instead of breaking the page.
  const known = Object.hasOwn(stateLabel, coverage.state) ? coverage.state : null
  const label: { text: string; tone: Tone } = known
    ? { text: t(stateLabel[known].text), tone: stateLabel[known].tone }
    : { text: coverage.state, tone: 'muted' }
  const sentence = known ? t(stateSentence[known]) : ''
  const reasons = coverage.state === 'partial' ? coverage.reasons : []
  return (
    <Card title={t('settings.read.title')} hint={t('settings.read.hint')} className="u-span-6">
      <div className="u-stats u-set-stats">
        <Stat
          label={t('settings.read.tracked')}
          value={t('settings.read.files', { n: fmtInt(coverage.filesTracked) })}
        />
        <Stat
          label={t('settings.read.unreadable')}
          value={t('settings.read.files', { n: fmtInt(coverage.filesUnreadable) })}
          tone={coverage.filesUnreadable > 0 ? 'warn' : 'plain'}
        />
        <Stat
          label={t('settings.read.skipped')}
          value={t('settings.read.lines', { n: fmtInt(coverage.linesSkipped) })}
          tone={coverage.linesSkipped > 0 ? 'warn' : 'plain'}
        />
      </div>
      <p className="u-set-line">
        <Pill tone={label.tone}>{label.text}</Pill> {sentence}
      </p>
      {reasons.length > 0 && (
        <ul className="u-set-reasons">
          {reasons.map((reason) => (
            <li key={reason}>{reasonText(reason)}</li>
          ))}
        </ul>
      )}
    </Card>
  )
}
