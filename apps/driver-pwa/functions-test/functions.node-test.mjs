// Pages Functions checks for the driver PWA (P24, GF-352). The PWA has no
// WebSocket client, so /stream must not be proxied here.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { dirname, join } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const proxy = await import(pathToFileURL(join(here, '../functions/[[path]].js')).href)

const UPSTREAM = 'https://api.example.test'

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

test('allowlisted driver path is proxied with method, headers and body preserved', async (t) => {
  const calls = fakeFetch(t)
  const { context } = ctx('https://driver.test/driver/jobs/today?day=2026-10-28', {
    method: 'POST',
    headers: { 'X-Test': 'kept' },
    body: 'payload',
  })
  await proxy.onRequest(context)
  assert.equal(calls.length, 1)
  assert.equal(calls[0].url, `${UPSTREAM}/driver/jobs/today?day=2026-10-28`)
  assert.equal(calls[0].method, 'POST')
  assert.equal(calls[0].headers.get('x-test'), 'kept')
  assert.equal(await calls[0].text(), 'payload')
})

test('/ingest and /auth are proxied, /stream is not', async (t) => {
  const calls = fakeFetch(t)
  for (const path of ['/ingest/position', '/auth/refresh']) {
    const { context } = ctx(`https://driver.test${path}`)
    await proxy.onRequest(context)
  }
  assert.equal(calls.length, 2)

  const { context, hit } = ctx('https://driver.test/stream?fleet=1', { headers: { Upgrade: 'websocket' } })
  await proxy.onRequest(context)
  assert.equal(hit.next, 1, '/stream falls through: the driver PWA has no WebSocket client')
  assert.equal(calls.length, 2)
})

test('a non-allowlisted path falls through to next()', async (t) => {
  const calls = fakeFetch(t)
  const { context, hit } = ctx('https://driver.test/jobs')
  const res = await proxy.onRequest(context)
  assert.equal(hit.next, 1, '/jobs is a dispatcher path, not a driver path')
  assert.equal(calls.length, 0)
  assert.equal(await res.text(), 'static')
})

test('a driver navigation gets index.html, not the API', async (t) => {
  const calls = fakeFetch(t)
  const { context, hit } = ctx('https://driver.test/driver/jobs', {
    headers: { 'Sec-Fetch-Mode': 'navigate' },
  })
  const res = await proxy.onRequest(context)
  assert.deepEqual(hit.assets, ['https://driver.test/index.html'])
  assert.equal(calls.length, 0)
  assert.equal(await res.text(), 'index')
})

test('missing UPSTREAM_ORIGIN is a 500 that names only the variable', async (t) => {
  const calls = fakeFetch(t)
  const { context } = ctx('https://driver.test/auth/login', { method: 'POST' }, { UPSTREAM_ORIGIN: undefined })
  const res = await proxy.onRequest(context)
  assert.equal(res.status, 500)
  const body = await res.text()
  assert.match(body, /UPSTREAM_ORIGIN/)
  assert.doesNotMatch(body, /api\.example/)
  assert.equal(calls.length, 0)
})
