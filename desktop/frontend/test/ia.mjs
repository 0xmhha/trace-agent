import assert from 'node:assert/strict'
import { chromium } from 'playwright'

const [mode, origin] = process.argv.slice(2)
const headers = { 'X-Trace-Test': process.env.TRACE_TEST_TOKEN }
const seed = await fetch(origin + '/_seed', { headers }).then((r) => r.json())
const short = (id) =>
  id.length > 18 ? id.slice(0, 8) + '…' + id.slice(-4) : id
const browser = await chromium.launch({ channel: 'chrome', headless: true })
const page = await browser.newPage({
  viewport: { width: 1100, height: 620 },
  reducedMotion: 'reduce',
})
page.setDefaultTimeout(6000)
const errors = []
page.on('pageerror', (e) => errors.push(e.message))
const heading = (name) => page.getByRole('heading', { name, exact: true })
const button = (name) => page.getByRole('button', { name, exact: true })
const panel = (name) => page.locator('section').filter({ has: heading(name) })
async function metric(name, value) {
  await page.waitForFunction(
    ({ name, value }) =>
      [...document.querySelectorAll('.metric')].some(
        (c) =>
          c.querySelector('span')?.textContent === name &&
          c.querySelector('strong')?.textContent === String(value),
      ),
    { name, value },
  )
}
async function revision(n, frozen) {
  await page.waitForFunction(
    ({ n, frozen }) =>
      document
        .querySelector('.eyebrow')
        ?.textContent.replace(/\s+/g, ' ')
        .trim() ===
      (frozen ? '고정' : '실시간') + ' / #' + n,
    { n, frozen },
  )
}
async function change(path) {
  const r = await fetch(origin + '/' + path, { method: 'POST', headers })
  assert.equal(r.status, 200)
  return r.json()
}
async function fixedStatus(rev) {
  const latest = await change('_change')
  assert.ok(latest.Revision > rev)
  await page
    .getByText('새 자료가 있습니다.', { exact: false })
    .first()
    .waitFor()
  await revision(rev, true)
  await page
    .getByText('저장 리비전 ' + latest.Revision, { exact: true })
    .waitFor()
}
async function erase(rev, oldSelector) {
  const point = await change('_erase')
  assert.ok(point.Epoch > 1)
  await page.waitForFunction(
    ({ rev, oldSelector }) =>
      !document.querySelector('.eyebrow')?.textContent.includes('#' + rev) &&
      ![...document.querySelectorAll('h2')].some(
        (h) => h.textContent === oldSelector,
      ),
    { rev, oldSelector },
  )
  await page
    .getByText('저장 리비전 ' + point.Revision, { exact: true })
    .waitFor()
  await revision(point.Revision, false)
  assert.equal(await heading(oldSelector).count(), 0)
}
try {
  await page.addInitScript(
    ({ headers }) => {
      window.go = {
        main: {
          Bridge: {
            Bootstrap: () =>
              fetch('/_ticket', { headers }).then((r) => r.json()),
            Pick: async () => '',
            HasReceiver: async () => false,
          },
        },
      }
    },
    { headers },
  )
  if (mode === 'IA120') {
    await page.goto(origin + '/#overview')
    await revision(120, false)
    await metric('도구 시도', 5)
    await metric('응답 대기', 1)
    await metric('실패율', '25%')
    await metric('하위 에이전트', 4)
    await page.locator('button.metric').filter({ hasText: '응답 대기' }).click()
    await heading('작업 목록').waitFor()
    await revision(120, true)
    await button(short(seed.IDs.A)).click()
    await heading('작업 상세').waitFor()
    await button('이 작업의 통계 보기').click()
    await metric('도구 시도', 5)
    await button('작업 필터 해제 · ' + short(seed.IDs.A)).waitFor()
    await button('이전 화면').click()
    await heading('작업 상세').waitFor()
    await revision(120, true)
    const modules = button('작업 표본의 모듈')
    await modules.scrollIntoViewIfNeeded()
    const scroll = await page.locator('main').evaluate((m) => m.scrollTop)
    assert.ok(scroll > 0, 'fixture should require scrolling')
    await modules.click()
    await heading('모듈 목록').waitFor()
    const tool = panel('모듈 목록')
      .locator('.row')
      .filter({ hasText: 'MCP 도구' })
    assert.equal(await tool.count(), 1)
    await tool.getByRole('button').click()
    await heading('연결 모듈').waitFor()
    const tries = panel('시도').locator('.row')
    assert.equal(await tries.count(), 1)
    const expected = '시도 ' + short(seed.IDs['attempt-5'])
    await tries.getByRole('button', { name: expected, exact: true }).click()
    await heading(expected).waitFor()
    const proof = panel('전달 과정')
      .getByRole('button', { name: /^근거 / })
      .first()
    const evidence = await proof.innerText()
    await proof.click()
    await heading(evidence).waitFor()
    const observed = await page.locator('dl').innerText()
    // Browser history must restore the same sample and scroll position.
    for (const title of [expected, '연결 모듈', '모듈 목록', '작업 상세']) {
      await button('이전 화면').click()
      await heading(title).waitFor()
    }
    await page.waitForFunction(
      (scroll) =>
        Math.abs(document.querySelector('main').scrollTop - scroll) < 3,
      scroll,
    )
    await button('도구 시도').click()
    await heading('호출 시도').first().waitFor()
    const direct = page.locator('.row').filter({ has: button(expected) })
    await direct.first().waitFor()
    assert.equal(await direct.count(), 1)
    await direct.getByRole('button', { name: expected, exact: true }).click()
    await panel('근거 기록').getByRole('button').first().click()
    await heading(evidence).waitFor()
    assert.equal(await page.locator('dl').innerText(), observed)
    await fixedStatus(120)
    await erase(120, evidence)
    console.log(
      JSON.stringify({
        browser: browser.version(),
        revision: 120,
        task: 1,
        attempts: 5,
        children: 4,
        waiting: 1,
        failureRate: 0.25,
        evidence,
        scroll,
      }),
    )
  } else if (mode === 'sequence') {
    await page.goto(origin + '/#live/' + seed.IDs.L7)
    const select = async (label, counts) => {
      const e = page.locator(
        'button.flowEvent[aria-label$="' + short(seed.IDs[label]) + '"]',
      )
      await e.click()
      await metric('하위 에이전트', counts[0])
      await metric('실행 중', counts[1])
      await metric('응답 대기', counts[2])
    }
    await heading('참여자와 사건').waitFor()
    await page.waitForFunction(() =>
      document.querySelector('.eyebrow')?.textContent.includes('고정'),
    )
    for (const [label, counts] of [
      ['L7', [3, 3, 2]],
      ['L8', [3, 3, 1]],
      ['L9', [3, 2, 1]],
      ['L16', [3, 0, 0]],
      ['L7', [3, 3, 2]],
    ])
      await select(label, counts)
    const join = page.locator(
      'button.flowEvent[aria-label$="' + short(seed.IDs.L15) + '"]',
    )
    await join.click()
    const inputs = panel('선택한 사건 · 합류')
      .locator('.row')
      .filter({ hasText: /JOIN_INPUT|합류 입력/ })
    await inputs.nth(1).waitFor()
    assert.equal(await inputs.count(), 2)
    assert.ok(
      (await inputs.allInnerTexts()).some((s) =>
        s.includes(short(seed.IDs.L11)),
      ),
    )
    assert.ok(
      (await inputs.allInnerTexts()).some((s) =>
        s.includes(short(seed.IDs.L14)),
      ),
    )
    await select('L7', [3, 3, 2])
    await fixedStatus(seed.Point.Revision)
    await metric('응답 대기', 2)
    await erase(seed.Point.Revision, '참여자와 사건')
    await page.goto(origin + '/?case=unavailable#live/missing-task')
    await page
      .getByRole('status')
      .filter({ hasText: '현재 범위에서 찾지 못했습니다' })
      .waitFor()
    await heading('진행 중인 작업').waitFor()
    console.log(
      'L7→L8→L9→L16→L7 passed through production React, frozen and epoch reset',
    )
  } else if (mode === 'delete') {
    await page.goto(origin + '/#sources')
    await button('Claude Code · claude-transcript').click()
    const del = panel('삭제')
    const target = del.getByLabel('삭제 대상')
    const preview = del.getByRole('button', { name: '삭제 미리보기' })
    const confirm = del.getByRole('button', { name: '이 범위 삭제' })
    await target.selectOption('SESSION')
    assert.equal(await preview.isDisabled(), true)
    await del.getByLabel('세션 ID').selectOption(seed.IDs.one)
    await preview.click()
    await del
      .getByText('범위에 포함된 근거 2건. 삭제할 근거 0건, 본문 2건.')
      .waitFor()
    await del.getByRole('button', { name: '취소' }).click()
    assert.equal(await confirm.count(), 0)
    await preview.click()
    await confirm.waitFor()
    await target.selectOption('TASK')
    assert.equal(await confirm.count(), 0)
    assert.equal(await preview.isDisabled(), true)
    await del.getByLabel('작업 ID').selectOption(seed.IDs.task0)
    await preview.click()
    await del
      .getByText('범위에 포함된 근거 1건. 삭제할 근거 0건, 본문 1건.')
      .waitFor()
    await del.getByLabel('삭제할 자료').selectOption('ALL')
    assert.equal(await confirm.count(), 0)
    await preview.click()
    await del
      .getByText('범위에 포함된 근거 1건. 삭제할 근거 1건, 본문 1건.')
      .waitFor()
    await confirm.click()
    await revision(seed.Point.Revision + 1, false)
    console.log(
      'scoped previews, receipt invalidation, cancellation and one-task deletion passed',
    )
  } else if (mode === 'source') {
    await page.goto(origin + '/#sources')
    await panel('연결한 출처')
      .getByRole('button', { name: /claude-otel/i })
      .click()
    await button('출처 범위 모델 시도 조회').click()
    const models = panel('모델 요청').locator('.row')
    await models.first().waitFor()
    assert.equal(await models.count(), 1)
    await models.getByRole('button').click()
    await panel('모델 요청 본문')
      .getByText('C one request sample', { exact: true })
      .waitFor()
    await panel('모델 응답 본문')
      .getByText('C one response sample', { exact: true })
      .waitFor()
    assert.equal(await panel('모델 전달 과정').locator('.row').count(), 2)
    assert.equal(await panel('모델 근거 기록').locator('.row').count(), 2)
    assert.equal(await page.getByText('U one unlinked sample').count(), 0)
    const responseId = seed.IDs['C1-response']
    await panel('모델 전달 과정')
      .getByRole('button', { name: '근거 ' + responseId, exact: true })
      .click()
    await heading('근거 ' + responseId).waitFor()
    await page.getByText('C one response sample', { exact: true }).waitFor()
    await revision(seed.Point.Revision, true)
    assert.deepEqual(
      await page.evaluate(() => history.state.fixed.filter.sources),
      [seed.Source],
    )
    await button('이전 화면').click()
    await button('미연계 근거 1건').click()
    const unlinked = panel('미연계 근거').locator('.row')
    await unlinked.first().waitFor()
    assert.equal(await unlinked.count(), 1)
    await unlinked.getByRole('button').click()
    await heading('근거 ' + seed.IDs.U1).waitFor()
    await revision(seed.Point.Revision, true)
    assert.deepEqual(
      await page.evaluate(() => history.state.fixed.filter.sources),
      [seed.Source],
    )
    await page.getByText('U one unlinked sample', { exact: true }).waitFor()
    assert.equal(await page.getByText('C one response sample').count(), 0)
    assert.equal(
      await page
        .locator('dl dd')
        .getByText(short(seed.Source), { exact: true })
        .count(),
      1,
    )
    console.log(
      'C1 request/response, both edges and scoped evidence; U1 isolated body and scoped evidence passed',
    )
  }
  assert.deepEqual(errors, [])
} catch (e) {
  console.error(
    'IA view:',
    (await page.locator('body').innerText()).slice(0, 600),
    errors,
  )
  throw e
} finally {
  await browser.close()
}
