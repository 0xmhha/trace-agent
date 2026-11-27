import { useCallback, useMemo } from 'react'
import type { ViewProps } from '../index'
import type { Coverage, Status } from '../types'
import { Card, lastDays, useLoad } from '../ui'
import { useActions } from './settings/actions'
import { ManageCard } from './settings/ManageCard'
import { ProjectsCard } from './settings/ProjectsCard'
import { ReadStateCard } from './settings/ReadStateCard'
import { ReceiverCard } from './settings/ReceiverCard'
import { WatchCard } from './settings/WatchCard'
import { WindowCard } from './settings/WindowCard'
import { LanguageCard } from './settings/LanguageCard'
import { t } from '../../i18n'
import './settings.css'

/**
 * The project list covers the longest window the app keeps (usage.MaxWindowDays),
 * so a project is listed however short the collection window is now.
 */
const PROJECT_LIST_DAYS = 365

/** Collection settings: what is collected, how far back, from which projects. */
export function Settings({ api, route, navigate, status, refresh, onStatus }: ViewProps) {
  // The range end moves only when stored data changed, as on the other pages.
  const range = useMemo(() => lastDays(PROJECT_LIST_DAYS), [refresh])
  // Every project is listed, so the project filter of the route is not applied.
  const projects = useLoad(() => api.projects({ range }), [api, range])
  const actions = useActions()

  const reloadProjects = projects.reload
  // Pages refetch a while after the data version moves. A change made here
  // should show in the list at once.
  const apply = useCallback(
    (next: Status) => {
      onStatus(next)
      reloadProjects()
    },
    [onStatus, reloadProjects],
  )

  if (!status)
    return (
      <Card title={t('settings.title')}>
        <p className="u-empty" role="status">
          {t('settings.noStatus')}
        </p>
      </Card>
    )

  // Status carries the scan and consent flags next to its coverage. They win
  // here, so the page never says "all read" while a pass is still running.
  const coverage: Coverage = {
    ...status.coverage,
    state: !status.consent ? 'disabled' : status.scanning ? 'scanning' : status.coverage.state,
  }
  const card = { api, status, actions, apply }
  return (
    <div className="u-set">
      <div className="u-grid">
        <WatchCard {...card} />
        <WindowCard {...card} />
        <ReadStateCard coverage={coverage} />
        <ProjectsCard
          {...card}
          projects={projects}
          coverage={coverage}
          days={PROJECT_LIST_DAYS}
        />
        <ManageCard {...card} />
        <ReceiverCard
          endpoint={status.extension}
          open={() => navigate({ view: 'extensions', days: route.days, project: route.project })}
        />
        <LanguageCard />
      </div>
      <p className="u-hint u-set-foot">
        {t('settings.foot')}
      </p>
    </div>
  )
}
