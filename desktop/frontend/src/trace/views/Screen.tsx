import type { ReactNode } from 'react'
import { obj, str } from '../../data'
import { Sequence } from '../../Sequence'
import { AgentDetail, AgentPage } from './Agents'
import { AttemptDetail, AttemptPage } from './Attempts'
import { EvidenceDetail, UnlinkedPage } from './Evidence'
import { IdentityDetail, IdentityPage } from './Identities'
import {
  GroupDetail,
  ModuleDetail,
  ModuleGroupPage,
  ModulePage,
} from './Modules'
import { Overview } from './Overview'
import { ActivityPage, PlanPage } from './Plan'
import { Sources } from './Sources'
import { TaskDetail, TaskPage } from './Tasks'
import type { Props } from './shared'

// The view of every screen type the service can answer with, keyed by its
// GraphQL __typename. A Map, so that an unexpected name cannot reach an
// inherited object property.
const views = new Map<string, (p: Props) => ReactNode>([
  ['Overview', Overview],
  ['TaskPage', TaskPage],
  ['TaskDetail', TaskDetail],
  ['AgentPage', AgentPage],
  ['AgentDetail', AgentDetail],
  ['PlanPage', PlanPage],
  ['ActivityPage', ActivityPage],
  ['AttemptPage', AttemptPage],
  ['AttemptDetail', AttemptDetail],
  ['ModuleGroupPage', ModuleGroupPage],
  ['GroupDetail', GroupDetail],
  ['ModulePage', ModulePage],
  ['ModuleDetail', ModuleDetail],
  ['SourcePage', Sources],
  ['SourceDetail', Sources],
  ['IdentityPage', IdentityPage],
  ['IdentityDetail', IdentityDetail],
  ['UnlinkedPage', UnlinkedPage],
  ['EvidenceDetail', EvidenceDetail],
  ['SequencePage', Sequence],
])

/** Draws the view that matches the screen type, followed by its pager. */
export function Screen(p: Props) {
  const View = views.get(str(p.screen.__typename)),
    page = obj(p.screen.page)
  return (
    <>
      {View ? (
        <View {...p} />
      ) : (
        <p className="notice">이 화면의 자료를 표시할 수 없습니다.</p>
      )}
      {page.hasNext === true && (
        <button
          onClick={() =>
            p.go({
              ...p.route.selection,
              page: { first: 50, after: page.endCursor },
            })
          }
        >
          다음 페이지
        </button>
      )}
    </>
  )
}
