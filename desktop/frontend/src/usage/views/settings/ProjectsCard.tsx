import { useMemo, useState } from 'react'
import type { Coverage, Projects, ProjectUsage } from '../../types'
import {
  Card,
  DataTable,
  Empty,
  ErrorNote,
  baseName,
  fmtCompact,
  fmtDateTime,
  fmtInt,
  sumTokens,
  type Column,
  type Loaded,
} from '../../ui'
import { projectAction, type CardProps } from './actions'
import { Feedback, Switch } from './parts'
import { happened } from './time'
import { intlTag, t } from '../../../i18n'

/** The filter box appears only when the list is longer than this. */
const FILTER_MIN_ROWS = 4
const FILTER_ID = 'u-set-project-filter'
const NO_VALUE = '—'

interface Row {
  key: string
  name: string
  path: string
  sessions: number
  tokens: number
  lastAt: string
  excluded: boolean
  /** Lowercase text the filter box matches against. */
  search: string
}

const NO_TOKENS = { input: 0, output: 0, cacheRead: 0, cacheCreate: 0 }

function toRow(item: ProjectUsage, excluded: boolean): Row {
  const name = item.path ? baseName(item.path) : item.key
  return {
    key: item.key,
    name,
    path: item.path,
    sessions: item.sessions,
    tokens: sumTokens(item.tokens),
    lastAt: item.lastAt,
    excluded,
    search: [name, item.path, item.key].join('\n').toLowerCase(),
  }
}

/**
 * Joins the stored projects with the excluded list of Status. Status is the
 * newer of the two right after a switch was used, and a project excluded
 * before it was ever read exists only there.
 */
function toRows(items: ProjectUsage[], excluded: string[]): Row[] {
  const out = new Set(excluded)
  const rows = items.map((item) => toRow(item, out.has(item.key)))
  const listed = new Set(items.map((item) => item.key))
  for (const key of excluded) {
    if (listed.has(key)) continue
    const unseen: ProjectUsage = {
      key,
      path: '',
      sessions: 0,
      prompts: 0,
      tokens: NO_TOKENS,
      lastAt: '',
      excluded: true,
    }
    rows.push(toRow(unseen, true))
  }
  // Alphabetical, so a row does not jump when its numbers are deleted.
  return rows.sort((a, b) => a.name.localeCompare(b.name, intlTag()) || a.key.localeCompare(b.key))
}

/** Projects: every project with its numbers and an include/exclude switch. */
export function ProjectsCard(
  p: CardProps & { projects: Loaded<Projects>; coverage: Coverage; days: number },
) {
  const { api, status, actions, apply, projects } = p
  const [filter, setFilter] = useState('')
  const rows = useMemo(
    () => toRows(projects.data?.items ?? [], status.excluded),
    [projects.data, status.excluded],
  )
  const needle = filter.trim().toLowerCase()
  const shown = needle ? rows.filter((row) => row.search.includes(needle)) : rows
  const excludedCount = rows.filter((row) => row.excluded).length

  const toggle = (row: Row, include: boolean) =>
    actions.run('projects', projectAction(row.key), async () => {
      const next = await api.setExcluded(row.key, !include)
      apply(next)
      const nowExcluded = next.excluded.includes(row.key)
      if (include)
        return nowExcluded
          ? t('settings.projects.stillExcluded', { name: row.name })
          : t('settings.projects.included', { name: row.name })
      return nowExcluded
        ? t('settings.projects.excluded', { name: row.name })
        : t('settings.projects.excludeFailed', { name: row.name })
    })

  const columns: Column<Row>[] = [
    {
      key: 'project',
      label: t('settings.projects.col.project'),
      render: (row) => (
        <>
          <span className="u-set-proj-name">{row.name}</span>
          {row.path && <span className="u-set-proj-path">{row.path}</span>}
        </>
      ),
    },
    {
      key: 'sessions',
      label: t('settings.projects.col.sessions'),
      align: 'right',
      render: (row) =>
        row.excluded ? NO_VALUE : t('settings.projects.sessions', { n: fmtInt(row.sessions) }),
    },
    {
      key: 'tokens',
      label: t('settings.projects.col.tokens'),
      align: 'right',
      render: (row) =>
        row.excluded ? (
          NO_VALUE
        ) : (
          <span title={t('settings.projects.tokens', { n: fmtInt(row.tokens) })}>
            {fmtCompact(row.tokens)}
          </span>
        ),
    },
    {
      key: 'last',
      label: t('settings.projects.col.last'),
      render: (row) =>
        !row.excluded && happened(row.lastAt) ? (
          <span className="u-set-when">{fmtDateTime(row.lastAt)}</span>
        ) : (
          NO_VALUE
        ),
    },
    {
      key: 'include',
      label: t('settings.projects.col.include'),
      render: (row) => (
        <Switch
          checked={!row.excluded}
          label={t('settings.projects.switch', { name: row.name })}
          onText={t('settings.projects.switch.on')}
          offText={t('settings.projects.switch.off')}
          busy={actions.pending?.id === projectAction(row.key)}
          disabled={actions.pending !== null}
          onChange={(include) => toggle(row, include)}
        />
      ),
    },
  ]

  const list = () => {
    if (rows.length === 0) {
      if (projects.data !== null) return <Empty coverage={p.coverage} />
      // Nothing has arrived yet. After a failure the error note above
      // already says so, and an empty list would read as "no projects".
      return projects.loading ? (
        <p className="u-empty" role="status">
          {t('settings.projects.loading')}
        </p>
      ) : null
    }
    if (shown.length === 0) return <Empty>{t('settings.projects.noMatch')}</Empty>
    return <DataTable columns={columns} rows={shown} rowKey={(row) => row.key} />
  }

  const count = needle
    ? t('settings.projects.shown', { total: fmtInt(rows.length), shown: fmtInt(shown.length) })
    : t('settings.projects.count', {
        count: rows.length,
        total: fmtInt(rows.length),
        excluded: fmtInt(excludedCount),
      })

  return (
    <Card
      title={t('settings.projects.title')}
      hint={t('settings.projects.hint', { days: p.days })}
      className="u-span-12"
    >
      <p className="u-set-line">{t('settings.projects.line')}</p>
      {projects.error && (
        <div className="u-set-feedback">
          <ErrorNote error={projects.error} retry={projects.reload} />
        </div>
      )}
      <Feedback actions={actions} scope="projects" />
      {rows.length > 0 && (
        <div className="u-set-listbar">
          {rows.length > FILTER_MIN_ROWS && (
            <div className="u-set-field">
              <label htmlFor={FILTER_ID}>{t('settings.projects.find')}</label>
              <input
                id={FILTER_ID}
                type="search"
                value={filter}
                placeholder={t('settings.projects.placeholder')}
                autoComplete="off"
                spellCheck={false}
                onChange={(e) => setFilter(e.target.value)}
              />
            </div>
          )}
          <span className="u-hint u-set-count" role="status">
            {count}
          </span>
        </div>
      )}
      {list()}
    </Card>
  )
}
