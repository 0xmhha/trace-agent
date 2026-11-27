import type { Coverage, ProjectUsage } from '../../types'
import {
  DataTable,
  Pill,
  baseName,
  fmtCompact,
  fmtDateTime,
  fmtInt,
  sumTokens,
  type Column,
} from '../../ui'

/** Rows shown on the dashboard; the project filter in the toolbar lists every project. */
const PROJECT_ROWS = 6

/**
 * Most active projects of the range. A row opens the session list filtered to
 * that project; the name is a real button so the keyboard can do the same.
 */
export function ProjectTable(p: {
  projects: readonly ProjectUsage[]
  coverage: Coverage
  onOpen: (projectKey: string) => void
}) {
  // A project without activity in the range is not a "top" project.
  const rows = p.projects
    .filter((row) => row.sessions > 0 || row.prompts > 0 || sumTokens(row.tokens) > 0)
    .slice(0, PROJECT_ROWS)
  const columns: Column<ProjectUsage>[] = [
    {
      key: 'project',
      label: '프로젝트',
      render: (row) => (
        <span className="u-dash-project">
          <button
            type="button"
            className="u-dash-link"
            title={row.path || row.key}
            onClick={(e) => {
              // The row has the same action; do not run it twice.
              e.stopPropagation()
              p.onOpen(row.key)
            }}
          >
            {baseName(row.path || row.key)}
          </button>
          {row.excluded && <Pill tone="muted">제외됨</Pill>}
        </span>
      ),
    },
    { key: 'sessions', label: '세션', align: 'right', render: (row) => fmtInt(row.sessions) },
    { key: 'prompts', label: '프롬프트', align: 'right', render: (row) => fmtInt(row.prompts) },
    {
      key: 'tokens',
      label: '토큰',
      align: 'right',
      render: (row) => fmtCompact(sumTokens(row.tokens)),
    },
    {
      key: 'last',
      label: '마지막 활동',
      align: 'right',
      render: (row) => fmtDateTime(row.lastAt),
    },
  ]
  return (
    <DataTable
      columns={columns}
      rows={rows}
      rowKey={(row) => row.key}
      onRow={(row) => p.onOpen(row.key)}
      coverage={p.coverage}
    />
  )
}
