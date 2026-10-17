import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { access, mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises'
import { resolve } from 'node:path'
import { build, preview } from 'vite'
import react from '@vitejs/plugin-react'
import { chromium } from 'playwright'

// Opt-in conventional browser tests: no computer-use service, credentials or model calls.
const enabled = process.env.RUN_MARKET_E2E === '1'
let directory, server, browser, baseURL
before(async () => {
  if (!enabled) return
  await mkdir(resolve('node_modules/.tmp'), { recursive: true })
  directory = await mkdtemp(resolve('node_modules/.tmp/market-browser-'))
  await writeFile(`${directory}/index.html`, '<div id="root"></div><script type="module" src="/entry.tsx"></script>')
  await writeFile(`${directory}/entry.tsx`, `import { createRoot } from 'react-dom/client'; import { MarketPage } from ${JSON.stringify(resolve('src/pages/MarketPage.tsx'))}; createRoot(document.getElementById('root')!).render(<MarketPage />);`)
  await build({ configFile: false, root: directory, plugins: [react()], logLevel: 'error' })
  server = await preview({ configFile: false, root: directory, preview: { host: '127.0.0.1', port: 0, open: false } })
  baseURL = `http://127.0.0.1:${server.httpServer.address().port}`
  const candidates = [process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE, 'C:/Program Files/Google/Chrome/Application/chrome.exe', 'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe'].filter(Boolean)
  let executablePath
  for (const candidate of candidates) { try { await access(candidate); executablePath = candidate; break } catch { /* Try another installed browser. */ } }
  browser = await chromium.launch({ headless: true, ...(executablePath ? { executablePath } : {}) })
})
after(async () => {
  await browser?.close()
  if (server) await new Promise(resolve => server.httpServer.close(resolve))
  if (directory) await rm(directory, { recursive: true, force: true })
})

const raw = '网页导航 广告\n甲公司招聘 Java 开发实习生\n负责交易接口开发。要求 Java、MySQL。\n乙公司招聘前端工程师\n负责页面开发。要求 React。'
const session = { id: 'material', status: 'ready', candidates: [{ id: 'backend', title: 'Java 开发实习生', company: '甲公司', raw_text: '负责交易接口开发。要求 Java、MySQL。' }, { id: 'frontend', title: '前端工程师', company: '乙公司', raw_text: '负责页面开发。要求 React。' }], has_pending: false }
const processing = { id: 'jd', title: 'Java 开发实习生', company: '甲公司', status: 'processing', raw_text: session.candidates[0].raw_text, responsibilities: ['负责交易接口开发。'], ability_mentions: [], ability_levels: [], job_status: 'running', validation_status: 'valid', created_at: '2026-10-17T00:00:00Z', updated_at: '2026-10-17T00:00:00Z', ability_review: { pending_count: 0, failed_count: 0 }, ability_grading: { status: 'not_started' }, analysis: { version: 'v', has_pending: true, next_poll_ms: 2000, stages: [{ name: 'extraction', status: 'succeeded', attempts: 1 }, { name: 'classification', status: 'running', attempts: 1 }] } }
const completed = { ...processing, status: 'included', job_status: 'succeeded', primary_category: '后端开发', secondary_category: '业务后端', ability_mentions: [{ name: 'MySQL', evidence: '要求 Java、MySQL。' }], ability_levels: [{ ability_id: 'mysql', name: 'MySQL', level: 2, source: 'explicit', requirement_kind: 'required', evidence: '要求 Java、MySQL。', reason: '要求掌握', confidence: 0.9 }], ability_grading: { status: 'succeeded' }, analysis: { ...processing.analysis, has_pending: false, stages: processing.analysis.stages.map(stage => ({ ...stage, status: 'succeeded' })) } }
const level = { common_levels: [2], recommended_level: 2, sample_count: 1, explicit_count: 1, inferred_count: 0, distribution: [{ level: 2, count: 1 }], higher_requirement_count: 0, status: 'ready', evidences: [] }
function profile(done) { return { included_jd_count: done ? 1 : 0, required_jd_count: 10, complete: false, ability_grading_pending_count: 0, ability_grading_failed_count: 0, abilities: done ? [{ ability_id: 'mysql', name: 'MySQL', category: '数据库', covered_jd_count: 1, evidences: [], target_level: 2, level_summary: level, preferred_level_summary: { ...level, sample_count: 0 } }] : [] } }
const respond = (route, data, status = 200) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify({ data }) })

test('noisy material selection and background completion update the list, open detail, progress and abilities', { skip: !enabled, timeout: 30000 }, async () => {
  const context = await browser.newContext()
  try {
    const page = await context.newPage()
    let imports = [], formal = false, done = false, submitted = 0, confirmed = 0, listReads = 0
    const failures = []
    page.on('pageerror', error => failures.push(error.message))
    await page.route('**/api/v1/**', async route => {
      const request = route.request(), path = new URL(request.url()).pathname
      if (path === '/api/v1/jd-imports' && request.method() === 'POST') {
        assert.equal(request.postDataJSON().raw_text, raw)
        submitted++
        imports = [session]
        return respond(route, session, 202)
      }
      if (path.endsWith('/confirm')) {
        assert.deepEqual(request.postDataJSON().candidate_ids, ['backend'])
        confirmed++; formal = true; imports = []
        return respond(route, { ...session, status: 'confirmed' })
      }
      if (path === '/api/v1/jd-imports') return respond(route, imports)
      if (path === '/api/v1/jds') { listReads++; return respond(route, formal ? [done ? completed : processing] : []) }
      if (path === '/api/v1/market-profile') return respond(route, profile(done))
      throw new Error(`Unexpected API ${path}`)
    })
    await page.goto(baseURL)
    await page.getByRole('button', { name: '添加 JD', exact: true }).click()
    await page.getByLabel('招聘材料', { exact: true }).fill(raw)
    await page.getByRole('button', { name: '保存并识别岗位' }).click()
    await page.getByRole('heading', { name: '识别到 2 个岗位，请选择' }).waitFor()
    assert.equal(await page.getByRole('checkbox', { checked: true }).count(), 0)
    assert.equal(await page.getByRole('button', { name: '确认导入 0 份' }).isDisabled(), true)
    assert.equal(formal, false)
    await page.getByRole('checkbox').first().check()
    await page.getByRole('button', { name: '确认导入 1 份' }).click()
    await page.locator('.jd-row').click()
    const detail = page.getByRole('dialog')
    assert.match(await detail.innerText(), /负责交易接口开发。/)
    assert.match(await detail.innerText(), /岗位分类：处理中/)
    done = true // A worker completes independently, without navigating or clicking refresh.
    await page.locator('.market-progress strong').filter({ hasText: '1 / 10' }).waitFor()
    await detail.getByText('岗位分类：已完成', { exact: true }).waitFor()
    assert.match(await detail.innerText(), /MySQL/)
    assert.match(await detail.innerText(), /L2/)
    await page.getByRole('button', { name: '关闭抽屉' }).click()
    assert.match(await page.locator('.jd-row').innerText(), /计入画像/)
    await page.getByRole('tab', { name: '岗位能力 1' }).click()
    await page.locator('.ability-row').getByText('MySQL', { exact: true }).waitFor()
    assert.equal(submitted, 1); assert.equal(confirmed, 1); assert.ok(listReads >= 3)
    assert.deepEqual(failures, [])
  } finally { await context.close() }
})

test('a late polling response cannot overwrite newer state; returning to the page refreshes it', { skip: !enabled, timeout: 30000 }, async () => {
  const context = await browser.newContext()
  try {
    const page = await context.newPage()
    let done = false, hold = false, heldRoute, signalHeld
    const held = new Promise(resolve => { signalHeld = resolve })
    await page.route('**/api/v1/**', async route => {
      const path = new URL(route.request().url()).pathname
      if (path === '/api/v1/jds') {
        if (hold) { hold = false; heldRoute = route; signalHeld(); return }
        return respond(route, [done ? completed : processing])
      }
      if (path === '/api/v1/jd-imports') return respond(route, [])
      if (path === '/api/v1/market-profile') return respond(route, profile(done))
      throw new Error(`Unexpected API ${path}`)
    })
    await page.goto(baseURL)
    await page.locator('.jd-row').waitFor()
    hold = true
    await page.evaluate(() => window.dispatchEvent(new Event('focus')))
    await held
    done = true
    await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')))
    await page.locator('.market-progress strong').filter({ hasText: '1 / 10' }).waitFor()
    const received = page.waitForResponse(response => new URL(response.url()).pathname === '/api/v1/jds')
    await respond(heldRoute, [processing])
    await (await received).finished()
    // Observe a rendering boundary after all three old responses have settled.
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
    assert.match(await page.locator('.market-progress').innerText(), /1 \/ 10/)
    assert.match(await page.locator('.jd-row').innerText(), /计入画像/)
  } finally { await context.close() }
})

test('novel text stays a normal empty result without creating a formal JD', { skip: !enabled, timeout: 30000 }, async () => {
  const context = await browser.newContext()
  try {
    const page = await context.newPage()
    await page.route('**/api/v1/**', route => {
      const path = new URL(route.request().url()).pathname
      if (path === '/api/v1/jds') return respond(route, [])
      if (path === '/api/v1/jd-imports') return respond(route, [{ ...session, status: 'empty', candidates: [] }])
      if (path === '/api/v1/market-profile') return respond(route, profile(false))
      throw new Error(`Unexpected API ${path}`)
    })
    await page.goto(baseURL)
    await page.getByRole('heading', { name: '未识别到有效 JD' }).waitFor()
    assert.equal(await page.getByRole('checkbox').count(), 0)
    assert.equal(await page.getByRole('button', { name: '重新识别' }).count(), 0)
    assert.equal(await page.locator('.jd-row').count(), 0)
  } finally { await context.close() }
})

test('a failed classification keeps extracted content visible and offers a stage retry', { skip: !enabled, timeout: 30000 }, async () => {
  const context = await browser.newContext()
  try {
    const page = await context.newPage()
    let retried = false
    const failed = { ...processing, status: 'failed', job_status: 'failed', job_error_code: 'classification_unresolved', ability_mentions: completed.ability_mentions, analysis: { ...processing.analysis, has_pending: false, stages: [{ name: 'extraction', status: 'succeeded', attempts: 1 }, { name: 'classification', status: 'failed', error_code: 'classification_unresolved', attempts: 3 }] } }
    await page.route('**/api/v1/**', route => {
      const path = new URL(route.request().url()).pathname
      if (path === '/api/v1/jds/jd/retry') { retried = true; return respond(route, { ...processing, ability_mentions: completed.ability_mentions }) }
      if (path === '/api/v1/jds') return respond(route, [failed])
      if (path === '/api/v1/jd-imports') return respond(route, [])
      if (path === '/api/v1/market-profile') return respond(route, profile(false))
      throw new Error(`Unexpected API ${path}`)
    })
    await page.goto(baseURL)
    await page.locator('.jd-row').click()
    const detail = page.getByRole('dialog')
    assert.match(await detail.innerText(), /负责交易接口开发。/)
    assert.match(await detail.innerText(), /MySQL/)
    assert.match(await detail.innerText(), /岗位分类未完成/)
    await detail.getByRole('button', { name: '重新分析', exact: true }).click()
    await detail.getByText('岗位分类：处理中', { exact: true }).waitFor()
    assert.equal(retried, true)
    assert.match(await detail.innerText(), /负责交易接口开发。/)
    assert.match(await detail.innerText(), /MySQL/)
    assert.match(await detail.innerText(), /内容提取：已完成/)
  } finally { await context.close() }
})
