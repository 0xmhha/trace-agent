import type { ReactNode } from 'react'
import { ErrorNote } from '../../ui'
import type { Actions, Scope } from './actions'

/** Outcome of the last mutation of one card: its error or its result text. */
export function Feedback({ actions, scope }: { actions: Actions; scope: Scope }) {
  const failure = actions.failure?.scope === scope ? actions.failure.text : ''
  const notice = actions.notice?.scope === scope ? actions.notice.text : ''
  if (!failure && !notice) return null
  return (
    <div className="u-set-feedback">
      <ErrorNote error={failure} />
      {notice && (
        <p className="u-set-notice" role="status">
          {notice}
        </p>
      )}
    </div>
  )
}

/** Label and value rows of a card. */
export function Facts({ children }: { children: ReactNode }) {
  return <dl className="u-set-facts">{children}</dl>
}

export function Fact({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div>
      <dt>{label}</dt>
      <dd>{children}</dd>
    </div>
  )
}

/**
 * On/off control. The state is carried by the knob position and by the text
 * next to it, not by color alone.
 */
export function Switch(p: {
  checked: boolean
  label: string
  onText: string
  offText: string
  busy?: boolean
  disabled?: boolean
  onChange: (next: boolean) => void
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={p.checked}
      aria-label={p.label}
      aria-busy={p.busy || undefined}
      className="u-set-switch"
      disabled={p.disabled}
      onClick={() => p.onChange(!p.checked)}
    >
      <span className="u-set-switch-track" aria-hidden="true">
        <span className="u-set-switch-knob" />
      </span>
      <span className="u-set-switch-text">
        {p.busy ? '바꾸는 중…' : p.checked ? p.onText : p.offText}
      </span>
    </button>
  )
}
