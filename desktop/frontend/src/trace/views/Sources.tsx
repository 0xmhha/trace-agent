import { SourcePanel } from '../../SourcePanel'
import type { Props } from './shared'
/** Source list and source detail: the panel tells them apart itself. */
export function Sources(p: Props) {
  return (
    <>
      <SourcePanel {...p} />
      <button onClick={() => p.go({ kind: 'IDENTITIES' })}>
        에이전트 식별 목록
      </button>
    </>
  )
}
