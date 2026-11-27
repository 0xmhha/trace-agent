import { useState } from 'react'
import { obj, rows, str, num, date } from '../../data'
import { Group, Items, type Props } from './shared'
export function Overview({ screen: s, go }: Props) {
  const [tab, setTab] = useState('시도 수')
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
  const a = obj(s.agents)
  return (
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
}
