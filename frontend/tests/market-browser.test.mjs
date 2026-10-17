import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { access, mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises'
import { resolve } from 'node:path'
import { build, preview } from 'vite'
import react from '@vitejs/plugin-react'
import { chromium } from 'playwright'

// Opt-in market and practice page tests: no credentials or model calls.
const enabled = process.env.RUN_MARKET_E2E === '1'
let directory, server, browser, baseURL
before(async () => {
  if (!enabled) return
  await mkdir(resolve('node_modules/.tmp'), { recursive: true })
  directory = await mkdtemp(resolve('node_modules/.tmp/market-browser-'))
  await writeFile(`${directory}/index.html`, '<div id="root"></div><script type="module" src="/entry.tsx"></script>')
  await writeFile(`${directory}/entry.tsx`, `import { createRoot } from 'react-dom/client'; import { MarketPage } from ${JSON.stringify(resolve('src/pages/MarketPage.tsx'))}; import { UserProfilePage } from ${JSON.stringify(resolve('src/pages/UserProfilePage.tsx'))}; import { KnowledgeGapsPage } from ${JSON.stringify(resolve('src/pages/KnowledgeGapsPage.tsx'))}; const page = new URLSearchParams(location.search).get('page'); createRoot(document.getElementById('root')!).render(page === 'profile' ? <UserProfilePage target={null} /> : page === 'gaps' ? <KnowledgeGapsPage onOpenMarket={()=>{}} onOpenProfile={()=>{}} onOpenSettings={()=>{}} /> : <MarketPage />);`)
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

// Synthetic practice API shared by the two real page layouts. No model calls.
async function practiceFixture(context, { assessed = true, failEvaluation = false, failSave = false, clarify = false, holdSave = false } = {}) {
  const page = await context.newPage()
  let session, heldSave, releaseSave
  const firstSave = new Promise(resolve => { releaseSave = resolve })
  const calls = { start: 0, save: 0, submit: 0, confirm: 0 }
  const capability = { concept_id: 'go', name: 'Go', category: '编程语言', current_level: 2, market_level: 3, market_level_ready: true, assessed, evidence: [], levels: Array.from({ length: 6 }, (_, level) => ({ level, description: `等级 ${level}` })), level_source: 'manual', status: 'evidence_backed' }
  const question = i => ({ id: `q${i}`, prompt: `合成问题 ${i}`, dimension: '原理', position: i, hint: '合成提示', reference: '合成答案', explanation: '合成讲解' })
  await page.route('**/api/v1/**', async route => {
    const request = route.request(), path = new URL(request.url()).pathname
    if (path === '/api/v1/profile') return respond(route, { profile: { capabilities: [capability], assessed_count: assessed ? 1 : 0, pending_count: assessed ? 0 : 1, market_profile_ready: true, material_count: 0 } })
    if (path === '/api/v1/profile/materials') return respond(route, { materials: [] })
    if (path === '/api/v1/profile/settings') return respond(route, { settings: { weekly_hours: 12, expected_weeks: 8, existing_experience: '合成经历' } })
    if (path === '/api/v1/knowledge-gaps') return respond(route, { readiness: { code: 'ready', included_jd_count: 10 }, stale: false })
    if (path === '/api/v1/model-configs/deepseek') return respond(route, { configured: true })
    if (path === '/api/v1/profile/sessions') {
      if (request.method() === 'GET') return respond(route, { sessions: session ? [session] : [] })
      calls.start++
      const { mode } = request.postDataJSON()
      session = { id: `session-${calls.start}`, concept_id: 'go', concept_name: 'Go', mode, base_level: 2, target_level: 3, status: 'ready', questions: Array.from({ length: mode === 'initial' ? 3 : 6 }, (_, i) => question(i + 1)), answers: [], clarification_count: 0, level_updated: false, created_at: '2026-10-17T00:00:00Z' }
      return respond(route, { session })
    }
    if (path.endsWith('/answers')) {
      calls.save++
      if (failSave && calls.save === 1) return route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ error: { code: 'save_unavailable', message: '合成保存故障' } }) })
      const answer = request.postDataJSON()
      session.answers = [...session.answers.filter(item => item.question_id !== answer.question_id), answer]
      if (holdSave && calls.save === 1) { heldSave = route; releaseSave(); return }
      return respond(route, { session })
    }
    if (path.endsWith('/submit')) {
      calls.submit++
      if (failEvaluation && calls.submit === 1) return route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ error: { code: 'model_unavailable', message: '合成模型故障' } }) })
      session.answers = request.postDataJSON().answers
      if (clarify && calls.submit === 1) {
        session = { ...session, status: 'clarifying', clarification_count: 1, questions: [...session.questions, question(7)] }
      } else {
        session = { ...session, status: 'evaluated', evaluation: { verdict: session.mode === 'review' ? 'review' : 'pass', suggested_level: session.mode === 'initial' ? 0 : undefined, summary: '合成反馈', question_results: [] } }
      }
      return respond(route, { session })
    }
    if (path.endsWith('/confirm')) {
      calls.confirm++
      session.level_updated = true
      capability.current_level = session.mode === 'initial' ? 0 : session.target_level
      return respond(route, { session })
    }
    throw new Error(`Unexpected practice API ${path}`)
  })
  return { page, calls, firstSave, releaseSave: () => respond(heldSave, { session }), session: () => session }
}

test('profile autosaves finish before evaluation; clarification and confirmation keep their boundaries', { skip: !enabled, timeout: 30000 }, async () => {
  const context = await browser.newContext()
  try {
    const f = await practiceFixture(context, { clarify: true, holdSave: true })
    await f.page.goto(`${baseURL}?page=profile`)
    await f.page.locator('.profile-ability-row').click()
    await f.page.getByRole('button', { name: '验证下一级能力' }).click()
    const inputs = f.page.locator('.profile-question-list textarea')
    await inputs.last().waitFor()
    assert.equal(await inputs.count(), 6)
    for (let i = 0; i < 6; i++) await inputs.nth(i).fill(`合成回答 ${i}`)
    await f.firstSave
    await f.page.getByRole('button', { name: '提交全部回答' }).click()
    assert.equal(f.calls.submit, 0)
    await f.releaseSave()
    await f.page.getByText('合成问题 7', { exact: false }).waitFor()
    assert.equal(await inputs.count(), 7)
    assert.equal(await inputs.first().inputValue(), '合成回答 0')
    await inputs.last().fill('合成澄清回答')
    await f.page.getByRole('button', { name: '提交全部回答' }).click()
    await f.page.getByRole('button', { name: '确认更新等级' }).waitFor()
    assert.equal(f.calls.confirm, 0)
    await f.page.getByRole('button', { name: '确认更新等级' }).evaluate(button => { button.click(); button.click() })
    await f.page.getByRole('heading', { name: '能力等级已更新' }).waitFor()
    assert.equal(f.calls.start, 1)
    assert.equal(f.calls.submit, 2)
    assert.equal(f.calls.confirm, 1)
  } finally { await context.close() }
})

test('initial assessment remains three questions and L0 only changes after confirmation', { skip: !enabled, timeout: 30000 }, async () => {
  const context = await browser.newContext()
  try {
    const f = await practiceFixture(context, { assessed: false })
    await f.page.goto(`${baseURL}?page=profile`)
    await f.page.locator('.profile-ability-row').click()
    await f.page.getByRole('button', { name: '初始能力评估' }).evaluate(button => { button.click(); button.click() })
    const inputs = f.page.locator('.profile-question-list textarea')
    await inputs.last().waitFor()
    assert.equal(await inputs.count(), 3)
    for (let i = 0; i < 3; i++) await inputs.nth(i).fill('合成回答')
    await f.page.getByRole('button', { name: '提交全部回答' }).click()
    await f.page.getByRole('heading', { name: '建议初始等级 L0' }).waitFor()
    assert.equal(f.calls.start, 1)
    assert.equal(f.calls.confirm, 0)
    // A page reload must retain an evaluated session that still needs consent.
    await f.page.reload()
    await f.page.locator('.profile-ability-row').click()
    await f.page.getByRole('button', { name: '继续上次问答' }).click()
    await f.page.getByRole('heading', { name: '建议初始等级 L0' }).waitFor()
    await f.page.getByRole('button', { name: '确认更新等级' }).click()
    await f.page.getByRole('heading', { name: '能力等级已更新' }).waitFor()
    assert.equal(f.calls.confirm, 1)
  } finally { await context.close() }
})

test('interview resumes saved answers and retries evaluation without saving the last answer twice', { skip: !enabled, timeout: 30000 }, async () => {
  const context = await browser.newContext()
  try {
    const f = await practiceFixture(context, { failEvaluation: true })
    await f.page.goto(`${baseURL}?page=gaps`)
    await f.page.getByRole('tab', { name: '面试问答' }).click()
    await f.page.getByRole('button', { name: '新建问答' }).click()
    for (let i = 1; i <= 6; i++) {
      if (i === 3) {
        await f.page.reload()
        await f.page.getByRole('tab', { name: '面试问答' }).click()
        await f.page.getByRole('button', { name: '继续上次问答' }).click()
      }
      await f.page.getByRole('heading', { name: `合成问题 ${i}`, exact: true }).waitFor()
      await f.page.getByLabel('你的回答').fill(`合成回答 ${i}`)
      await f.page.getByRole('button', { name: '提交并继续' }).click()
    }
    await f.page.getByRole('heading', { name: '回答已保存，等待模型评估' }).waitFor()
    assert.equal(f.calls.save, 6)
    await f.page.getByRole('button', { name: '重试评估' }).click()
    await f.page.getByRole('heading', { name: '模型建议升级' }).waitFor()
    assert.equal(f.calls.save, 6)
    assert.equal(f.calls.submit, 2)
    assert.equal(f.calls.confirm, 0)
    await f.page.reload()
    await f.page.getByRole('tab', { name: '面试问答' }).click()
    await f.page.getByRole('button', { name: '继续上次问答' }).click()
    await f.page.getByRole('button', { name: '确认提升至 L3' }).evaluate(button => { button.click(); button.click() })
    await f.page.getByText('已确认，用户画像更新为 L3。', { exact: true }).waitFor()
    assert.equal(f.calls.confirm, 1)
  } finally { await context.close() }
})

test('review exposes aids and never offers a grade update', { skip: !enabled, timeout: 30000 }, async () => {
  const context = await browser.newContext()
  try {
    const f = await practiceFixture(context)
    await f.page.goto(`${baseURL}?page=gaps`)
    await f.page.getByRole('tab', { name: '面试问答' }).click()
    await f.page.getByRole('button', { name: '复习练习' }).click()
    await f.page.getByRole('button', { name: '新建问答' }).click()
    await f.page.getByRole('button', { name: '查看提示' }).click()
    await f.page.getByText('合成提示', { exact: true }).waitFor()
    for (let i = 1; i <= 6; i++) {
      await f.page.getByRole('heading', { name: `合成问题 ${i}`, exact: true }).waitFor()
      await f.page.getByLabel('你的回答').fill('合成复习回答')
      await f.page.getByRole('button', { name: '提交并继续' }).click()
    }
    await f.page.getByRole('heading', { name: '复习反馈' }).waitFor()
    assert.equal(await f.page.getByRole('button', { name: /确认.*(提升|等级)/ }).count(), 0)
    assert.equal(f.calls.confirm, 0)
  } finally { await context.close() }
})

test('autosave failure is visible and a later full submission preserves local answers', { skip: !enabled, timeout: 30000 }, async () => {
  const context = await browser.newContext()
  try {
    const f = await practiceFixture(context, { failSave: true })
    await f.page.goto(`${baseURL}?page=profile`)
    await f.page.locator('.profile-ability-row').click()
    await f.page.getByRole('button', { name: '验证下一级能力' }).click()
    const inputs = f.page.locator('.profile-question-list textarea')
    await inputs.last().waitFor()
    await inputs.first().fill('保存失败也保留的回答')
    await inputs.nth(1).focus()
    await f.page.getByRole('alert').filter({ hasText: '回答暂未保存' }).waitFor()
    assert.equal(await inputs.first().inputValue(), '保存失败也保留的回答')
    for (let i = 1; i < 6; i++) await inputs.nth(i).fill(`其余回答 ${i}`)
    await f.page.getByRole('button', { name: '提交全部回答' }).click()
    await f.page.getByRole('button', { name: '确认更新等级' }).waitFor()
    assert.equal(f.session().answers[0].answer, '保存失败也保留的回答')
    assert.equal(f.calls.submit, 1)
  } finally { await context.close() }
})

test('a late save cannot replace a newly opened practice session', { skip: !enabled, timeout: 30000 }, async () => {
  const context = await browser.newContext()
  try {
    const f = await practiceFixture(context, { holdSave: true })
    await f.page.goto(`${baseURL}?page=gaps`)
    await f.page.getByRole('tab', { name: '面试问答' }).click()
    await f.page.getByRole('button', { name: '新建问答' }).click()
    await f.page.getByLabel('你的回答').fill('旧会话回答')
    await f.page.getByRole('button', { name: '提交并继续' }).click()
    await f.firstSave
    await f.page.getByRole('button', { name: '返回选择' }).click()
    await f.page.getByRole('button', { name: '新建问答' }).click()
    await f.releaseSave()
    await f.page.getByRole('heading', { name: '合成问题 1', exact: true }).waitFor()
    assert.equal(await f.page.getByLabel('你的回答').inputValue(), '')
    assert.equal(f.session().id, 'session-2')
    assert.equal(f.calls.submit, 0)
  } finally { await context.close() }
})

test('search and status filters are local and keep the loaded records', { skip: !enabled, timeout: 30000 }, async () => {
  const context = await browser.newContext()
  try {
    const page = await context.newPage()
    const reads = { jds: 0, profile: 0, imports: 0 }
    const reference = { ...completed, id: 'reference', company: '乙公司', status: 'reference', ability_mentions: [{ name: 'Python', evidence: 'Python' }] }
    await page.route('**/api/v1/**', route => {
      const path = new URL(route.request().url()).pathname
      if (path === '/api/v1/jds') { reads.jds++; return respond(route, [completed, reference]) }
      if (path === '/api/v1/market-profile') { reads.profile++; return respond(route, profile(true)) }
      if (path === '/api/v1/jd-imports') { reads.imports++; return respond(route, []) }
      throw new Error(`Unexpected API ${path}`)
    })
    await page.goto(baseURL)
    await page.locator('.jd-row').last().waitFor()
    await page.getByLabel('搜索 JD').focus()
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
    const baseline = { ...reads }
    await page.getByLabel('搜索 JD').fill('甲')
    await page.getByLabel('搜索 JD').pressSequentially('公司')
    await page.locator('.jd-row').filter({ hasText: '甲公司' }).waitFor()
    assert.equal(await page.locator('.jd-row').count(), 1)
    await page.getByLabel('搜索 JD').fill('')
    await page.getByLabel('按状态筛选').selectOption('reference')
    await page.locator('.jd-row').filter({ hasText: '乙公司' }).waitFor()
    assert.equal(await page.locator('.jd-row').count(), 1)
    await page.getByLabel('按状态筛选').selectOption('all')
    await page.getByLabel('搜索 JD').fill('MySQL')
    await page.locator('.jd-row').filter({ hasText: '甲公司' }).waitFor()
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
    assert.deepEqual(reads, baseline)
  } finally { await context.close() }
})

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

test('empty imports show one dismissible modal, without a permanent card or repeat on refresh', { skip: !enabled, timeout: 30000 }, async () => {
  const context = await browser.newContext()
  try {
    const page = await context.newPage()
    let reads = 0
    await page.route('**/api/v1/**', route => {
      const path = new URL(route.request().url()).pathname
      if (path === '/api/v1/jds') return respond(route, [])
      if (path === '/api/v1/jd-imports') {
        reads++
        return respond(route, [{ ...session, status: 'empty', candidates: [] }, { ...session, id: 'another-empty', status: 'empty', candidates: [] }])
      }
      if (path === '/api/v1/market-profile') return respond(route, profile(false))
      throw new Error(`Unexpected API ${path}`)
    })
    await page.goto(baseURL)
    const modal = page.getByRole('alertdialog', { name: '未识别到有效 JD' })
    await modal.waitFor()
    assert.match(await modal.innerText(), /有 2 份材料/)
    assert.match(await modal.innerText(), /不会计入市场画像/)
    assert.equal(await page.locator('.jd-import-panel').count(), 0)
    assert.equal(await page.getByRole('checkbox').count(), 0)
    assert.equal(await page.getByRole('button', { name: '重新识别' }).count(), 0)
    assert.equal(await page.locator('.jd-row').count(), 0)
    await page.keyboard.press('Escape')
    await modal.waitFor({ state: 'detached' })
    await page.getByText('还没有 JD。可粘贴招聘网页文字开始建立市场画像。', { exact: true }).waitFor()
    await page.evaluate(() => window.dispatchEvent(new Event('focus')))
    await page.waitForFunction(() => document.querySelector('.market-empty')?.textContent?.includes('还没有 JD'))
    // A reload keeps acknowledgements in this tab, without retaining any material text.
    await page.reload()
    await page.locator('.market-empty').waitFor()
    assert.equal(await modal.count(), 0)
    const settledReads = reads
    await page.waitForTimeout(2200)
    assert.equal(reads, settledReads, 'terminal empty results must not keep polling')
    assert.equal(await page.locator('.jd-import-panel').count(), 0)
  } finally { await context.close() }
})

test('async empty results clear processing notices and explicit resubmissions open a new reminder', { skip: !enabled, timeout: 30000 }, async () => {
  const context = await browser.newContext()
  try {
    const page = await context.newPage()
    let imports = [], finish = false, submissions = 0
    await page.route('**/api/v1/**', route => {
      const request = route.request(), path = new URL(request.url()).pathname
      if (path === '/api/v1/jds') return respond(route, [])
      if (path === '/api/v1/market-profile') return respond(route, profile(false))
      if (path === '/api/v1/jd-imports' && request.method() === 'POST') {
        submissions++
        // Identical material can reuse a terminal empty session on the server.
        const result = { ...session, status: submissions === 1 ? 'running' : 'empty', candidates: [], has_pending: submissions === 1, next_poll_ms: 2000 }
        imports = [result]
        return respond(route, result, 202)
      }
      if (path === '/api/v1/jd-imports') return respond(route, finish ? imports.map(item => ({ ...item, status: 'empty', has_pending: false })) : imports)
      throw new Error(`Unexpected API ${path}`)
    })
    await page.goto(baseURL)
    await page.getByRole('button', { name: '添加 JD', exact: true }).click()
    await page.getByLabel('招聘材料', { exact: true }).fill(raw)
    await page.getByRole('button', { name: '保存并识别岗位' }).click()
    const progress = page.getByRole('dialog', { name: '正在识别岗位内容' })
    await progress.waitFor()
    assert.match(await progress.innerText(), /0 份排队中 · 1 份正在识别/)
    assert.equal(await page.locator('.jd-import-panel').count(), 0)
    assert.equal(await page.locator('.market-notice').count(), 0)
    finish = true
    const modal = page.getByRole('alertdialog', { name: '未识别到有效 JD' })
    await modal.waitFor()
    assert.equal(await page.locator('.market-notice').count(), 0)
    assert.equal(await page.locator('.jd-import-panel').count(), 0)
    await modal.getByRole('button', { name: '重新粘贴' }).click()
    await modal.waitFor({ state: 'detached' })
    await page.getByLabel('招聘材料', { exact: true }).fill(raw)
    await page.getByRole('button', { name: '保存并识别岗位' }).click()
    await modal.waitFor()
    assert.equal(submissions, 2)
    assert.equal(await page.locator('.market-notice').count(), 0)
    await modal.getByRole('button', { name: '关闭提醒' }).click()
    await modal.waitFor({ state: 'detached' })
    await page.reload()
    await page.locator('.market-empty').waitFor()
    assert.equal(await modal.count(), 0)
    assert.equal(await page.locator('.jd-import-panel').count(), 0)
  } finally { await context.close() }
})

test('progress is dismissible and reopenable; queue transitions and refresh do not reopen it, completion closes it', { skip: !enabled, timeout: 30000 }, async () => {
  const context = await browser.newContext()
  try {
    const page = await context.newPage()
    let imports = [
      { ...session, status: 'queued', candidates: [], has_pending: true, next_poll_ms: 2000 },
      { ...session, id: 'second-material', status: 'running', candidates: [], has_pending: true, next_poll_ms: 2000 },
    ]
    await page.route('**/api/v1/**', route => {
      const path = new URL(route.request().url()).pathname
      if (path === '/api/v1/jds') return respond(route, [])
      if (path === '/api/v1/jd-imports') return respond(route, imports)
      if (path === '/api/v1/market-profile') return respond(route, profile(false))
      throw new Error(`Unexpected API ${path}`)
    })
    await page.goto(baseURL)
    const modal = page.getByRole('dialog', { name: '正在识别岗位内容' })
    await modal.waitFor()
    assert.match(await modal.innerText(), /1 份排队中 · 1 份正在识别/)
    assert.equal(await page.locator('.jd-import-panel').count(), 0)
    await modal.getByRole('button', { name: '后台继续' }).click()
    await modal.waitFor({ state: 'detached' })
    imports = imports.map(item => ({ ...item, status: 'running' }))
    // Let the real poll observe queued -> running after the reminder is dismissed.
    const response = await page.waitForResponse(response => new URL(response.url()).pathname === '/api/v1/jd-imports')
    await response.finished()
    await page.reload()
    const reopen = page.getByRole('button', { name: '查看识别进度（2）', exact: true })
    await reopen.waitFor()
    assert.equal(await modal.count(), 0)
    await reopen.click()
    await modal.waitFor()
    assert.match(await modal.innerText(), /0 份排队中 · 2 份正在识别/)
    imports = [session]
    await page.getByRole('heading', { name: '识别到 2 个岗位，请选择' }).waitFor()
    await modal.waitFor({ state: 'detached' })
    assert.equal(await page.getByRole('button', { name: /查看识别进度/ }).count(), 0)
    assert.equal(await page.getByRole('checkbox', { checked: true }).count(), 0)
    await page.getByRole('checkbox').first().check()
    assert.equal(await page.getByRole('button', { name: '确认导入 1 份' }).isEnabled(), true)
  } finally { await context.close() }
})

test('recognition failure closes progress and a retry opens progress for the same import', { skip: !enabled, timeout: 30000 }, async () => {
  const context = await browser.newContext()
  try {
    const page = await context.newPage()
    let imports = [{ ...session, status: 'running', candidates: [], has_pending: true, next_poll_ms: 2000 }], retried = 0
    await page.route('**/api/v1/**', route => {
      const path = new URL(route.request().url()).pathname
      if (path === '/api/v1/jds') return respond(route, [])
      if (path === '/api/v1/jd-imports/material/retry') {
        retried++
        imports = [{ ...imports[0], status: 'queued', has_pending: true }]
        return respond(route, imports[0])
      }
      if (path === '/api/v1/jd-imports') return respond(route, imports)
      if (path === '/api/v1/market-profile') return respond(route, profile(false))
      throw new Error(`Unexpected API ${path}`)
    })
    await page.goto(baseURL)
    const modal = page.getByRole('dialog', { name: '正在识别岗位内容' })
    await modal.waitFor()
    imports = [{ ...imports[0], status: 'failed', has_pending: false, error_code: 'model_rate_limited' }]
    await page.getByRole('heading', { name: '岗位识别暂时失败' }).waitFor()
    await modal.waitFor({ state: 'detached' })
    await page.getByRole('button', { name: '重新识别', exact: true }).click()
    await modal.waitFor()
    assert.equal(retried, 1)
    assert.match(await modal.innerText(), /1 份排队中 · 0 份正在识别/)
    assert.equal(await page.locator('.jd-import-panel').count(), 0)
    await page.keyboard.press('Escape')
    await modal.waitFor({ state: 'detached' })
    await page.getByRole('button', { name: '查看识别进度（1）', exact: true }).waitFor()
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
