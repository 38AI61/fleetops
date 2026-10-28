// Pages Functions checks with a fake env and a stubbed fetch (P24, GF-352).
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { dirname, join } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const proxy = await import(pathToFileURL(join(here, '../functions/[[path]].js')).href)
const stream = await import(pathToFileURL(join(here, '../functions/stream.js')).href)

const UPSTREAM = 'https://api.example.test'

// fakeFetch records every outgoing fetch and returns a fixed body.
function fakeFetch(t) {
  const calls = []
  const original = globalThis.fetch
  globalThis.fetch = async (req) => {
    calls.push(req)
    return new Response('upstream')
  }
  t.after(() => {
    globalThis.fetch = original
  })
  return calls
}

function ctx(url, init = {}, env = {}) {
  const hit = { next: 0, assets: [] }
  const context = {
    request: new Request(url, init),
    env: {
      UPSTREAM_ORIGIN: UPSTREAM,
      ASSETS: {
        fetch: async (req) => {
          hit.assets.push(req.url)
          return new Response('index')
        },
      },
      ...env,
    },
    next: async () => {
      hit.next++
      return new Response('static')
    },
  }
  return { context, hit }
}

test('allowlisted path is proxied with method, headers and body preserved', async (t) => {
  const calls = fakeFetch(t)
  const { context } = ctx('https://fleetops.test/jobs?state=created', {
    method: 'POST',
    headers: { 'X-Test': 'kept', 'Content-Type': 'application/json' },
    body: '{"a":1}',
  })
  await proxy.onRequest(context)
  assert.equal(calls.length, 1)
  assert.equal(calls[0].url, `${UPSTREAM}/jobs?state=created`)
  assert.equal(calls[0].method, 'POST')
  assert.equal(calls[0].headers.get('x-test'), 'kept')
  assert.equal(await calls[0].text(), '{"a":1}')
})

test('allowlist matches exact paths and sub-paths only', async (t) => {
  const calls = fakeFetch(t)
  for (const path of ['/auth/login', '/reports/daily', '/alert-rules']) {
    const { context } = ctx(`https://fleetops.test${path}`)
    await proxy.onRequest(context)
  }
  assert.equal(calls.length, 3)

  const { context, hit } = ctx('https://fleetops.test/alertsx')
  await proxy.onRequest(context)
  assert.equal(hit.next, 1, '/alertsx is not under /alerts')
  assert.equal(calls.length, 3)
})

test('a non-allowlisted path falls through to next()', async (t) => {
  const calls = fakeFetch(t)
  const { context, hit } = ctx('https://fleetops.test/assets/app.js')
  const res = await proxy.onRequest(context)
  assert.equal(hit.next, 1)
  assert.equal(calls.length, 0)
  assert.equal(await res.text(), 'static')
})

test('a navigation to a UI route gets index.html, not the API', async (t) => {
  const calls = fakeFetch(t)
  const { context, hit } = ctx('https://fleetops.test/jobs/new', {
    headers: { 'Sec-Fetch-Mode': 'navigate' },
  })
  const res = await proxy.onRequest(context)
  assert.deepEqual(hit.assets, ['https://fleetops.test/index.html'])
  assert.equal(calls.length, 0, 'navigation must not be proxied')
  assert.equal(await res.text(), 'index')
})

test('missing UPSTREAM_ORIGIN is a 500 that names only the variable', async (t) => {
  const calls = fakeFetch(t)
  const { context } = ctx('https://fleetops.test/vehicles', {}, { UPSTREAM_ORIGIN: undefined })
  const res = await proxy.onRequest(context)
  assert.equal(res.status, 500)
  const body = await res.text()
  assert.match(body, /UPSTREAM_ORIGIN/)
  assert.equal(calls.length, 0)
})

test('stream: a non-upgrade request is 426', async () => {
  const { context } = ctx('https://fleetops.test/stream?fleet=5')
  const res = await stream.onRequest(context)
  assert.equal(res.status, 426)
})

test('stream: an upgrade reaches the upstream /stream with fleet preserved', async (t) => {
  const calls = fakeFetch(t)
  const { context } = ctx('https://fleetops.test/stream?fleet=5', {
    headers: { Upgrade: 'websocket', Origin: 'https://fleetops.test' },
  })
  await stream.onRequest(context)
  assert.equal(calls.length, 1)
  assert.equal(calls[0].url, `${UPSTREAM}/stream?fleet=5`)
  assert.equal(calls[0].headers.get('origin'), 'https://fleetops.test')
})

test('stream: a missing UPSTREAM_ORIGIN is a 500 without leaking a value', async (t) => {
  const calls = fakeFetch(t)
  const { context } = ctx('https://fleetops.test/stream?fleet=5', {
    headers: { Upgrade: 'websocket' },
  }, { UPSTREAM_ORIGIN: '' })
  const res = await stream.onRequest(context)
  assert.equal(res.status, 500)
  assert.equal(calls.length, 0)
  assert.doesNotMatch(await res.text(), /api\.example/)
})
