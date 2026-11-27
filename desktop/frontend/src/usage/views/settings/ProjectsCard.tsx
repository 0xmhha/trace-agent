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
  return rows.sort((a, b) => a.name.localeCompare(b.name, 'ko') || a.key.localeCompare(b.key))
}

/** 프로젝트: every project with its numbers and an include/exclude switch. */
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
          ? `‘${row.name}’ 프로젝트는 제외한 상태 그대로입니다. 확인 창에서 취소하면 바꾸지 않습니다.`
          : `‘${row.name}’ 프로젝트를 다시 포함했습니다. 수집 기간 안의 기록을 다시 읽습니다.`
      return nowExcluded
        ? `‘${row.name}’ 프로젝트를 제외하고 저장한 수치를 지웠습니다.`
        : `‘${row.name}’ 프로젝트를 제외하지 못했습니다.`
    })

  const columns: Column<Row>[] = [
    {
      key: 'project',
      label: '프로젝트',
      render: (row) => (
        <>
          <span className="u-set-proj-name">{row.name}</span>
          {row.path && <span className="u-set-proj-path">{row.path}</span>}
        </>
      ),
    },
    {
      key: 'sessions',
      label: '세션',
      align: 'right',
      render: (row) => (row.excluded ? NO_VALUE : `${fmtInt(row.sessions)}개`),
    },
    {
      key: 'tokens',
      label: '토큰',
      align: 'right',
      render: (row) =>
        row.excluded ? (
          NO_VALUE
        ) : (
          <span title={`${fmtInt(row.tokens)} 토큰`}>{fmtCompact(row.tokens)}</span>
        ),
    },
    {
      key: 'last',
      label: '마지막 활동',
      render: (row) =>
        !row.excluded && happened(row.lastAt) ? (
          <span className="u-set-when">{fmtDateTime(row.lastAt)}</span>
        ) : (
          NO_VALUE
        ),
    },
    {
      key: 'include',
      label: '수집',
      render: (row) => (
        <Switch
          checked={!row.excluded}
          label={`${row.name} 프로젝트 수집`}
          onText="수집"
          offText="제외"
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
          프로젝트를 읽는 중입니다.
        </p>
      ) : null
    }
    if (shown.length === 0) return <Empty>찾는 말과 맞는 프로젝트가 없습니다.</Empty>
    return <DataTable columns={columns} rows={shown} rowKey={(row) => row.key} />
  }

  const count = needle
    ? `${fmtInt(rows.length)}개 중 ${fmtInt(shown.length)}개 표시`
    : `프로젝트 ${fmtInt(rows.length)}개, 그중 제외 ${fmtInt(excludedCount)}개`

  return (
    <Card
      title="프로젝트"
      hint={`세션과 토큰은 최근 ${p.days}일 동안 저장한 수치입니다.`}
      className="u-span-12"
    >
      <p className="u-set-line">
        프로젝트를 제외하면 그 프로젝트의 저장한 수치를 바로 지우고 더 읽지 않습니다. 다시
        포함하면 확인 창이 뜨고, 허락하면 수집 기간 안의 기록을 다시 읽습니다.
      </p>
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
              <label htmlFor={FILTER_ID}>프로젝트 찾기</label>
              <input
                id={FILTER_ID}
                type="search"
                value={filter}
                placeholder="이름이나 경로"
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
