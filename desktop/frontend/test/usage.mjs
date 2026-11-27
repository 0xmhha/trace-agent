// Drives the usage pages of the built frontend. The bridge is fake; behind it
// the Go harness (desktop/usage_ia_test.go) runs the real usage pipeline over
// the synthetic transcripts of internal/usage/testdata, so the numbers
// asserted here are the ones documented in that directory's README.
import assert from 'node:assert/strict'
import { chromium } from 'playwright'

const [origin] = process.argv.slice(2)
const headers = { 'X-Trace-Test': process.env.TRACE_TEST_TOKEN }
const seed = await fetch(origin + '/_seed', { headers }).then((r) => r.json())

// What the fixtures hold.
const SESSION_TITLE = 'Fix failing build'
const PROJECT_PATH = '/work/sample'
const PROJECT_NAME = 'sample'
const KPIS = [
  ['세션', '1개'],
  ['프롬프트', '2회'],
  ['토큰 합계', '328.8K'],
  ['캐시 적중률', '99.0%'],
  ['도구 호출', '6회'],
  ['비용', '$1.25'],
  ['하위 에이전트', '1개'],
  ['맥락 압축', '1회'],
]
const USAGE_ENTRIES = [
  '대시보드',
  '세션',
  '도구와 확장',
  '인사이트',
  '확장 이벤트',
  '수집 설정',
]
const TRACE_ENTRIES = ['실시간', '개요', '작업', '모듈', '출처와 설정']
// The published trace fixture the harness seeds, as in ia.mjs.
const TRACE_ATTEMPTS = 5

// The usage pages poll the status this often (STATUS_POLL_MS in usage/index.tsx).
// Watching a trace page for longer than one period shows that the poll stopped.
const STATUS_POLL_MS = 3000
const QUIET_MS = STATUS_POLL_MS + 1000
// How long an address is watched for a late rewrite by the workspace that was left.
const SETTLE_MS = 1000

const browser = await chromium.launch({ channel: 'chrome', headless: true })
const page = await browser.newPage({
  viewport: { width: 1280, height: 800 },
  reducedMotion: 'reduce',
})
page.setDefaultTimeout(6000)
const errors = []
page.on('pageerror', (e) => errors.push('page error: ' + e.message))
// Chrome asks for the icon on its own and the harness serves none. Every
// other console error fails the run.
const BROWSER_ICON = origin + '/favicon.ico'
page.on('console', (m) => {
  if (m.type() === 'error' && m.location().url !== BROWSER_ICON)
    errors.push('console error: ' + m.text() + ' ' + m.location().url)
})

const exact = (text) =>
  new RegExp('^' + text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '$')
const title = (name) =>
  page.getByRole('heading', { name, exact: true, level: 1 })
const usageNav = page.getByRole('group', { name: '사용 분석', exact: true })
const traceNav = page.getByRole('group', { name: '추적', exact: true })
const usageEntry = (name) =>
  usageNav.getByRole('button', { name, exact: true })
// A trace entry's icon is part of its name, so the label is matched inside it.
const traceEntry = (name) => traceNav.getByRole('button', { name })
const card = (name) =>
  page.locator('section').filter({
    has: page.getByRole('heading', { name, exact: true, level: 2 }),
  })
const hash = () => page.evaluate(() => location.hash)
const bridge = () => page.evaluate(() => window.__usage)

async function openUsage(name, address) {
  await usageEntry(name).click()
  await title(name).waitFor()
  assert.equal(await hash(), address)
}
async function kpis() {
  for (const [label, value] of KPIS)
    await page
      .locator('.u-dash-kpis .u-stat')
      .filter({ has: page.locator('.u-stat-label', { hasText: exact(label) }) })
      .locator('.u-stat-value', { hasText: exact(value) })
      .waitFor()
}
const timeline = page.getByRole('list', { name: '대화 타임라인' })
// One timeline row: its kind label and the name it shows in code type.
const row = (kind, name) =>
  timeline
    .getByRole('listitem')
    .filter({ hasText: kind })
    .filter({ has: page.locator('code', { hasText: exact(name) }) })
const sessionRow = () =>
  page.getByRole('button', { name: SESSION_TITLE, exact: true })
// The address must still be the same after the workspace that was left had
// time to write its own.
async function stays(address, ms) {
  assert.equal(await hash(), address)
  await page.waitForTimeout(ms)
  assert.equal(await hash(), address)
}
async function traceOverview() {
  await page.getByRole('heading', { name: '시간별 활동' }).waitFor()
  await page.waitForFunction(
    (attempts) =>
      [...document.querySelectorAll('.metric')].some(
        (c) =>
          c.querySelector('span')?.textContent === '도구 시도' &&
          c.querySelector('strong')?.textContent === String(attempts),
      ),
    TRACE_ATTEMPTS,
  )
}

try {
  await page.addInitScript(
    ({ headers }) => {
      // calls counts Bridge.Usage, last keeps the newest response of each
      // method, native lists the calls that would open a dialog in the app.
      const usage = { calls: 0, last: {}, native: [] }
      window.__usage = usage
      const native = (name, result) => async () => {
        usage.native.push(name)
        return result
      }
      window.go = {
        main: {
          Bridge: {
            Bootstrap: () =>
              fetch('/_ticket', { headers }).then((r) => r.json()),
            Pick: async () => '',
            HasReceiver: async () => false,
            Usage: async (method, params) => {
              usage.calls++
              const r = await fetch('/__usage', {
                method: 'POST',
                headers: { ...headers, 'Content-Type': 'application/json' },
                body: JSON.stringify({ method, params: JSON.parse(params) }),
              })
              const text = await r.text()
              if (!r.ok) throw new Error(text.trim())
              usage.last[method] = text
              return text
            },
            EnableUsageWatch: native('EnableUsageWatch', false),
            DisableUsageWatch: native('DisableUsageWatch'),
            CopyExtensionSettings: native('CopyExtensionSettings'),
          },
        },
      }
    },
    { headers },
  )

  // 1. The bare address is the dashboard, with the numbers of the pipeline.
  await page.goto(origin + '/')
  await title('대시보드').waitFor()
  for (const name of USAGE_ENTRIES) await usageEntry(name).waitFor()
  assert.equal(await usageNav.getByRole('button').count(), USAGE_ENTRIES.length)
  for (const name of TRACE_ENTRIES) await traceEntry(name).waitFor()
  assert.equal(await traceNav.getByRole('button').count(), TRACE_ENTRIES.length)
  await kpis()
  assert.equal(await hash(), '', 'the shell must not rewrite the bare address')
  assert.equal(await usageEntry('대시보드').getAttribute('class'), 'selected')

  // 2. Every usage view shows something only the scanned transcripts explain.
  await openUsage('세션', '#sessions')
  await sessionRow().waitFor()
  assert.equal(await page.getByRole('rowheader').count(), 1)
  await page
    .getByRole('status')
    .filter({ hasText: '이 기간의 세션 1개' })
    .waitFor()

  const sessionAddress = '#session/' + seed.SessionID
  await sessionRow().click()
  await title('세션 상세').waitFor()
  assert.equal(await hash(), sessionAddress)
  await page
    .getByRole('heading', { name: SESSION_TITLE, exact: true, level: 2 })
    .waitFor()
  await row('도구 호출', 'Bash').waitFor()
  const failed = row('도구 결과', 'Bash')
  await failed.getByText('오류', { exact: true }).waitFor()
  await failed
    .getByText('error: cannot find module config', { exact: true })
    .waitFor()
  await row('훅', 'PreToolUse:Bash')
    .getByText('성공', { exact: true })
    .waitFor()
  // One line of the root fixture is broken JSON, and the page says so.
  await page
    .getByRole('status')
    .filter({ hasText: '건너뛴 줄이 1개' })
    .waitFor()

  await page.getByRole('tab', { name: /^도구/ }).click()
  await page
    .getByRole('tabpanel', { name: '도구', exact: true })
    .locator('code', { hasText: exact('mcp__issues__search') })
    .waitFor()
  assert.equal(await hash(), sessionAddress + '?tab=tools')

  // 3. The sub-agent's own transcript replaces the root one in the timeline.
  await page.getByRole('tab', { name: /^하위 에이전트/ }).click()
  await page
    .getByRole('tabpanel', { name: '하위 에이전트', exact: true })
    .getByRole('button', { name: 'Explore ' + seed.AgentID + ' 대화 보기' })
    .click()
  const chat = page.getByRole('tabpanel', { name: '대화', exact: true })
  await row('도구 호출', 'WebFetch').waitFor()
  await row('훅', 'PreToolUse:WebFetch').waitFor()
  await timeline.getByText('3 files import config.', { exact: true }).waitFor()
  assert.equal(await row('도구 호출', 'Bash').count(), 0)
  assert.equal(await chat.getByRole('combobox').inputValue(), seed.AgentID)

  await openUsage('도구와 확장', '#tools')
  const tools = page.getByRole('tabpanel', { name: '도구', exact: true })
  await tools.getByText('mcp__issues__search', { exact: true }).waitFor()
  await tools.getByText('서버 issues', { exact: true }).waitFor()

  await openUsage('인사이트', '#insights')
  const rules = card('규칙 판단 결과')
  await rules.waitFor()
  // The three counters are compared with the report the page was given.
  const report = JSON.parse((await bridge()).last.insights)
  const outcomes = [
    ['발견', report.rulesFired],
    ['통과', report.rulesPassed],
    ['판단 보류', report.rulesSkipped],
  ]
  const judged = outcomes.reduce((sum, [, count]) => sum + count, 0)
  assert.ok(judged > 0, 'the pipeline must have judged at least one rule')
  for (const [label, count] of outcomes)
    await rules
      .getByRole('listitem')
      .filter({ hasText: label })
      .locator('strong', { hasText: exact(count + '개') })
      .waitFor()
  await rules
    .getByRole('img', {
      name:
        `규칙 ${judged}개 가운데 ` +
        outcomes.map(([label, count]) => `${label} ${count}개`).join(', '),
      exact: true,
    })
    .waitFor()

  await openUsage('확장 이벤트', '#extensions')
  const receiver = card('이벤트 수신기')
  await receiver.getByText('꺼짐', { exact: true }).waitFor()
  await receiver.getByText('수신기를 켜면 주소가 정해집니다.').waitFor()
  assert.equal(
    await receiver
      .getByRole('switch', { name: '이벤트 받기' })
      .getAttribute('aria-checked'),
    'false',
  )
  assert.equal(
    await receiver.getByRole('button', { name: '설정 복사' }).isDisabled(),
    true,
  )

  await openUsage('수집 설정', '#settings')
  const watch = card('수집')
  await watch.getByText('켜짐', { exact: true }).waitFor()
  await watch.getByRole('button', { name: '수집 중지' }).waitFor()
  await watch.getByText(seed.RootLabel, { exact: true }).waitFor()
  const projects = card('프로젝트')
  const project = projects.getByRole('row').filter({ hasText: PROJECT_PATH })
  await project.getByText(PROJECT_NAME, { exact: true }).waitFor()
  await project.getByText('1개', { exact: true }).waitFor()
  assert.equal(
    await project
      .getByRole('switch', { name: PROJECT_NAME + ' 프로젝트 수집' })
      .getAttribute('aria-checked'),
    'true',
  )
  await projects
    .getByRole('status')
    .filter({ hasText: '프로젝트 1개, 그중 제외 0개' })
    .waitFor()

  // 4. The two workspaces leave each other's address and bridge alone.
  await openUsage('대시보드', '#dashboard')
  await kpis()
  await traceEntry('개요').click()
  await traceOverview()
  const whileTracing = (await bridge()).calls
  await stays('#overview', QUIET_MS)
  assert.equal(
    (await bridge()).calls,
    whileTracing,
    'the usage pages must not call the bridge while a trace page is shown',
  )
  await usageEntry('대시보드').click()
  await title('대시보드').waitFor()
  await kpis()
  await stays('#dashboard', SETTLE_MS)
  assert.ok((await bridge()).calls > whileTracing)

  // 5. Back and forward restore the usage view of each entry, and cross
  // between the workspaces.
  await openUsage('세션', '#sessions')
  await sessionRow().click()
  await title('세션 상세').waitFor()
  await row('도구 호출', 'Bash').waitFor()
  await openUsage('인사이트', '#insights')
  await page.goBack()
  await title('세션 상세').waitFor()
  await row('도구 호출', 'Bash').waitFor()
  assert.equal(await hash(), sessionAddress)
  await page.goBack()
  await title('세션').waitFor()
  await sessionRow().waitFor()
  assert.equal(await hash(), '#sessions')
  await page.goForward()
  await page.goForward()
  await title('인사이트').waitFor()
  await card('규칙 판단 결과').waitFor()
  assert.equal(await hash(), '#insights')
  for (const name of ['세션 상세', '세션', '대시보드']) {
    await page.goBack()
    await title(name).waitFor()
  }
  await page.goBack()
  await traceOverview()
  await stays('#overview', SETTLE_MS)
  await page.goForward()
  await title('대시보드').waitFor()
  await kpis()
  await stays('#dashboard', SETTLE_MS)

  assert.deepEqual((await bridge()).native, [])
  assert.deepEqual(errors, [])
  console.log(
    JSON.stringify({
      browser: browser.version(),
      session: seed.SessionID,
      kpis: Object.fromEntries(KPIS),
      rules: Object.fromEntries(outcomes),
      usageCalls: (await bridge()).calls,
    }),
  )
} catch (e) {
  console.error(
    'usage view:',
    await hash().catch(() => ''),
    (await page.locator('body').innerText()).slice(0, 800),
    errors,
  )
  throw e
} finally {
  await browser.close()
}
