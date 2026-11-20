import { useState, type ReactNode } from 'react'
import {
  obj,
  rows,
  str,
  num,
  short,
  code,
  label,
  date,
  list,
  type Row,
  type Filter,
  type Stamp,
} from './data'
import { entity, task, type Go, type Query, type Route } from './nav'
import { SourcePanel } from './SourcePanel'
import { Sequence } from './Sequence'
import { TelemetryFields } from './TelemetryFields'
export interface Props {
  screen: Row
  stamp: Stamp
  filter: Filter
  route: Route
  go: Go
  connected: boolean
  query: Query
  refresh: () => void
  sources: Row[]
}
export function Group({
  title,
  children,
}: {
  title: string
  children: ReactNode
}) {
  return (
    <section className="panel">
      <h2>{title}</h2>
      {children}
    </section>
  )
}
export function Items({
  items,
  render,
  empty = '이 범위의 기록이 없습니다.',
}: {
  items: Row[]
  render: (r: Row) => ReactNode
  empty?: string
}) {
  return items.length ? (
    <div className="items">
      {items.map((r, i) => (
        <div className="row" key={str(r.id) || str(r.key) || i}>
          {render(r)}
        </div>
      ))}
    </div>
  ) : (
    <p className="empty">{empty}</p>
  )
}
export function ID({ value }: { value: unknown }) {
  return <code title={str(value)}>{short(value)}</code>
}
export function Badge({ value }: { value: unknown }) {
  return (
    <span className={'badge ' + str(value).toLowerCase()}>
      {code(value) || '불명'}
    </span>
  )
}
export function EvidenceLinks({ ids, go }: { ids: unknown; go: Go }) {
  return (
    <span className="actions">
      {list(ids).map((id) => (
        <button
          key={id}
          onClick={() => go({ kind: 'UNLINKED_DETAIL', evidenceId: id })}
        >
          근거 {short(id)}
        </button>
      ))}
    </span>
  )
}
export function evidenceRows(page: unknown, go: Go) {
  return (
    <Items
      items={rows(obj(page).items)}
      render={(e) => (
        <>
          <button
            onClick={() =>
              go({ kind: 'UNLINKED_DETAIL', evidenceId: str(e.id) })
            }
          >
            {str(e.recordKind) || '근거'} <ID value={e.id} />
          </button>
          <Badge value={e.link} />
          <small>{date(e.observed)}</small>
        </>
      )}
    />
  )
}
function activityRows(page: unknown, go: Go, taskId: string) {
  return (
    <Items
      items={rows(obj(page).items)}
      render={(a) => (
        <>
          <time>{date(a.at)}</time>
          <b>{code(a.kind)}</b>
          <Badge value={a.grade} />
          {a.attemptId ? (
            <button
              onClick={() =>
                go({
                  kind: 'ATTEMPT',
                  entity: entity(taskId, str(a.attemptId)),
                })
              }
            >
              호출 상세
            </button>
          ) : null}
          <EvidenceLinks ids={a.evidenceIds} go={go} />
        </>
      )}
    />
  )
}
function moduleRows(items: Row[], go: Go) {
  return (
    <Items
      items={items}
      render={(m) => (
        <>
          <button
            onClick={() =>
              go({ kind: 'MODULE', entity: entity(str(m.taskId), str(m.id)) })
            }
          >
            {str(m.name) || short(m.id)}
          </button>
          <Badge value={m.kind} />
          <span>시도 {num(obj(m.count).count)}회</span>
        </>
      )}
    />
  )
}
function attemptRows(items: Row[], go: Go) {
  return (
    <Items
      items={items}
      render={(a) => (
        <>
          <button
            onClick={() =>
              go({ kind: 'ATTEMPT', entity: entity(str(a.taskId), str(a.id)) })
            }
          >
            시도 <ID value={a.id} />
          </button>
          <Badge value={a.phase} />
          <Badge value={a.result} />
          {a.moduleId ? (
            <button
              onClick={() =>
                go({
                  kind: 'MODULE',
                  entity: entity(str(a.taskId), str(a.moduleId)),
                })
              }
            >
              모듈 <ID value={a.moduleId} />
            </button>
          ) : null}
          <small>{date(a.started)}</small>
        </>
      )}
    />
  )
}
export function Fields({ value }: { value: unknown }) {
  return (
    <Items
      items={rows(value)}
      empty="이 범위에 제공된 본문이 없습니다."
      render={(f) => (
        <>
          <span>{str(f.purpose)}</span>
          <Badge value={f.state} />
          {typeof f.text === 'string' && <p className="body">{f.text}</p>}
        </>
      )}
    />
  )
}
export function Screen(p: Props) {
  const { screen: s, go, route: r } = p,
    kind = str(s.__typename),
    selection = r.selection,
    t = selection.task?.taskId ?? selection.entity?.taskId ?? ''
  const [tab, setTab] = useState('시도 수')
  let body: ReactNode = null
  const measure = (
    name: string,
    v: unknown,
    target: string,
    metric: string,
  ) => {
    const m = obj(v)
    return (
      <button
        className="metric"
        disabled={!m.cohort}
        onClick={() =>
          go(
            { kind: target, metric },
            { cohort: str(m.cohort), caption: name + ' 표본' },
          )
        }
      >
        <span>{name}</span>
        <strong>
          {metric === 'FAILURES'
            ? m.value == null
              ? '—'
              : Number(
                  (num(m.value) <= 1
                    ? num(m.value) * 100
                    : num(m.value)
                  ).toFixed(1),
                ) + '%'
            : num(m.count)}
        </strong>
        <small>
          {metric === 'FAILURES'
            ? '확인된 결과 ' + num(m.eligible) + '회'
            : '같은 표본 열기'}{' '}
          ↗
        </small>
      </button>
    )
  }
  switch (kind) {
    case 'Overview': {
      const a = obj(s.agents)
      body = (
        <>
          <div className="metrics">
            {measure('작업', s.tasks, 'TASKS', 'ALL')}
            {measure('하위 에이전트', a.total, 'TASKS', 'AGENTS')}
            {measure('도구 시도', s.attempts, 'TASKS', 'ATTEMPTS')}
            {measure('응답 대기', s.waiting, 'TASKS', 'WAITING')}
            {measure('실패율', s.failureRate, 'TASKS', 'FAILURES')}
          </div>
          <Group title="하위 에이전트 상태">
            <div className="metrics">
              {measure('실행 중', a.running, 'TASKS', 'AGENTS_RUNNING')}
              {measure('종료', a.ended, 'TASKS', 'AGENTS_ENDED')}
              {measure('불명', a.unknown, 'TASKS', 'AGENTS_UNKNOWN')}
            </div>
          </Group>
          <Group title="시간별 활동">
            <div className="tabs">
              {['시도 수', '실패율', 'p95 지연'].map((t) => (
                <button
                  className={t === tab ? 'active' : ''}
                  key={t}
                  onClick={() => setTab(t)}
                >
                  {t}
                </button>
              ))}
            </div>
            <Items
              items={rows(s.bins).filter(
                (b) =>
                  b.kind ===
                  {
                    '시도 수': 'ATTEMPT_COUNT',
                    실패율: 'FAILURE_RATE',
                    'p95 지연': 'LATENCY_P95_MS',
                  }[tab],
              )}
              render={(b) => (
                <>
                  <time>{date(b.from)}</time>
                  <button
                    onClick={() =>
                      go(
                        { kind: 'TASKS' },
                        {
                          cohort: str(obj(b.measure).cohort),
                          caption: tab + ' · ' + date(b.from),
                        },
                      )
                    }
                  >
                    {String(obj(b.measure).value ?? '표본 없음')}
                  </button>
                  <small>표본 {num(obj(b.measure).eligible)}건</small>
                </>
              )}
            />
          </Group>
        </>
      )
      break
    }
    case 'TaskPage':
      body = (
        <Group
          title={selection.activeTasksOnly ? '진행 중인 작업' : '작업 목록'}
        >
          <Items
            items={rows(s.items)}
            empty={
              p.sources.length
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
      break
    case 'TaskDetail': {
      const a = obj(s.agents)
      body = (
        <>
          <Group
            title={
              str(obj(obj(s.task).title).text) ||
              '작업 ' + short(obj(s.task).id)
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
      break
    }
    case 'AgentPage':
      body = (
        <Group title="에이전트 계보">
          <Items
            items={rows(s.items)}
            render={(a) => (
              <>
                <button
                  onClick={() =>
                    go({ kind: 'AGENT', entity: entity(t, str(a.id)) })
                  }
                >
                  <ID value={a.id} />
                </button>
                <span>
                  부모 <ID value={a.parentId} />
                </span>
                <Badge value={a.state} />
                <time>{date(a.lastEvidenceAt)}</time>
              </>
            )}
          />
        </Group>
      )
      break
    case 'AgentDetail':
      body = (
        <>
          <Group title={'에이전트 ' + short(obj(s.agent).id)}>
            <Badge value={obj(s.agent).state} />
            <p>미션 {label(s.mission)}</p>
            <p>
              모델 <ID value={s.modelId} />
            </p>
          </Group>
          <Group title="활동">{activityRows(s.activities, go, t)}</Group>
        </>
      )
      break
    case 'PlanPage':
      body = (
        <Group title="계획 버전">
          {rows(s.versions).length ? (
            rows(s.versions).map((v) => (
              <div key={str(v.id)}>
                <h3>{date(v.at)}</h3>
                <Items
                  items={rows(v.steps)}
                  render={(step) => (
                    <>
                      <button
                        onClick={() =>
                          go({
                            kind: 'STEP_ACTIVITIES',
                            task: task(t),
                            planId: str(v.id),
                            stepId: str(step.key),
                          })
                        }
                      >
                        {num(step.ordinal) + 1}. {label(step.title)}
                      </button>
                      <Badge value={step.state} />
                      <EvidenceLinks ids={step.evidenceIds} go={go} />
                    </>
                  )}
                />
              </div>
            ))
          ) : (
            <p className="empty">계획을 제공하지 않았습니다.</p>
          )}
        </Group>
      )
      break
    case 'ActivityPage':
      body = <Group title="단계 활동">{activityRows(s, go, t)}</Group>
      break
    case 'AttemptPage':
      body = <Group title="호출 시도">{attemptRows(rows(s.items), go)}</Group>
      break
    case 'AttemptDetail': {
      const a = obj(s.attempt)
      body = (
        <>
          <Group title={'시도 ' + short(a.id)}>
            <Badge value={a.phase} /> <Badge value={a.result} />
            <p>
              {date(a.started)} → {date(a.ended)}
            </p>
            <div className="actions">
              {a.moduleId ? (
                <button
                  onClick={() =>
                    go({ kind: 'MODULE', entity: entity(t, str(a.moduleId)) })
                  }
                >
                  모듈 열기
                </button>
              ) : null}
              {a.agentId ? (
                <button
                  onClick={() =>
                    go({ kind: 'AGENT', entity: entity(t, str(a.agentId)) })
                  }
                >
                  에이전트 열기
                </button>
              ) : null}
            </div>
          </Group>
          <Group title="전달 과정">
            <Items
              items={rows(s.edges)}
              render={(e) => (
                <>
                  <time>{date(e.at)}</time>
                  <b>
                    {code(e.actor)} · {code(e.action)}
                  </b>
                  <Badge value={e.grade} />
                  <EvidenceLinks ids={e.evidenceIds} go={go} />
                </>
              )}
            />
          </Group>
          <Group title="지연 근거">
            <Items
              items={rows(s.durations)}
              render={(d) => (
                <>
                  <span>
                    {code(d.basis)} · {num(d.ms)} ms
                  </span>
                  <EvidenceLinks ids={d.evidenceIds} go={go} />
                </>
              )}
            />
          </Group>
          <Group title="입력·응답">
            <Fields
              value={[
                ...rows(s.request),
                ...rows(s.response),
                ...rows(s.conversation),
              ]}
            />
          </Group>
          <Group title="근거 기록">{evidenceRows(s.evidence, go)}</Group>
        </>
      )
      break
    }
    case 'ModuleGroupPage':
      body = (
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
                  onClick={() =>
                    go({ kind: 'MODULE_GROUP', groupId: str(m.id) })
                  }
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
      break
    case 'GroupDetail':
      body = (
        <Group title={str(obj(s.group).name) || '모듈 인스턴스'}>
          {moduleRows(rows(obj(s.instances).items), go)}
        </Group>
      )
      break
    case 'ModulePage':
      body = <Group title="모듈 목록">{moduleRows(rows(s.items), go)}</Group>
      break
    case 'ModuleDetail':
      body = (
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
      break
    case 'SourcePage':
    case 'SourceDetail':
      body = (
        <>
          <SourcePanel {...p} />
          <button onClick={() => go({ kind: 'IDENTITIES' })}>
            에이전트 식별 목록
          </button>
        </>
      )
      break
    case 'IdentityPage':
      body = (
        <Group title="에이전트 식별">
          <Items
            items={rows(s.items)}
            render={(i) => (
              <>
                <button
                  onClick={() =>
                    go({ kind: 'IDENTITY', identityId: str(i.id) })
                  }
                >
                  <ID value={i.id} />
                </button>
                <span>{code(i.attribution)}</span>
                <span>
                  부모 <ID value={i.parentId} />
                </span>
              </>
            )}
          />
        </Group>
      )
      break
    case 'IdentityDetail':
      body = (
        <>
          <Group title="작업 배정">
            <Items
              items={rows(obj(s.assignments).items)}
              render={(a) => (
                <>
                  <button
                    onClick={() =>
                      go({ kind: 'TASK', task: task(str(a.taskId)) })
                    }
                  >
                    작업 <ID value={a.taskId} />
                  </button>
                  <span>{label(a.mission)}</span>
                  <Badge value={a.grade} />
                  <EvidenceLinks ids={a.evidenceIds} go={go} />
                </>
              )}
            />
          </Group>
          <Group title="식별 근거">{evidenceRows(s.evidence, go)}</Group>
        </>
      )
      break
    case 'UnlinkedPage':
      body = <Group title="미연계 근거">{evidenceRows(s.evidence, go)}</Group>
      break
    case 'EvidenceDetail': {
      const e = obj(s.evidence)
      body = (
        <Group title={'근거 ' + short(e.id)}>
          <dl>
            {[
              ['종류', e.recordKind],
              ['관찰자', code(e.observer)],
              ['출처', short(e.sourceId)],
              ['발생', date(e.occurred)],
              ['관찰', date(e.observed)],
              ['연결', code(e.link)],
              ['판정 근거', e.reason ?? s.reason ?? '미제공'],
            ].map(([k, v]) => (
              <div key={str(k)}>
                <dt>{str(k)}</dt>
                <dd>{str(v)}</dd>
              </div>
            ))}
          </dl>
          <TelemetryFields value={s.safeMetadata} />
          <Fields value={s.fields} />
        </Group>
      )
      break
    }
    case 'SequencePage':
      body = <Sequence {...p} />
      break
    default:
      body = <p className="notice">이 화면의 자료를 표시할 수 없습니다.</p>
  }
  const page = obj(s.page)
  return (
    <>
      {body}
      {page.hasNext === true && (
        <button
          onClick={() =>
            go({ ...selection, page: { first: 50, after: page.endCursor } })
          }
        >
          다음 페이지
        </button>
      )}
    </>
  )
}
