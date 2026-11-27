import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { chromium } from 'playwright'
import { KOREAN, pinLocale } from './locale.mjs'

const [mode, origin] = process.argv.slice(2)
const headers = { 'X-Trace-Test': process.env.TRACE_TEST_TOKEN }
const browser = await chromium.launch({
  channel: 'chrome',
  headless: true,
  args: [
    '--disable-background-timer-throttling',
    '--disable-renderer-backgrounding',
  ],
})
try {
  const page = await browser.newPage({
    viewport: { width: 1280, height: 800 },
    reducedMotion: 'reduce',
  })
  const errors = []
  page.on('pageerror', (e) => errors.push(e.message))
  await pinLocale(page, KOREAN)
  await page.addInitScript(
    ({ headers }) => {
      window.go = {
        main: {
          Bridge: {
            Bootstrap: () =>
              fetch('/_ticket', { headers }).then((r) => r.json()),
            Pick: () => Promise.resolve(''),
          },
        },
      }
      window.samples = []
      let last = -1
      new MutationObserver(() => {
        const banner = document.querySelector('.eyebrow')?.textContent
        const match = banner?.match(/#(\d+)/)
        if (!match) return
        const rev = Number(match[1])
        if (rev <= last) return
        last = rev
        requestAnimationFrame(() =>
          requestAnimationFrame(() =>
            window.samples.push({
              rev,
              at: performance.timeOrigin + performance.now(),
            }),
          ),
        )
      }).observe(document, {
        subtree: true,
        childList: true,
        characterData: true,
      })
    },
    { headers },
  )
  await page.goto(origin + (mode === 'read' ? '#overview' : '#sources'))
  try {
    await page.waitForFunction(
      () => window.samples.length > 0,
      {},
      { timeout: 30000 },
    )
  } catch (e) {
    console.error('render', await page.locator('body').innerText(), errors)
    throw e
  }
  if (mode === 'smoke') {
    await page.locator('nav button').filter({ hasText: '출처와 설정' }).click()
    await page.getByRole('heading', { name: '연결한 출처' }).waitFor()
  }
  if (mode === 'read') {
    const documents = Object.fromEntries(
      await Promise.all(
        ['watch', 'overview', 'attempts', 'modules'].map(async (name) => [
          name,
          await readFile(
            new URL(
              '../../../internal/transport/operations/' + name + '.graphql',
              import.meta.url,
            ),
            'utf8',
          ),
        ]),
      ),
    )
    const result = await page.evaluate(async (docs) => {
      const { url, ticket } = await window.go.main.Bridge.Bootstrap()
      const ws = new WebSocket(url, 'graphql-transport-ws')
      let id = 0
      const handlers = new Map()
      await new Promise((resolve, reject) => {
        ws.onerror = () => reject(Error('connect'))
        ws.onopen = () =>
          ws.send(
            JSON.stringify({ type: 'connection_init', payload: { ticket } }),
          )
        ws.onmessage = (e) => {
          const m = JSON.parse(e.data)
          if (m.type === 'connection_ack') resolve()
          if (m.type === 'next' || m.type === 'error') handlers.get(m.id)?.(m)
        }
      })
      const query = (name, variables) =>
        new Promise((resolve, reject) => {
          const key = String(++id)
          const timeout = setTimeout(
            () => reject(Error(name + ' timed out')),
            90000,
          )
          handlers.set(key, (m) => {
            clearTimeout(timeout)
            handlers.delete(key)
            if (name === 'watch')
              ws.send(JSON.stringify({ id: key, type: 'complete' }))
            if (m.type === 'error' || m.payload.errors)
              reject(Error(name + ' ' + JSON.stringify(m.payload)))
            else resolve(m.payload.data[name])
          })
          ws.send(
            JSON.stringify({
              id: key,
              type: 'subscribe',
              payload: { query: docs[name], operationName: name, variables },
            }),
          )
        })
      try {
        const until = new Date(),
          from = new Date(+until - 86400000)
        const filter = {
          interval: { from: from.toISOString(), until: until.toISOString() },
        }
        const snap = await query('watch', {
          input: {
            scope: { filter, mode: 'LIVE', liveWindow: 'FOLLOW_END' },
            selection: { kind: 'OVERVIEW' },
          },
        })
        const scope = {
          point: snap.stamp.point,
          cohort: snap.stamp.cohort,
          filter: { interval: snap.stamp.effectiveInterval },
        }
        const modules = await query('modules', { scope, page: { first: 1 } })
        const m = modules.data.items[0]
        if (!m) throw Error('missing tool instance')
        const args = {
          scope,
          module: { taskId: m.taskId, id: m.id },
          page: { first: 50 },
        }
        const rounds = { overview: [], attempts: [] }
        for (let i = 0; i <= 30; i++) {
          for (const name of ['overview', 'attempts']) {
            const start = performance.now(),
              result = await query(
                name,
                name === 'overview' ? { scope } : args,
              ),
              elapsed = performance.now() - start
            if (result.stamp.point.revision !== scope.point.revision)
              throw Error('point changed')
            if (name === 'overview' && result.data.attempts.count !== 49000)
              throw Error(
                'unexpected global count ' + result.data.attempts.count,
              )
            if (name === 'attempts' && result.data.page.total !== 49)
              throw Error('unexpected tool count ' + result.data.page.total)
            if (i > 0) rounds[name].push(elapsed)
          }
        }
        return {
          revision: scope.point.revision,
          overviewP95: rounds.overview.sort((a, b) => a - b)[28],
          attemptsP95: rounds.attempts.sort((a, b) => a - b)[28],
        }
      } finally {
        ws.close()
      }
    }, documents)
    console.log('fixed queries', JSON.stringify(result))
    assert.ok(result.overviewP95 <= 1000 && result.attemptsP95 <= 1000)
  } else {
    await page.evaluate((headers) => fetch('/_start', { headers }), headers)
    let rows
    for (;;) {
      const resp = await fetch(origin + '/_samples', { headers })
      if (resp.ok && resp.status !== 202) {
        rows = await resp.json()
        break
      }
      await new Promise((resolve) =>
        setTimeout(resolve, mode === 'smoke' ? 100 : 1000),
      )
    }
    rows.sort((a, b) => a.Rev - b.Rev)
    const last = rows.at(-1).Rev
    await page.waitForFunction(
      (last) => window.samples.at(-1)?.rev >= last,
      last,
      { timeout: 60000 },
    )
    const frames = await page.evaluate(() =>
      window.samples.slice().sort((a, b) => a.rev - b.rev),
    )
    let k = 0
    const delays = rows
      .map(({ Rev, Ready }) => {
        while (frames[k].rev < Rev) k++
        return frames[k].at - Ready
      })
      .sort((a, b) => a - b)
    const p95 = delays[Math.ceil(0.95 * delays.length) - 1]
    console.log(
      'readable line to painted source screen',
      JSON.stringify({
        lines: rows.length,
        frames: frames.length,
        p95Ms: p95,
        maxMs: delays.at(-1),
        browser: browser.version(),
      }),
    )
    assert.ok(p95 >= 0 && p95 <= 2000)
    if (mode === 'smoke') {
      await page
        .getByRole('button', { name: '개요', exact: false })
        .first()
        .click()
      await page.getByRole('heading', { name: '시간별 활동' }).waitFor()
      await page.getByRole('button', { name: '시점 고정' }).click()
      await page.getByRole('button', { name: '고정 해제' }).waitFor()
      await page
        .getByRole('button', { name: '작업 같은 표본 열기', exact: false })
        .first()
        .click({ timeout: 3000 })
        .catch(async () => page.locator('.metric').first().click())
      await page.getByRole('heading', { name: '작업 목록' }).waitFor()
      await page.locator('.row button').first().click()
      await page.getByRole('button', { name: '분기 시퀀스' }).click()
      await page
        .getByRole('heading', { name: '작업 시퀀스', exact: true })
        .waitFor()
    }
  }
  assert.deepEqual(errors, [])
} finally {
  await browser.close()
}
