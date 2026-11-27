import { obj, rows, str, num, short, code, label, date } from '../../data'
import { task } from '../../nav'
import { t } from '../../i18n'
import {
  Badge,
  EvidenceLinks,
  Group,
  ID,
  Items,
  activityRows,
  taskOf,
  type Props,
} from './shared'
export function TaskPage({ screen: s, go, route, sources }: Props) {
  const selection = route.selection
  return (
    <Group
      title={
        selection.activeTasksOnly ? t('trace.tasks.active') : t('trace.tasks.list')
      }
    >
      <Items
        items={rows(s.items)}
        empty={
          sources.length
            ? t('trace.tasks.noMatch')
            : t('trace.tasks.noSources')
        }
        render={(v) => (
          <>
            <button
              onClick={() => {
                localStorage.setItem('lastTask', str(v.id))
                go(
                  selection.activeTasksOnly
                    ? {
                        kind: 'SEQUENCE',
                        task: task(str(v.id)),
                        sequenceWindow: 'LATEST',
                      }
                    : { kind: 'TASK', task: task(str(v.id)) },
                  { live: !!selection.activeTasksOnly },
                )
              }}
            >
              {str(obj(v.title).text) || short(v.id)}
            </button>
            <Badge value={v.client} />
            <Badge value={v.state} />
            <small>{date(v.lastAt)}</small>
          </>
        )}
      />
      {!rows(s.items).length && (
        <button onClick={() => go({ kind: 'SOURCES' }, { live: true })}>
          {t('trace.tasks.manageSources')}
        </button>
      )}
    </Group>
  )
}
export function TaskDetail({ screen: s, go, route }: Props) {
  const id = taskOf(route),
    a = obj(s.agents)
  return (
    <>
      <Group
        title={
          str(obj(obj(s.task).title).text) ||
          t('trace.task.title', { id: short(obj(s.task).id) })
        }
      >
        <Badge value={obj(s.task).state} />
        <p>
          {t('trace.task.session')} <ID value={obj(s.task).sessionId} />
        </p>
        <EvidenceLinks ids={obj(s.task).stateEvidenceIds} go={go} />
        <div className="actions">
          {[
            ['AGENTS', t('trace.task.agents')],
            ['ATTEMPTS', t('trace.overview.toolAttempts')],
            ['PLAN', t('trace.task.plan')],
            ['SEQUENCE', t('trace.task.sequence')],
          ].map(([k, l]) => (
            <button
              key={k}
              onClick={() =>
                go({
                  kind: k!,
                  task: task(id),
                  ...(k === 'SEQUENCE' ? { sequenceWindow: 'LATEST' } : {}),
                })
              }
            >
              {l}
            </button>
          ))}
        </div>
      </Group>
      <Group title={t('trace.subagents')}>
        <div className="actions">
          {[
            ['RUNNING', 'running'],
            ['ENDED', 'ended'],
            ['UNKNOWN', 'unknown'],
          ].map(([state, key]) => (
            <button
              key={state}
              onClick={() =>
                go({ kind: 'AGENTS', task: task(id), agentState: state })
              }
            >
              {t('trace.task.agentState', {
                state: code(state),
                count: num(obj(a[key!]).count),
              })}
            </button>
          ))}
        </div>
      </Group>
      <Group title={t('trace.task.currentSteps')}>
        <Items
          items={rows(s.currentSteps)}
          render={(step) => (
            <>
              <b>{label(step.title)}</b>
              <Badge value={step.state} />
              <EvidenceLinks ids={step.evidenceIds} go={go} />
            </>
          )}
        />
      </Group>
      <Group title={t('trace.activity')}>
        {activityRows(s.activities, go, id)}
      </Group>
      <button onClick={() => go({ kind: 'MODULES' })}>
        {t('trace.task.modules')}
      </button>
    </>
  )
}
