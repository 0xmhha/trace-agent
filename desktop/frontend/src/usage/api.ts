import type {
  ExtOverview,
  ExtPage,
  ExtQuery,
  InsightReport,
  Overview,
  Projects,
  Query,
  SessionDetail,
  SessionPage,
  SessionQuery,
  Status,
  TimelinePage,
  TimelineRequest,
  Tools,
} from './types'

/**
 * Everything the usage pages may ask the desktop process. Read methods map to
 * the service method names in internal/usage/api.go. The last three go through
 * native dialogs or the clipboard, so the WebView never holds a credential and
 * cannot grant itself file access.
 */
export interface UsageApi {
  status(): Promise<Status>
  overview(q: Query): Promise<Overview>
  sessions(q: SessionQuery): Promise<SessionPage>
  session(sessionId: string): Promise<SessionDetail>
  timeline(r: TimelineRequest): Promise<TimelinePage>
  tools(q: Query): Promise<Tools>
  projects(q: Query): Promise<Projects>
  insights(q: Query): Promise<InsightReport>
  extensions(q: Query): Promise<ExtOverview>
  extEvents(q: ExtQuery): Promise<ExtPage>
  setExcluded(project: string, excluded: boolean): Promise<Status>
  setWindowDays(days: number): Promise<Status>
  rescan(): Promise<Status>
  clearData(): Promise<Status>
  setExtensionEnabled(enabled: boolean): Promise<Status>
  /** Opens a native confirmation; resolves true when the user allowed it. */
  enableWatch(): Promise<boolean>
  disableWatch(): Promise<void>
  /** Copies the endpoint settings to the clipboard from the native side. */
  copyExtensionSettings(): Promise<void>
}

type UsageBridge = {
  Usage: (method: string, params: string) => Promise<string>
  EnableUsageWatch: () => Promise<boolean>
  DisableUsageWatch: () => Promise<void>
  CopyExtensionSettings: () => Promise<void>
}

function bridge(): UsageBridge {
  const b = (
    window as unknown as { go?: { main?: { Bridge?: Partial<UsageBridge> } } }
  ).go?.main?.Bridge
  if (!b?.Usage || !b.EnableUsageWatch || !b.DisableUsageWatch || !b.CopyExtensionSettings)
    throw Error('설치한 앱에서 열어 주세요.')
  return b as UsageBridge
}

async function call<T>(method: string, params: unknown = {}): Promise<T> {
  const raw = await bridge().Usage(method, JSON.stringify(params))
  return JSON.parse(raw) as T
}

/** Client backed by the Wails bridge of the installed app. */
export const nativeApi: UsageApi = {
  status: () => call('status'),
  overview: (q) => call('overview', q),
  sessions: (q) => call('sessions', q),
  session: (sessionId) => call('session', { sessionId }),
  timeline: (r) => call('timeline', r),
  tools: (q) => call('tools', q),
  projects: (q) => call('projects', q),
  insights: (q) => call('insights', q),
  extensions: (q) => call('extensions', q),
  extEvents: (q) => call('extEvents', q),
  setExcluded: (project, excluded) => call('setExcluded', { project, excluded }),
  setWindowDays: (days) => call('setWindowDays', { days }),
  rescan: () => call('rescan'),
  clearData: () => call('clearData'),
  setExtensionEnabled: (enabled) => call('setExtensionEnabled', { enabled }),
  // async, so that a missing bridge rejects instead of throwing synchronously.
  enableWatch: async () => bridge().EnableUsageWatch(),
  disableWatch: async () => bridge().DisableUsageWatch(),
  copyExtensionSettings: async () => bridge().CopyExtensionSettings(),
}
