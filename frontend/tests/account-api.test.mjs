import assert from 'node:assert/strict'
import { after, afterEach, before, test } from 'node:test'
import { mkdir, mkdtemp, rm } from 'node:fs/promises'
import { resolve } from 'node:path'
import { pathToFileURL } from 'node:url'
import { build } from 'vite'

let directory, authAPI, jobPilotAPI
const originalFetch = globalThis.fetch
const reply = (body, status = 200) => new Response(body === null ? null : JSON.stringify(body), { status })
const issue = { access_token: 'access-token', account: {}, expires_at: '' }

before(async () => {
  const cache = resolve('node_modules/.tmp')
  await mkdir(cache, { recursive: true })
  directory = await mkdtemp(`${cache}/account-api-`)
  await build({ configFile: false, logLevel: 'error', build: { ssr: true, outDir: directory, rolldownOptions: { input: resolve('src/api.ts'), output: { entryFileNames: 'api.mjs' } } } })
  ;({ authAPI, jobPilotAPI } = await import(pathToFileURL(`${directory}/api.mjs`).href))
})
afterEach(() => { globalThis.fetch = originalFetch; authAPI.setFailureHandler(null) })
after(async () => { if (directory) await rm(directory, { recursive: true, force: true }) })

async function signIn() {
  globalThis.fetch = async () => reply({ data: issue })
  await authAPI.login('person@example.com', 'password')
}

test('deletion sends the exact password and clears the bearer token after success', async () => {
  await signIn()
  const calls = []
  globalThis.fetch = async (url, init) => { calls.push({ url, init }); return url === '/auth/v1/me' ? reply(null, 204) : reply({}) }
  await authAPI.deleteAccount(' password with spaces ')
  assert.equal(calls[0].url, '/auth/v1/me')
  assert.equal(calls[0].init.method, 'DELETE')
  assert.equal(calls[0].init.credentials, 'include')
  assert.equal(calls[0].init.headers.Authorization, 'Bearer access-token')
  assert.deepEqual(JSON.parse(calls[0].init.body), { current_password: ' password with spaces ' })
  await jobPilotAPI.getCurrentTarget()
  assert.equal(calls[1].init.headers.has('Authorization'), false)
})

test('incorrect password does not refresh, retry, or discard the login token', async () => {
  await signIn()
  const calls = []
  let failures = 0
  authAPI.setFailureHandler(() => { failures++ })
  globalThis.fetch = async (url, init) => { calls.push({ url, init }); return url === '/auth/v1/me' ? reply({ error: { code: 'invalid_password', message: '当前密码错误' } }, 401) : reply({}) }
  await assert.rejects(authAPI.deleteAccount('wrong'), { status: 401, code: 'invalid_password' })
  assert.equal(calls.length, 1)
  await jobPilotAPI.getCurrentTarget()
  assert.equal(calls[1].init.headers.get('Authorization'), 'Bearer access-token')
  assert.equal(failures, 0)
})

test('an expired token is refreshed once and the deletion is retried with the new token', async () => {
  await signIn()
  const calls = []
  globalThis.fetch = async (url, init) => {
    calls.push({ url, init })
    if (calls.length === 1) return reply({ error: { code: 'unauthorized' } }, 401)
    if (url === '/auth/v1/token/refresh') return reply({ data: { ...issue, access_token: 'fresh-token' } })
    return reply(null, 204)
  }
  await authAPI.deleteAccount('password')
  assert.deepEqual(calls.map(call => call.url), ['/auth/v1/me', '/auth/v1/token/refresh', '/auth/v1/me'])
  assert.equal(calls[2].init.headers.Authorization, 'Bearer fresh-token')
})

test('a failed token refresh returns to the login screen without retrying deletion', async () => {
  await signIn()
  const calls = []
  let failures = 0
  authAPI.setFailureHandler(() => { failures++ })
  globalThis.fetch = async url => { calls.push(url); return reply({ error: { code: 'unauthorized' } }, 401) }
  await assert.rejects(authAPI.deleteAccount('password'), { status: 401 })
  assert.deepEqual(calls, ['/auth/v1/me', '/auth/v1/token/refresh'])
  assert.equal(failures, 1)
})

test('a refresh started during deletion cannot restore a deleted account token', async () => {
  await signIn()
  let finishDelete, finishRefresh
  globalThis.fetch = url => new Promise(resolve => {
    if (url === '/auth/v1/me') finishDelete = resolve
    else finishRefresh = resolve
  })
  const deleting = authAPI.deleteAccount('password')
  const refreshing = authAPI.refresh()
  const rejected = assert.rejects(refreshing, { code: 'session_changed' })
  finishDelete(reply(null, 204))
  await deleting
  finishRefresh(reply({ data: issue }))
  await rejected
  let authorization
  globalThis.fetch = async (_url, init) => { authorization = init.headers.get('Authorization'); return reply({}) }
  await jobPilotAPI.getCurrentTarget()
  assert.equal(authorization, null)
})
