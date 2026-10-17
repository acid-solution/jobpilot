// Explicit opt-in production acceptance using only an independent example.test account
// and the already authorized corpus. No credentials, tokens or raw material are logged.
import assert from 'node:assert/strict'
import { readFile, access } from 'node:fs/promises'
import { chromium } from 'playwright'

assert.equal(process.env.RUN_MARKET_LIVE, '1', 'Set RUN_MARKET_LIVE=1 for real model calls')
const account = JSON.parse(await readFile(process.env.JOBPILOT_TEST_ACCOUNT_FILE, 'utf8'))
const corpus = JSON.parse(await readFile(process.env.JOBPILOT_TEST_CORPUS_FILE, 'utf8'))
const base = process.env.JOBPILOT_BASE_URL
assert.ok(base && account.user_id && account.email.endsWith('@example.test'), 'Independent test account required')
const fixtures = Object.fromEntries(corpus.Cases.map(item => [item.Name, item.Raw]))
assert.ok(fixtures.page_java && fixtures.three_jobs)
let executablePath
for (const candidate of [process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE, 'C:/Program Files/Google/Chrome/Application/chrome.exe', 'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe'].filter(Boolean)) {
  try { await access(candidate); executablePath = candidate; break } catch { /* Try another installed browser. */ }
}
const browser = await chromium.launch({ headless: true, ...(executablePath ? { executablePath } : {}) })
let page
try {
  const context = await browser.newContext({ viewport: { width: 1600, height: 1000 } })
  page = await context.newPage()
  page.setDefaultTimeout(120000)
  const errors = []
  page.on('pageerror', error => errors.push(error.message))
  await page.goto(base)
  await page.getByLabel('邮箱', { exact: true }).fill(account.email)
  await page.getByLabel('密码', { exact: true }).fill(account.password)
  const loginResponse = page.waitForResponse(response => new URL(response.url()).pathname === '/auth/v1/login')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  const loginBody = await (await loginResponse).json(), login = loginBody.data ?? loginBody
  assert.equal(login.account.user.id, account.user_id, 'Unexpected account identity')
  console.log('LIVE_UI_LOGIN_OK independent_test_account=true')
  const api = async (method, path, data) => {
    const response = await context.request.fetch(new URL(path, base).href, { method, data, headers: { Authorization: `Bearer ${login.access_token}` } })
    assert.ok(response.ok(), `${method} ${path}: ${response.status()}`)
    if (response.status() === 204) return
    const body = await response.json()
    return body.data ?? body
  }
  // Reset only the previously created, precisely identified synthetic Java fixture.
  // This lets the same authorized text exercise real completion again.
  if (process.env.RESET_MARKET_SYNTHETIC_FIXTURE === '1') {
    const items = await api('GET', '/api/v1/jds')
    for (const item of items) {
      if (item.company === '蓝鲸科技' && item.title.includes('Java') && item.raw_text.includes('负责电商服务接口开发和数据库调优。') && item.raw_text.includes('Spring Boot')) {
        await api('DELETE', `/api/v1/jds/${item.id}`)
      }
    }
  }
  await page.getByRole('button', { name: '市场画像', exact: true }).click()
  const minimize = page.getByRole('button', { name: '收起助手', exact: true })
  if (await minimize.isVisible()) await minimize.click()
  console.log('LIVE_UI_MARKET_OPENED')
  const paste = async raw => {
    await page.getByRole('button', { name: '添加 JD', exact: true }).click()
    await page.getByLabel('招聘材料', { exact: true }).fill(raw)
    const saved = page.waitForResponse(response => new URL(response.url()).pathname === '/api/v1/jd-imports' && response.request().method() === 'POST')
    await page.getByRole('button', { name: '保存并识别岗位' }).click()
    const body = await (await saved).json()
    return body.data ?? body
  }
  const before = await api('GET', '/api/v1/jds')
  const multi = await paste(fixtures.three_jobs)
  const selection = page.getByRole('region', { name: '岗位识别结果' }).filter({ has: page.getByRole('heading', { name: '识别到 3 个岗位，请选择' }) })
  if (multi.status !== 'confirmed') {
    await selection.waitFor()
    assert.equal(await selection.getByRole('checkbox', { checked: true }).count(), 0)
    assert.equal((await api('GET', '/api/v1/jds')).length, before.length)
    const available = selection.getByRole('checkbox', { disabled: false })
    const count = Math.min(2, await available.count())
    for (let i=0; i<count; i++) await available.nth(i).check()
    await selection.getByRole('button', { name: `确认导入 ${count} 份` }).click()
    await page.getByText('所选岗位已导入，正在分别提取内容并处理分类与能力。').waitFor()
    console.log(`LIVE_UI_MULTI_OK candidates=3 selected=${count} initially_selected=0`)
  } else {
    assert.equal((await api('GET', '/api/v1/jds')).length, before.length)
    console.log('LIVE_UI_MULTI_REPLAY_OK duplicate_formal_count=0')
  }
  await paste(fixtures.page_java)
  const row = page.locator('.jd-row').filter({ hasText: '蓝鲸科技' })
  await row.waitFor()
  await row.click()
  const detail = page.getByRole('dialog')
  await detail.getByText('岗位分类：已完成', { exact: true }).waitFor()
  await detail.locator('.ability-tags').getByText('Java', { exact: true }).waitFor()
  await detail.locator('.jd-ability-level-list').waitFor()
  assert.match(await detail.innerText(), /负责电商服务接口开发和数据库调优/)
  await page.getByRole('button', { name: '关闭抽屉' }).click()
  await page.getByRole('tab', { name: /^岗位能力 / }).click()
  await page.locator('.ability-row').getByText('MySQL', { exact: true }).waitFor()
  const profile = await api('GET', '/api/v1/market-profile')
  assert.ok(profile.included_jd_count >= 1 && profile.abilities.length >= 4)
  assert.match(await page.locator('.market-progress').innerText(), new RegExp(`${profile.included_jd_count} / 10`))
  assert.deepEqual(errors, [])
  console.log(`LIVE_UI_AUTO_REFRESH_OK included=${profile.included_jd_count} abilities=${profile.abilities.length} detail=graded no_manual_refresh=true`)
  const items = await api('GET', '/api/v1/jds')
  const java = items.find(item => item.company === '蓝鲸科技' && item.title.includes('Java'))
  assert.equal(java.job_status, 'succeeded')
  assert.equal(java.ability_grading.status, 'succeeded')
  assert.equal(java.analysis.has_pending, false)
} catch (error) {
  if (page) console.error('LIVE_UI_FAILURE_CONTEXT', JSON.stringify({ url: page.url(), headings: await page.getByRole('heading').allTextContents(), alerts: await page.getByRole('alert').allTextContents() }))
  throw error
} finally { await browser.close() }
