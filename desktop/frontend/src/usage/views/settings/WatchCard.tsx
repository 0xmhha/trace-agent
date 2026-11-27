import type { Status } from '../../types'
import { Card, Pill, fmtDateTime, fmtInt } from '../../ui'
import { ACTION, type CardProps } from './actions'
import { Fact, Facts, Feedback } from './parts'
import { happened } from './time'

const DEFAULT_ROOT = '~/.claude/projects'

const stored: readonly string[] = [
  '토큰 수, 호출 횟수, 걸린 시간 같은 수치',
  '모델, 도구, 스킬, 플러그인, MCP 서버, 훅의 이름',
  '프로젝트 경로, 브랜치 이름, Bash 프로그램 이름(인자는 제외)',
  '세션 제목(비밀 값으로 보이는 부분은 가린 뒤)',
]

const notStored: readonly string[] = [
  '대화 원문, 도구 인자, 도구 출력은 저장하지 않습니다.',
  '대화는 세션 상세를 열 때만 원본 파일에서 읽어 화면에 보여 줍니다.',
  '읽은 내용과 집계한 수치는 이 Mac 밖으로 나가지 않습니다.',
]

function progressText(status: Status): string {
  const files = `파일 ${fmtInt(status.filesDone)} / ${fmtInt(status.filesTotal)}개`
  if (!status.consent) return '수집이 꺼져 있어 읽지 않습니다.'
  if (status.scanning)
    return status.filesTotal > 0 ? `읽는 중입니다. ${files}` : '읽을 파일을 찾는 중입니다.'
  const idle = '대기 중입니다. 바뀐 파일이 생기면 이어서 읽습니다.'
  return status.filesTotal > 0 ? `${idle} 최근 훑기 ${files}` : idle
}

/** 수집: whether transcripts are read, from where, how far, and what is kept. */
export function WatchCard({ api, status, actions, apply }: CardProps) {
  const busy = actions.pending?.id === ACTION.watch
  const locked = actions.pending !== null

  const start = () =>
    actions.run('watch', ACTION.watch, async () => {
      // The native side asks the user; false means they declined and nothing
      // changed, so there is no new Status to fetch.
      if (!(await api.enableWatch())) return '확인 창에서 취소해 수집을 시작하지 않았습니다.'
      const next = await api.status()
      apply(next)
      return next.consent ? '수집을 시작했습니다. 세션 기록을 읽기 시작합니다.' : undefined
    })
  const stop = () =>
    actions.run('watch', ACTION.watch, async () => {
      await api.disableWatch()
      const next = await api.status()
      apply(next)
      return next.consent ? undefined : '수집을 멈췄습니다. 이미 모은 수치는 그대로 있습니다.'
    })

  const action = status.consent ? (
    <button type="button" onClick={stop} disabled={locked} aria-busy={busy || undefined}>
      {busy ? '멈추는 중…' : '수집 중지'}
    </button>
  ) : (
    <button
      type="button"
      className="u-set-primary"
      onClick={start}
      disabled={locked}
      aria-busy={busy || undefined}
    >
      {busy ? '확인을 기다리는 중…' : '수집 시작'}
    </button>
  )

  return (
    <Card
      title="수집"
      hint="이 Mac의 Claude Code 세션 기록을 읽어 사용 현황을 집계합니다."
      action={action}
      className="u-span-12"
    >
      <Feedback actions={actions} scope="watch" />
      <div className="u-set-watch">
        <div>
          <Facts>
            <Fact label="상태">
              <Pill tone={status.consent ? 'good' : 'muted'}>
                {status.consent ? '켜짐' : '꺼짐'}
              </Pill>
            </Fact>
            <Fact label="읽는 폴더">
              <code className="u-set-path">{status.root || DEFAULT_ROOT}</code>
            </Fact>
            <Fact label="진행">
              <p className="u-set-progress-text" role="status">
                {progressText(status)}
              </p>
              {status.consent && status.scanning && (
                <progress
                  className="u-set-progress"
                  aria-label="세션 기록 읽기 진행"
                  max={status.filesTotal > 0 ? status.filesTotal : undefined}
                  value={
                    status.filesTotal > 0
                      ? Math.min(status.filesDone, status.filesTotal)
                      : undefined
                  }
                />
              )}
            </Fact>
            <Fact label="마지막 훑기">
              {happened(status.coverage.lastScanAt)
                ? fmtDateTime(status.coverage.lastScanAt)
                : '아직 없습니다'}
            </Fact>
          </Facts>
          <p className="u-hint">
            {status.consent
              ? '수집을 멈춰도 이미 모은 수치는 남습니다. 지우려면 아래 관리의 모두 지우기를 씁니다.'
              : '수집 시작을 누르면 확인 창이 뜹니다. 허락해야 세션 기록을 읽기 시작합니다.'}
          </p>
        </div>
        <div className="u-set-kept">
          <section>
            <h3>저장하는 것</h3>
            <ul>
              {stored.map((line) => (
                <li key={line}>{line}</li>
              ))}
            </ul>
          </section>
          <section>
            <h3>저장하지 않는 것</h3>
            <ul>
              {notStored.map((line) => (
                <li key={line}>{line}</li>
              ))}
            </ul>
          </section>
        </div>
      </div>
    </Card>
  )
}
