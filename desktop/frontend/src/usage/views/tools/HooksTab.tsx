import type { Coverage, HookUsage } from '../../types'
import { fmtInt } from '../../ui'
import { CountBar, NameCell, RateCell, SlowCell, failureRate, maxOf } from './cells'
import {
  HOOK_AVG_BAD_MS,
  HOOK_AVG_WARN_MS,
  HOOK_MAX_BAD_MS,
  HOOK_MAX_WARN_MS,
  slowTone,
} from './model'
import { SortTable, type SortColumn } from './SortTable'

// A hook is identified by event and name together; neither can contain NUL.
const KEY_SEPARATOR = '\u0000'

function columns(max: number): SortColumn<HookUsage>[] {
  return [
    {
      key: 'event',
      label: '이벤트',
      render: (h) => <NameCell name={h.event} />,
      sortValue: (h) => h.event,
    },
    {
      key: 'name',
      label: '이름',
      render: (h) => <NameCell name={h.name} />,
      sortValue: (h) => h.name,
    },
    {
      key: 'runs',
      label: '실행(회)',
      align: 'right',
      render: (h) => <CountBar value={h.runs} max={max} />,
      sortValue: (h) => h.runs,
    },
    {
      key: 'failures',
      label: '실패(회)',
      align: 'right',
      render: (h) => fmtInt(h.failures),
      sortValue: (h) => h.failures,
    },
    {
      key: 'rate',
      label: '실패율',
      align: 'right',
      render: (h) => <RateCell failures={h.failures} total={h.runs} />,
      sortValue: (h) => failureRate(h.failures, h.runs),
    },
    {
      key: 'avg',
      label: '평균 시간',
      align: 'right',
      render: (h) => (
        <SlowCell ms={h.avgMs} tone={slowTone(h.avgMs, HOOK_AVG_WARN_MS, HOOK_AVG_BAD_MS)} />
      ),
      sortValue: (h) => h.avgMs,
    },
    {
      key: 'max',
      label: '최대 시간',
      align: 'right',
      render: (h) => (
        <SlowCell ms={h.maxMs} tone={slowTone(h.maxMs, HOOK_MAX_WARN_MS, HOOK_MAX_BAD_MS)} />
      ),
      sortValue: (h) => h.maxMs,
    },
  ]
}

/** Hook runs by event and name. */
export function HooksTab(p: { rows: readonly HookUsage[]; coverage: Coverage }) {
  return (
    <SortTable
      caption="훅별 실행 횟수와 실패율, 실행 시간"
      columns={columns(maxOf(p.rows, (h) => h.runs))}
      rows={p.rows}
      rowKey={(h) => h.event + KEY_SEPARATOR + h.name}
      initial={{ key: 'runs', dir: 'desc' }}
      coverage={p.coverage}
    />
  )
}
