import { useEffect, useRef, useState } from 'react'
import { Card } from '../../ui'
import { ACTION, type CardProps } from './actions'
import { Feedback } from './parts'

const CONFIRM_TITLE_ID = 'u-set-clear-title'

/** 관리: read everything again, or delete everything that was stored. */
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
      return '처음부터 다시 읽도록 했습니다. 진행 상황은 위의 수집 카드에 나옵니다.'
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
        if (!changed) return '바뀐 것이 없습니다. 확인 창에서 취소했거나 지울 자료가 없었습니다.'
        const cleared = '저장한 수치와 확장 이벤트를 모두 지웠습니다.'
        return next.consent ? cleared : `${cleared} 수집은 꺼져 있습니다.`
      } finally {
        setConfirming(false)
      }
    })

  return (
    <Card title="관리" className="u-span-6">
      <Feedback actions={actions} scope="manage" />
      <div className="u-set-task">
        <div>
          <h3>다시 읽기</h3>
          <p className="u-hint">
            모든 세션 기록을 처음부터 다시 읽습니다. 저장한 수치는 그대로 두고, 다시 읽어도
            중복해서 세지 않습니다.
            {!status.consent && ' 수집이 꺼져 있으면 읽지 않으므로 쓸 수 없습니다.'}
          </p>
        </div>
        <button
          type="button"
          onClick={rescan}
          disabled={locked || !status.consent}
          aria-busy={rescanning || undefined}
        >
          {rescanning ? '요청하는 중…' : '다시 읽기'}
        </button>
      </div>
      <div className="u-set-task">
        <div>
          <h3>모두 지우기</h3>
          <p className="u-hint">
            저장한 수치와 확장 이벤트를 모두 지우고 수집을 멈춥니다. 세션 기록 원본 파일은
            건드리지 않습니다.
          </p>
        </div>
        <button
          type="button"
          ref={openButton}
          className="u-set-danger"
          onClick={() => setConfirming(true)}
          disabled={locked || confirming}
          aria-expanded={confirming}
        >
          모두 지우기
        </button>
      </div>
      {confirming && (
        <div className="u-set-confirm" role="group" aria-labelledby={CONFIRM_TITLE_ID}>
          <p id={CONFIRM_TITLE_ID} className="u-set-confirm-title">
            정말 모두 지울까요?
          </p>
          <ul>
            <li>저장한 사용 수치를 모두 지웁니다.</li>
            <li>수집도 함께 멈춥니다. 다시 모으려면 수집 시작을 눌러 새로 허락해야 합니다.</li>
            <li>
              확장 이벤트도 영구히 지웁니다. 확장 이벤트는 원본이 없어 되살릴 수 없습니다.
            </li>
          </ul>
          <p className="u-hint">지우기를 누르면 확인 창이 한 번 더 뜹니다.</p>
          <div className="u-set-confirm-actions">
            <button
              type="button"
              ref={cancelButton}
              onClick={() => setConfirming(false)}
              disabled={clearing}
            >
              취소
            </button>
            <button
              type="button"
              className="u-set-danger-solid"
              onClick={clear}
              disabled={locked}
              aria-busy={clearing || undefined}
            >
              {clearing ? '지우는 중…' : '지우고 수집 중지'}
            </button>
          </div>
        </div>
      )}
    </Card>
  )
}
