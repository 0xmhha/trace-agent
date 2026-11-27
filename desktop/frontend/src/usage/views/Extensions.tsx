import type { ViewProps } from '../index'
import { useLoad } from '../ui'
import { ConnectGuide } from './extensions/ConnectGuide'
import { EndpointCard } from './extensions/EndpointCard'
import { EventsCard } from './extensions/EventsCard'
import { SourcesCard } from './extensions/SourcesCard'
import { filterTab, parseFilter, type EventFilter } from './extensions/model'
import { useEventPages } from './extensions/useEventPages'
import './extensions.css'

/**
 * 확장 이벤트 page: the local event endpoint, who sends to it, what arrived
 * and how to connect a tool. It works before transcript collection is
 * allowed, so it reads nothing that depends on consent.
 */
export function Extensions(props: ViewProps) {
  const { api, route, navigate, range, status, refresh, onStatus } = props
  const overview = useLoad(
    () => api.extensions({ range, project: route.project }),
    [api, range, route.project, refresh],
  )
  // The source and level filters live in the route so that reload and back keep them.
  const filter = parseFilter(route.tab)
  const setFilter = (next: EventFilter) => navigate({ ...route, tab: filterTab(next) })
  const pages = useEventPages(api, range, filter, refresh)

  // The polled status is fresher than the page data and is replaced at once
  // after a switch, so it wins when both are present.
  const endpoint = status?.extension ?? overview.data?.endpoint ?? null
  const endpointOn = endpoint?.enabled ?? false

  return (
    <div className="u-ext-page">
      <EndpointCard api={api} endpoint={endpoint} onStatus={onStatus} onChanged={overview.reload} />
      <SourcesCard
        overview={overview}
        endpointOn={endpointOn}
        selected={filter.source}
        onSelect={(source) => setFilter({ ...filter, source })}
      />
      <EventsCard
        pages={pages}
        filter={filter}
        onFilter={setFilter}
        sources={(overview.data?.sources ?? []).map((row) => row.source)}
        endpointOn={endpointOn}
        onSession={(sessionId) => navigate({ view: 'session', sessionId })}
      />
      <ConnectGuide />
    </div>
  )
}
