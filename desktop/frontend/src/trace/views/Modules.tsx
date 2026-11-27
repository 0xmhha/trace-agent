import { obj, rows, str, num, short } from '../../data'
import { t } from '../../i18n'
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
    <Group title={t('trace.modules.groups')}>
      <div className="tabs">
        {[
          ['', t('trace.modules.all')],
          ['MODEL', t('trace.modules.model')],
          ['TOOL', t('trace.modules.tool')],
          ['MCP_SERVER', t('trace.modules.mcpServer')],
          ['MCP_TOOL', t('trace.modules.mcpTool')],
        ].map(([k, v]) => (
          <button
            key={k || 'ALL'}
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
            <span>
              {t('trace.modules.instances', { count: num(m.instances) })}
            </span>
          </>
        )}
      />
    </Group>
  )
}
export function GroupDetail({ screen: s, go }: Props) {
  return (
    <Group title={str(obj(s.group).name) || t('trace.modules.groupInstances')}>
      {moduleRows(rows(obj(s.instances).items), go)}
    </Group>
  )
}
export function ModulePage({ screen: s, go }: Props) {
  return <Group title={t('trace.modules.list')}>{moduleRows(rows(s.items), go)}</Group>
}
export function ModuleDetail({ screen: s, go }: Props) {
  return (
    <>
      <Group title={str(obj(s.module).name) || t('trace.modules.detail')}>
        <Badge value={obj(s.module).kind} /> <ID value={obj(s.module).id} />
      </Group>
      <Group title={t('trace.modules.linked')}>
        {moduleRows(rows(obj(s.children).items), go)}
      </Group>
      <Group title={t('trace.modules.attempts')}>
        {attemptRows(rows(obj(s.attempts).items), go)}
      </Group>
    </>
  )
}
