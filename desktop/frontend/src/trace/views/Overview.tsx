import { useState } from 'react'
import { obj, rows, str, num, date } from '../../data'
import { formatNumber, t, type MessageKey } from '../../i18n'
import { Group, Items, type Props } from './shared'

// The time-bin tabs: the bin kind the service reports and the tab's label.
const BIN_TABS = [
  { kind: 'ATTEMPT_COUNT', label: 'trace.overview.attemptCount' },
  { kind: 'FAILURE_RATE', label: 'trace.overview.failureRate' },
  { kind: 'LATENCY_P95_MS', label: 'trace.overview.latencyP95' },
] as const satisfies readonly { kind: string; label: MessageKey }[]
type BinKind = (typeof BIN_TABS)[number]['kind']

export function Overview({ screen: s, go }: Props) {
  const [tab, setTab] = useState<BinKind>(BIN_TABS[0].kind)
  const tabLabel = t(
    BIN_TABS.find((b) => b.kind === tab)?.label ?? BIN_TABS[0].label,
  )
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
            {
              cohort: str(m.cohort),
              caption: t('trace.overview.sampleCaption', { name }),
            },
          )
        }
      >
        <span>{name}</span>
        <strong>
          {metric === 'FAILURES'
            ? m.value == null
              ? '—'
              : formatNumber(
                  num(m.value) <= 1 ? num(m.value) * 100 : num(m.value),
                  { maximumFractionDigits: 1 },
                ) + '%'
            : num(m.count)}
        </strong>
        <small>
          {metric === 'FAILURES'
            ? t('trace.overview.confirmedResults', { count: num(m.eligible) })
            : t('trace.overview.openSample')}{' '}
          ↗
        </small>
      </button>
    )
  }
  const a = obj(s.agents)
  return (
    <>
      <div className="metrics">
        {measure(t('trace.overview.tasks'), s.tasks, 'TASKS', 'ALL')}
        {measure(t('trace.subagents'), a.total, 'TASKS', 'AGENTS')}
        {measure(
          t('trace.overview.toolAttempts'),
          s.attempts,
          'TASKS',
          'ATTEMPTS',
        )}
        {measure(t('trace.waiting'), s.waiting, 'TASKS', 'WAITING')}
        {measure(
          t('trace.overview.failureRate'),
          s.failureRate,
          'TASKS',
          'FAILURES',
        )}
      </div>
      <Group title={t('trace.overview.subagentState')}>
        <div className="metrics">
          {measure(t('trace.running'), a.running, 'TASKS', 'AGENTS_RUNNING')}
          {measure(t('trace.overview.ended'), a.ended, 'TASKS', 'AGENTS_ENDED')}
          {measure(
            t('trace.overview.unknown'),
            a.unknown,
            'TASKS',
            'AGENTS_UNKNOWN',
          )}
        </div>
      </Group>
      <Group title={t('trace.overview.overTime')}>
        <div className="tabs">
          {BIN_TABS.map((b) => (
            <button
              className={b.kind === tab ? 'active' : ''}
              key={b.kind}
              onClick={() => setTab(b.kind)}
            >
              {t(b.label)}
            </button>
          ))}
        </div>
        <Items
          items={rows(s.bins).filter((b) => b.kind === tab)}
          render={(b) => (
            <>
              <time>{date(b.from)}</time>
              <button
                onClick={() =>
                  go(
                    { kind: 'TASKS' },
                    {
                      cohort: str(obj(b.measure).cohort),
                      caption: tabLabel + ' · ' + date(b.from),
                    },
                  )
                }
              >
                {String(obj(b.measure).value ?? t('trace.overview.noSample'))}
              </button>
              <small>
                {t('trace.overview.sampleSize', {
                  count: num(obj(b.measure).eligible),
                })}
              </small>
            </>
          )}
        />
      </Group>
    </>
  )
}
