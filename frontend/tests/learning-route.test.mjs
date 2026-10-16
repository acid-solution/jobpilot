import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { mkdir, mkdtemp, rm } from 'node:fs/promises'
import { resolve } from 'node:path'
import { pathToFileURL } from 'node:url'
import { build } from 'vite'

let directory, routeInputsReady, Preparation, Panel
before(async () => {
  const cache = resolve('node_modules/.tmp')
  await mkdir(cache, { recursive: true })
  directory = await mkdtemp(`${cache}/route-test-`)
  await build({ configFile: false, logLevel: 'error', build: { ssr: true, outDir: directory, rolldownOptions: { input: { preparation: resolve('src/components/LearningRoutePreparation.tsx'), panel: resolve('src/pages/LearningRoutePanel.tsx') }, output: { entryFileNames: '[name].mjs' } } } })
  const preparation = await import(pathToFileURL(`${directory}/preparation.mjs`).href)
  routeInputsReady = preparation.routeInputsReady
  Preparation = preparation.LearningRoutePreparation
  Panel = (await import(pathToFileURL(`${directory}/panel.mjs`).href)).LearningRoutePanel
})
after(async () => { if (directory) await rm(directory, { recursive: true, force: true }) })

const navigation = { onOpenMarket() {}, onOpenProfile() {}, onOpenProjects() {}, onOpenTarget() {} }
function readyView() {
  return { goal_title: '后端实习', project_title: '任务协作系统', readiness: { code: 'ready', message: '' }, preparation: { market_complete: true, profile_complete: true, analysis_ready: true, project_selected: true, project_needs_review: false, included_jd_count: 10, required_jd_count: 10 } }
}

test('only complete profiles, analysis and a current selection unlock planning', () => {
  assert.equal(routeInputsReady(null), false)
  assert.equal(routeInputsReady({ readiness: { code: 'ready' } }), false)
  assert.equal(routeInputsReady(readyView()), true)
  for (const key of ['market_complete', 'profile_complete', 'analysis_ready', 'project_selected']) {
    const view = readyView()
    view.preparation[key] = false
    assert.equal(routeInputsReady(view), false, key)
  }
  const stale = readyView()
  stale.preparation.project_needs_review = true
  assert.equal(routeInputsReady(stale), false)
  const pending = readyView()
  pending.readiness.code = 'grading_pending'
  assert.equal(routeInputsReady(pending), false)
})

test('incomplete profiles show progress and do not offer project selection early', () => {
  const view = readyView()
  Object.assign(view.preparation, { market_complete: false, profile_complete: false, analysis_ready: false, project_selected: false, included_jd_count: 3 })
  view.readiness = { code: 'market_incomplete', message: '请先收集至少 10 份计入市场画像的 JD' }
  const html = renderToStaticMarkup(createElement(Preparation, { view, ...navigation }))
  assert.match(html, /已计入 3 \/ 10 份目标 JD/)
  assert.match(html, /先完成准备，再生成学习路线/)
  assert.match(html, /disabled=""[^>]*>前往项目推荐/)
  assert.doesNotMatch(html, /生成学习路线<\/button>|<input|<textarea|<select/)
})

test('complete profiles still require a recommended and selected project', () => {
  const view = readyView()
  view.preparation.project_selected = false
  view.readiness = { code: 'project_missing', message: '请先生成项目推荐并选择一个项目' }
  const html = renderToStaticMarkup(createElement(Preparation, { view, ...navigation }))
  assert.match(html, /请先生成项目推荐并选择一个项目/)
  assert.match(html, /<button class="button" type="button">前往项目推荐/)
  assert.equal(routeInputsReady(view), false)
})

test('missing target offers a direct target action', () => {
  const view = readyView()
  view.readiness.code = 'target_missing'
  const html = renderToStaticMarkup(createElement(Preparation, { view, ...navigation }))
  assert.match(html, /设置求职目标/)
  assert.equal(routeInputsReady(view), false)
})

test('initial loading never renders generation controls or a budget form', () => {
  const html = renderToStaticMarkup(createElement(Panel, navigation))
  assert.match(html, /正在核对两类画像、审核判级和已选项目/)
  assert.doesNotMatch(html, /生成学习路线<\/button>|<fieldset|<input|<textarea|<select/)
})
