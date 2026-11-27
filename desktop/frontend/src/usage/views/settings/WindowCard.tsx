import { useState } from 'react'
import { Card, fmtDay } from '../../ui'
import { ACTION, type CardProps } from './actions'
import { Feedback } from './parts'

/** Collection windows offered, in days. The widest is usage.MaxWindowDays. */
const WINDOW_OPTIONS: readonly number[] = [7, 30, 90, 365]

const SELECT_ID = 'u-set-window-days'

/** 기간: how far back transcripts are read. */
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
          return '기간을 그대로 두었습니다. 확인 창에서 취소하면 바꾸지 않습니다.'
        return days > current
          ? `수집 기간을 최근 ${days}일로 늘렸습니다. 늘어난 기간의 기록을 다시 읽습니다.`
          : `수집 기간을 최근 ${days}일로 줄였습니다.`
      } finally {
        setChosen(null)
      }
    })
  }

  const windowFrom = fmtDay(status.coverage.windowFrom)
  return (
    <Card title="기간" hint="얼마나 오래된 세션 기록까지 읽을지 정합니다." className="u-span-6">
      <div className="u-set-field">
        <label htmlFor={SELECT_ID}>수집 기간</label>
        <select
          id={SELECT_ID}
          value={busy && chosen !== null ? chosen : current}
          disabled={actions.pending !== null}
          aria-busy={busy || undefined}
          onChange={(e) => change(Number(e.target.value))}
        >
          {options.map((days) => (
            <option key={days} value={days}>
              최근 {days}일
            </option>
          ))}
        </select>
        {busy && (
          <span className="u-hint" role="status">
            기간을 바꾸는 중입니다…
          </span>
        )}
      </div>
      {windowFrom && <p className="u-set-line">지금은 {windowFrom} 이후의 기록을 읽습니다.</p>}
      <Feedback actions={actions} scope="window" />
      <p className="u-hint">
        기간을 늘리면 확인 창이 뜨고, 허락하면 늘어난 기간의 세션 기록을 다시 읽습니다. 줄일
        때는 묻지 않습니다.
      </p>
    </Card>
  )
}
