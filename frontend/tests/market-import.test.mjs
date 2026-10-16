import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { mkdir, mkdtemp, rm } from 'node:fs/promises'
import { resolve } from 'node:path'
import { pathToFileURL } from 'node:url'
import { build } from 'vite'

let directory, Panel, delay
before(async () => {
  const cache = resolve('node_modules/.tmp')
  await mkdir(cache, { recursive: true })
  directory = await mkdtemp(`${cache}/market-test-`)
  await build({ configFile: false, logLevel: 'error', build: { ssr: true, outDir: directory, rolldownOptions: { input: { panel: resolve('src/pages/JDImportPanel.tsx'), data: resolve('src/pages/useMarketData.ts') }, output: { entryFileNames: '[name].mjs' } } } })
  Panel = (await import(pathToFileURL(`${directory}/panel.mjs`).href)).JDImportPanel
  delay = (await import(pathToFileURL(`${directory}/data.mjs`).href)).marketPollDelay
})
after(async () => { if (directory) await rm(directory, { recursive: true, force: true }) })
const session = { id: 'import', status: 'ready', candidates: [{ id: 'a', title: '后端', company: '甲', raw_text: 'Go' }, { id: 'b', title: '前端', company: '乙', raw_text: 'React' }] }
const props = { onConfirm: async () => {}, onRetry: async () => {} }
test('multiple jobs require an explicit selection with no default choices', () => {
  const html = renderToStaticMarkup(createElement(Panel, { session, ...props }))
  assert.equal((html.match(/type="checkbox"/g) ?? []).length, 2)
  assert.doesNotMatch(html, /checked=""/)
  assert.match(html, /disabled=""[^>]*>确认导入 0 份/)
})
test('non JD is a normal empty result without a failure retry action', () => {
  const html = renderToStaticMarkup(createElement(Panel, { session: { ...session, status: 'empty', candidates: [] }, ...props }))
  assert.match(html, /未识别到有效 JD/)
  assert.doesNotMatch(html, /重新识别|暂时失败|type="checkbox"/)
})
test('replacement selection uses one radio choice and preserves original identity', () => {
  const html = renderToStaticMarkup(createElement(Panel, { session: { ...session, replacement_jd_id: 'old' }, ...props }))
  assert.equal((html.match(/type="radio"/g) ?? []).length, 2)
  assert.match(html, /原记录编号会保留/)
})
test('pending import, analysis, review and grading keep the canonical state polling', () => {
  const profile = { ability_grading_pending_count: 0 }
  assert.equal(delay([], [], profile), null)
  assert.equal(delay([], [{ has_pending: true, next_poll_ms: 2000 }], profile), 2000)
  assert.equal(delay([{ analysis: { has_pending: true, next_poll_ms: 30000 } }], [], profile), 30000)
  assert.equal(delay([{ analysis: { has_pending: false } }], [], profile), null)
  assert.equal(delay([{ analysis: { has_pending: false } }], [], { ability_grading_pending_count: 1 }), null)
  assert.equal(delay([{ analysis: { has_pending: true, next_poll_ms: 30000 } }], [], { ability_grading_pending_count: 1 }), 30000)
  assert.equal(delay([{ analysis: { has_pending: true, next_poll_ms: 30000 } }, { analysis: { has_pending: true, next_poll_ms: 2000 } }], [], profile), 2000)
  assert.equal(delay([{ ability_review: { pending_count: 1, blocked: false } }], [], profile), 2000)
  assert.equal(delay([{ ability_review: { pending_count: 1, blocked: true } }], [], profile), null)
})
