import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { access, mkdir, mkdtemp, rm } from 'node:fs/promises'
import { resolve } from 'node:path'
import { build, preview } from 'vite'
import react from '@vitejs/plugin-react'
import { chromium } from 'playwright'

const enabled = process.env.RUN_ACCOUNT_E2E === '1'
let directory, server, browser, baseURL
before(async () => {
  if (!enabled) return
  await mkdir(resolve('node_modules/.tmp'), { recursive: true })
  directory = await mkdtemp(resolve('node_modules/.tmp/account-browser-'))
  await build({ configFile: false, plugins: [react()], logLevel: 'error', build: { outDir: directory } })
  server = await preview({ configFile: false, build: { outDir: directory }, preview: { host: '127.0.0.1', port: 0, open: false } })
  baseURL = `http://127.0.0.1:${server.httpServer.address().port}`
  const candidates = [process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE, 'C:/Program Files/Google/Chrome/Application/chrome.exe', 'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe'].filter(Boolean)
  let executablePath
  for (const candidate of candidates) { try { await access(candidate); executablePath = candidate; break } catch { /* Try the next browser. */ } }
  browser = await chromium.launch({ headless: true, ...(executablePath ? { executablePath } : {}) })
})
after(async () => {
  await browser?.close()
  if (server) await new Promise(resolve => server.httpServer.close(resolve))
  if (directory) await rm(directory, { recursive: true, force: true })
})

test('account settings require consent, retain a wrong-password login and sign out after deletion', { skip: !enabled, timeout: 30000 }, async () => {
  const context = await browser.newContext()
  try {
    const page = await context.newPage()
    let deletions = 0, refreshes = 0, finishDelete
    const session = { access_token: 'access-token', account: { user: { id: 'user-id', status: 'active' }, identities: [{ kind: 'email', value: 'person@example.com' }] }, expires_at: '', token_type: 'Bearer' }
    await page.route('**/auth/v1/**', async route => {
      const path = new URL(route.request().url()).pathname
      if (path === '/auth/v1/token/refresh') {
        refreshes++
        return route.fulfill({ json: { data: session } })
      }
      assert.equal(path, '/auth/v1/me')
      assert.equal(route.request().method(), 'DELETE')
      assert.equal(route.request().headers().authorization, 'Bearer access-token')
      deletions++
      if (deletions === 1) return route.fulfill({ status: 401, json: { error: { code: 'invalid_password', message: '当前密码错误' } } })
      assert.deepEqual(route.request().postDataJSON(), { current_password: 'correct-password' })
      await new Promise(resolve => { finishDelete = resolve })
      return route.fulfill({ status: 204 })
    })
    await page.route('**/api/v1/**', route => route.fulfill({ status: 404, json: { error: { code: 'not_found', message: '合成测试数据为空' } } }))
    await page.goto(baseURL)
    await page.getByRole('button', { name: '设置', exact: true }).click()
    await page.getByRole('tab', { name: '账号安全' }).click()
    await page.getByRole('button', { name: '注销账号', exact: true }).click()
    const submit = page.getByRole('button', { name: '永久注销账号', exact: true })
    assert.equal(await submit.isDisabled(), true)
    await page.getByLabel('当前密码', { exact: true }).fill('wrong-password')
    assert.equal(await submit.isDisabled(), true)
    await page.getByRole('checkbox').check()
    await page.getByRole('button', { name: '取消', exact: true }).click()
    assert.equal(deletions, 0)
    await page.getByRole('button', { name: '注销账号', exact: true }).click()
    assert.equal(await page.getByLabel('当前密码', { exact: true }).inputValue(), '')
    assert.equal(await page.getByRole('checkbox').isChecked(), false)
    await page.getByLabel('当前密码', { exact: true }).fill('wrong-password')
    await page.getByRole('checkbox').check()
    await submit.click()
    await page.getByRole('alert').filter({ hasText: '当前密码错误' }).waitFor()
    assert.equal(refreshes, 1)
    await page.getByLabel('当前密码', { exact: true }).fill('correct-password')
    await submit.evaluate(button => { button.click(); button.click() })
    await page.getByRole('button', { name: '正在注销…' }).waitFor()
    assert.equal(await page.getByRole('button', { name: '取消', exact: true }).isDisabled(), true)
    await page.waitForFunction(() => document.querySelector('form[aria-busy="true"]') !== null)
    await assertEventually(() => finishDelete)
    assert.equal(deletions, 2)
    finishDelete()
    await page.getByRole('heading', { name: '登录 JobPilot' }).waitFor()
    assert.equal(deletions, 2)
  } finally { await context.close() }
})

async function assertEventually(value) {
  for (let attempt = 0; attempt < 100; attempt++) {
    if (value()) return
    await new Promise(resolve => setTimeout(resolve, 10))
  }
  assert.fail('expected deletion request did not arrive')
}
