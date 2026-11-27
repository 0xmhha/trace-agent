import type { Coverage, CoverageState } from '../../types'
import { Card, Pill, Stat, fmtInt, type Tone } from '../../ui'

const stateLabel: Record<CoverageState, { text: string; tone: Tone }> = {
  complete: { text: '모두 읽음', tone: 'good' },
  partial: { text: '일부 누락', tone: 'warn' },
  scanning: { text: '읽는 중', tone: 'plain' },
  disabled: { text: '꺼짐', tone: 'muted' },
}

const stateSentence: Record<CoverageState, string> = {
  complete: '수집 기간 안의 세션 기록을 빠짐없이 읽었습니다.',
  partial: '수집 기간 안에 읽지 못한 부분이 있습니다. 화면의 수치가 실제보다 적을 수 있습니다.',
  scanning: '세션 기록을 읽는 중입니다. 다 읽을 때까지 수치가 계속 늘어납니다.',
  disabled: '수집이 꺼져 있어 세션 기록을 읽지 않습니다.',
}

/** Wording for the reason codes of usage.Coverage. An unknown code is shown as is. */
const reasonSentence: Record<string, string> = {
  unreadable_files: '열거나 읽지 못한 파일이 있습니다. 그 파일의 수치는 집계에서 빠졌습니다.',
  skipped_lines:
    '해석하지 못해 건너뛴 줄이 있습니다. 그 줄만 빠졌고 나머지 줄은 읽었습니다.',
  outside_window: '수집 기간보다 앞선 구간은 읽지 않았습니다.',
}

/** 읽기 상태: how complete the read of the whole collection window is. */
export function ReadStateCard({ coverage }: { coverage: Coverage }) {
  // The state comes from another process; a value this build does not know
  // is shown as it is instead of breaking the page.
  const label: { text: string; tone: Tone } = stateLabel[coverage.state] ?? {
    text: coverage.state,
    tone: 'muted',
  }
  const reasons = coverage.state === 'partial' ? coverage.reasons : []
  return (
    <Card title="읽기 상태" hint="수집 기간 전체를 얼마나 읽었는지 보여 줍니다." className="u-span-6">
      <div className="u-stats u-set-stats">
        <Stat label="추적 중인 파일" value={`${fmtInt(coverage.filesTracked)}개`} />
        <Stat
          label="읽지 못한 파일"
          value={`${fmtInt(coverage.filesUnreadable)}개`}
          tone={coverage.filesUnreadable > 0 ? 'warn' : 'plain'}
        />
        <Stat
          label="건너뛴 줄"
          value={`${fmtInt(coverage.linesSkipped)}줄`}
          tone={coverage.linesSkipped > 0 ? 'warn' : 'plain'}
        />
      </div>
      <p className="u-set-line">
        <Pill tone={label.tone}>{label.text}</Pill> {stateSentence[coverage.state]}
      </p>
      {reasons.length > 0 && (
        <ul className="u-set-reasons">
          {reasons.map((reason) => (
            <li key={reason}>{reasonSentence[reason] ?? reason}</li>
          ))}
        </ul>
      )}
    </Card>
  )
}
