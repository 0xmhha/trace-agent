import { obj, rows, str, num, short } from '../../data'
import {
  Badge,
  Group,
  ID,
  Items,
  attemptRows,
  moduleRows,
  type Props,
} from './shared'
export function ModuleGroupPage({ screen: s, go }: Props) {
  return (
    <Group title="모듈 그룹">
      <div className="tabs">
        {[
          ['', '전체'],
          ['MODEL', '모델'],
          ['TOOL', '도구'],
          ['MCP_SERVER', 'MCP 서버'],
          ['MCP_TOOL', 'MCP 도구'],
        ].map(([k, v]) => (
          <button
            key={v}
            onClick={() =>
              go({ kind: 'MODULE_GROUPS', ...(k ? { moduleKind: k } : {}) })
            }
          >
            {v}
          </button>
        ))}
      </div>
      <Items
        items={rows(s.items)}
        render={(m) => (
          <>
            <button
              onClick={() => go({ kind: 'MODULE_GROUP', groupId: str(m.id) })}
            >
              {str(m.name) || short(m.id)}
            </button>
            <Badge value={m.kind} />
            <span>인스턴스 {num(m.instances)}개</span>
          </>
        )}
      />
    </Group>
  )
}
export function GroupDetail({ screen: s, go }: Props) {
  return (
    <Group title={str(obj(s.group).name) || '모듈 인스턴스'}>
      {moduleRows(rows(obj(s.instances).items), go)}
    </Group>
  )
}
export function ModulePage({ screen: s, go }: Props) {
  return <Group title="모듈 목록">{moduleRows(rows(s.items), go)}</Group>
}
export function ModuleDetail({ screen: s, go }: Props) {
  return (
    <>
      <Group title={str(obj(s.module).name) || '모듈 상세'}>
        <Badge value={obj(s.module).kind} /> <ID value={obj(s.module).id} />
      </Group>
      <Group title="연결 모듈">
        {moduleRows(rows(obj(s.children).items), go)}
      </Group>
      <Group title="시도">
        {attemptRows(rows(obj(s.attempts).items), go)}
      </Group>
    </>
  )
}
