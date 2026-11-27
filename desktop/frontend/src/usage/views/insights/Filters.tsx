import type { Insight } from '../../types'
import { fmtInt } from '../../ui'
import {
  applyFilter,
  categoryLabel,
  categoryOrder,
  severityLabel,
  severityOrder,
  type InsightFilter,
} from './model'
import { t } from '../../../i18n'

function Chips<T extends string>(p: {
  legend: string
  options: readonly { id: T; label: string; count: number }[]
  value: T
  onChange: (id: T) => void
}) {
  return (
    <div className="u-ins-filter" role="group" aria-label={p.legend}>
      <span className="u-ins-filter-name" aria-hidden="true">
        {p.legend}
      </span>
      <div className="u-ins-chips">
        {p.options.map((option) => (
          <button
            key={option.id}
            type="button"
            className={'u-ins-chip' + (option.id === p.value ? ' u-ins-chip-on' : '')}
            aria-pressed={option.id === p.value}
            onClick={() => p.onChange(option.id)}
          >
            {option.label}
            <span className="u-count">{fmtInt(option.count)}</span>
          </button>
        ))}
      </div>
    </div>
  )
}

/**
 * Severity and category filters. Each count is the number of insights the
 * option would show with the other filter left as it is.
 */
export function Filters(p: {
  items: Insight[]
  value: InsightFilter
  onChange: (next: InsightFilter) => void
}) {
  const { items, value } = p
  const count = (filter: InsightFilter) => applyFilter(items, filter).length
  const severities = [
    { id: 'all' as const, label: t('insights.filter.all'), count: count({ ...value, severity: 'all' }) },
    ...severityOrder.map((severity) => ({
      id: severity,
      label: severityLabel(severity),
      count: count({ ...value, severity }),
    })),
  ]
  const categories = [
    { id: 'all' as const, label: t('insights.filter.all'), count: count({ ...value, category: 'all' }) },
    ...categoryOrder.map((category) => ({
      id: category,
      label: categoryLabel(category),
      count: count({ ...value, category }),
    })),
  ]
  return (
    <div className="u-ins-filters">
      <Chips
        legend={t('insights.filter.severity')}
        options={severities}
        value={value.severity}
        onChange={(severity) => p.onChange({ ...value, severity })}
      />
      <Chips
        legend={t('insights.filter.category')}
        options={categories}
        value={value.category}
        onChange={(category) => p.onChange({ ...value, category })}
      />
    </div>
  )
}
