// Checks the default language of the built frontend and the language
// selector. Nothing is stored before the page loads, so the page starts in
// English; usage.mjs walks the same pages in depth in Korean. The bridge is
// fake and the Go harness (desktop/usage_ia_test.go) answers through the real
// usage pipeline, so the insight title below is generated in Go.
import assert from 'node:assert/strict'
import { chromium } from 'playwright'
import { bridgeState, installUsageBridge } from './bridge.mjs'
import { ENGLISH, KOREAN, storedLocale } from './locale.mjs'

const [origin] = process.argv.slice(2)
const headers = { 'X-Trace-Test': process.env.TRACE_TEST_TOKEN }
const seed = await fetch(origin + '/_seed', { headers }).then((r) => r.json())

// What the fixtures hold, in the words of the English pages.
const SESSION_TITLE = 'Fix failing build'
const KPIS = [
  ['Sessions', '1'],
  ['Prompts', '2'],
  ['Total tokens', '328.8K'],
  ['Cache hit ratio', '99.0%'],
  ['Tool calls', '6'],
  ['Cost', '$1.25'],
  ['Subagents', '1'],
  ['Compactions', '1'],
]
const USAGE_ENTRIES = [
  'Dashboard',
  'Sessions',
  'Tools and extensions',
  'Insights',
  'Extension events',
  'Collection settings',
]
const TRACE_ENTRIES = [
  'Live',
  'Overview',
  'Tasks',
  'Modules',
  'Sources and settings',
]
const SYSTEM_ITEMS = ['Turn end · 70.0 s', 'Context compaction (auto)']
// The published trace fixture the harness seeds, as in ia.mjs.
const TRACE_ATTEMPTS = 5
const TRACE_REVISION = 120

// The same places in Korean, after the selector switched the language.
const KOREAN_ENTRIES = { Dashboard: '대시보드', Insights: '인사이트' }

const browser = await chromium.launch({ channel: 'chrome', headless: true })
// A new context has empty storage: no language is stored.
const page = await browser.newPage({
  viewport: { width: 1280, height: 800 },
  reducedMotion: 'reduce',
})
page.setDefaultTimeout(6000)
const errors = []
page.on('pageerror', (e) => errors.push('page error: ' + e.message))
const BROWSER_ICON = origin + '/favicon.ico'
page.on('console', (m) => {
  if (m.type() === 'error' && m.location().url !== BROWSER_ICON)
    errors.push('console error: ' + m.text() + ' ' + m.location().url)
})

const exact = (text) =>
  new RegExp('^' + text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '$')
const title = (name) =>
  page.getByRole('heading', { name, exact: true, level: 1 })
const nav = (name) => page.getByRole('group', { name, exact: true })
const usageEntry = (name) =>
  nav('Usage').getByRole('button', { name, exact: true })
const card = (name) =>
  page.locator('section').filter({
    has: page.getByRole('heading', { name, exact: true, level: 2 }),
  })
const hash = () => page.evaluate(() => location.hash)
const lang = () => page.evaluate(() => document.documentElement.lang)
// The sidebar's selector is tied to its visible label.
const sidebarLanguage = (label) => page.getByLabel(label, { exact: true })
const timeline = page.getByRole('list', {
  name: 'Conversation timeline',
  exact: true,
})
const row = (kind, name) =>
  timeline
    .getByRole('listitem')
    .filter({ hasText: kind })
    .filter({ has: page.locator('code', { hasText: exact(name) }) })

async function openUsage(name, address) {
  await usageEntry(name).click()
  await title(name).waitFor()
  assert.equal(await hash(), address)
}

try {
  await installUsageBridge(page, headers)

  // 1. Without a stored choice the page is English, and says so.
  await page.goto(origin + '/')
  await title('Dashboard').waitFor()
  assert.equal(await lang(), ENGLISH)
  assert.equal(await storedLocale(page), null)
  for (const name of USAGE_ENTRIES) await usageEntry(name).waitFor()
  for (const name of TRACE_ENTRIES)
    await nav('Trace').getByRole('button', { name }).waitFor()
  const selector = sidebarLanguage('Language')
  assert.equal(await selector.inputValue(), ENGLISH)
  assert.deepEqual(await selector.locator('option').allInnerTexts(), [
    'English',
    '한국어',
  ])
  for (const [label, value] of KPIS)
    await page
      .locator('.u-dash-kpis .u-stat')
      .filter({ has: page.locator('.u-stat-label', { hasText: exact(label) }) })
      .locator('.u-stat-value', { hasText: exact(value) })
      .waitFor()

  // 2. Every usage page is worded in English.
  await openUsage('Sessions', '#sessions')
  await page
    .getByRole('status')
    .filter({ hasText: 'Sessions in this period 1' })
    .waitFor()
  await page.getByRole('button', { name: SESSION_TITLE, exact: true }).click()
  await title('Session details').waitFor()
  assert.equal(await hash(), '#session/' + seed.SessionID)
  await row('Tool call', 'Bash').waitFor()
  await row('Tool result', 'Bash').getByText('Error', { exact: true }).waitFor()
  await row('Hook', 'PreToolUse:Bash')
    .getByText('Success', { exact: true })
    .waitFor()
  for (const text of SYSTEM_ITEMS)
    await timeline
      .getByRole('listitem')
      .filter({ hasText: 'System' })
      .getByText(text, { exact: true })
      .waitFor()
  await page
    .getByRole('status')
    .filter({ hasText: '1 line in the part read could not be parsed' })
    .waitFor()
  await page.getByRole('tab', { name: /^Tools/ }).waitFor()

  await openUsage('Tools and extensions', '#tools')
  await page
    .getByRole('tabpanel', { name: 'Tools', exact: true })
    .getByText('server issues', { exact: true })
    .waitFor()

  await openUsage('Insights', '#insights')
  await card('Rule results').waitFor()
  const insight = page.getByRole('heading', {
    name: seed.InsightTitles[ENGLISH],
    exact: true,
  })
  await insight.waitFor()
  const report = JSON.parse((await bridgeState(page)).last.insights)
  assert.deepEqual(
    report.items.map((i) => i.title),
    [seed.InsightTitles[ENGLISH]],
  )

  await openUsage('Extension events', '#extensions')
  const receiver = card('Event receiver')
  await receiver.getByText('Off', { exact: true }).waitFor()
  await receiver
    .getByText('The address is set when the receiver is turned on.')
    .waitFor()
  assert.equal(
    await receiver
      .getByRole('switch', { name: 'Receive events' })
      .getAttribute('aria-checked'),
    'false',
  )
  assert.equal(
    await receiver.getByRole('button', { name: 'Copy settings' }).isDisabled(),
    true,
  )

  await openUsage('Collection settings', '#settings')
  const watch = card('Collection')
  await watch.getByText('On', { exact: true }).waitFor()
  await watch.getByRole('button', { name: 'Stop collection' }).waitFor()
  await watch.getByText(seed.RootLabel, { exact: true }).waitFor()
  await card('Projects')
    .getByRole('status')
    .filter({ hasText: '1 project, 0 excluded' })
    .waitFor()
  assert.equal(
    await card('Language').getByLabel('Display language').inputValue(),
    ENGLISH,
  )

  // 3. The trace workspace is English too.
  await nav('Trace').getByRole('button', { name: 'Overview' }).click()
  await page.getByRole('heading', { name: 'Activity over time' }).waitFor()
  await page.waitForFunction(
    ({ attempts, revision }) =>
      document
        .querySelector('.eyebrow')
        ?.textContent.replace(/\s+/g, ' ')
        .trim() ===
        'Live / #' + revision &&
      [...document.querySelectorAll('.metric')].some(
        (c) =>
          c.querySelector('span')?.textContent === 'Tool attempts' &&
          c.querySelector('strong')?.textContent === String(attempts),
      ),
    { attempts: TRACE_ATTEMPTS, revision: TRACE_REVISION },
  )

  // 4. The sidebar selector switches the page to Korean at once. The
  // insight title comes back from Go in Korean, so the request carried it.
  await usageEntry('Insights').click()
  await insight.waitFor()
  await selector.selectOption(KOREAN)
  await title(KOREAN_ENTRIES.Insights).waitFor()
  await page
    .getByRole('heading', { name: seed.InsightTitles[KOREAN], exact: true })
    .waitFor()
  await nav('사용 분석')
    .getByRole('button', { name: KOREAN_ENTRIES.Dashboard, exact: true })
    .waitFor()
  assert.equal(await lang(), KOREAN)
  assert.equal(await storedLocale(page), KOREAN)
  assert.equal(await hash(), '#insights')
  assert.equal(await sidebarLanguage('언어').inputValue(), KOREAN)
  assert.ok((await bridgeState(page)).locales.includes(KOREAN))

  // 5. The choice survives a reload.
  await page.reload()
  await title(KOREAN_ENTRIES.Insights).waitFor()
  await page
    .getByRole('heading', { name: seed.InsightTitles[KOREAN], exact: true })
    .waitFor()
  assert.equal(await lang(), KOREAN)
  assert.equal(await sidebarLanguage('언어').inputValue(), KOREAN)
  // The page reports the stored language to the desktop process at start.
  assert.deepEqual((await bridgeState(page)).locales, [KOREAN])

  // 6. And switching back gives the English page again.
  await sidebarLanguage('언어').selectOption(ENGLISH)
  await title('Insights').waitFor()
  await insight.waitFor()
  assert.equal(await storedLocale(page), ENGLISH)

  assert.deepEqual((await bridgeState(page)).native, [])
  assert.deepEqual(errors, [])
  console.log(
    JSON.stringify({
      browser: browser.version(),
      session: seed.SessionID,
      insight: seed.InsightTitles,
      locales: (await bridgeState(page)).locales,
    }),
  )
} catch (e) {
  console.error(
    'english view:',
    await hash().catch(() => ''),
    (await page.locator('body').innerText()).slice(0, 800),
    errors,
  )
  throw e
} finally {
  await browser.close()
}
