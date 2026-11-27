import type { ExtEndpoint } from '../../types'
import { Card, Pill } from '../../ui'
import { Fact, Facts } from './parts'

/** 확장 수신기: state only. Its switch and setup live on the 확장 이벤트 page. */
export function ReceiverCard({ endpoint, open }: { endpoint: ExtEndpoint; open: () => void }) {
  return (
    <Card
      title="확장 수신기"
      hint="다른 도구가 보내는 이벤트를 이 Mac 안에서만 받습니다."
      className="u-span-6"
    >
      <Facts>
        <Fact label="상태">
          <Pill tone={endpoint.enabled ? 'good' : 'muted'}>
            {endpoint.enabled ? '켜짐' : '꺼짐'}
          </Pill>
        </Fact>
        {endpoint.enabled && endpoint.url && (
          <Fact label="받는 주소">
            <code className="u-set-path">{endpoint.url}</code>
          </Fact>
        )}
      </Facts>
      <p className="u-set-line">켜고 끄기와 연결 방법은 확장 이벤트 화면에 있습니다.</p>
      <button type="button" onClick={open}>
        확장 이벤트 열기
      </button>
    </Card>
  )
}
