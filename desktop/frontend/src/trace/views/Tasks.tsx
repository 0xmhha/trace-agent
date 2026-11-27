import { obj, rows, str, num, short, code, label, date } from '../../data'
import { task } from '../../nav'
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
      title={selection.activeTasksOnly ? '진행 중인 작업' : '작업 목록'}
    >
      <Items
        items={rows(s.items)}
        empty={
          sources.length
            ? '조건에 맞는 작업이 없습니다.'
            : '연결한 출처가 없습니다.'
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
          출처 관리
        </button>
      )}
    </Group>
  )
}
export function TaskDetail({ screen: s, go, route }: Props) {
  const t = taskOf(route),
    a = obj(s.agents)
  return (
    <>
      <Group
        title={
          str(obj(obj(s.task).title).text) || '작업 ' + short(obj(s.task).id)
        }
      >
        <Badge value={obj(s.task).state} />
        <p>
          세션 <ID value={obj(s.task).sessionId} />
        </p>
        <EvidenceLinks ids={obj(s.task).stateEvidenceIds} go={go} />
        <div className="actions">
          {[
            ['AGENTS', '에이전트'],
            ['ATTEMPTS', '도구 시도'],
            ['PLAN', '계획'],
            ['SEQUENCE', '분기 시퀀스'],
          ].map(([k, l]) => (
            <button
              key={k}
              onClick={() =>
                go({
                  kind: k!,
                  task: task(t),
                  ...(k === 'SEQUENCE' ? { sequenceWindow: 'LATEST' } : {}),
                })
              }
            >
              {l}
            </button>
          ))}
        </div>
      </Group>
      <Group title="하위 에이전트">
        <div className="actions">
          {[
            ['RUNNING', 'running'],
            ['ENDED', 'ended'],
            ['UNKNOWN', 'unknown'],
          ].map(([state, key]) => (
            <button
              key={state}
              onClick={() =>
                go({ kind: 'AGENTS', task: task(t), agentState: state })
              }
            >
              {code(state)} {num(obj(a[key!]).count)}개
            </button>
          ))}
        </div>
      </Group>
      <Group title="진행 중인 단계">
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
      <Group title="활동">{activityRows(s.activities, go, t)}</Group>
      <button onClick={() => go({ kind: 'MODULES' })}>
        작업 표본의 모듈
      </button>
    </>
  )
}
