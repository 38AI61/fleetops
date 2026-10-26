// Scripted live checks for P9 (validation C1, C3, C4, C6, C7, C8, C9; GF-165).
// Node 26, global fetch and WebSocket, no test framework. Not part of `vitest run`.
//
// Env (required): FLEETOPS_EMAIL, FLEETOPS_PASSWORD
// Env (optional): FLEETOPS_WINDOW_S (default 120)
//
// Credentials are read from the environment only and never written to disk.

const EMAIL = process.env.FLEETOPS_EMAIL
const PASSWORD = process.env.FLEETOPS_PASSWORD
const WINDOW_S = Number(process.env.FLEETOPS_WINDOW_S ?? 120)

const API = 'http://127.0.0.1:10010'
const PROXY = 'http://127.0.0.1:10012'
const PROXY_LOCALHOST = 'http://localhost:10012'
const HUB_DIRECT = 'ws://127.0.0.1:10011'
const HUB_PROXY = 'ws://localhost:10012'
const WORLD = '-180,-90,180,90'
const OPS_ROLES = new Set(['admin', 'manager', 'dispatcher'])
const KNOWN_TYPES = new Set(['auth.ok', 'position.batch', 'presence.changed'])

if (!EMAIL || !PASSWORD) {
  console.error('FLEETOPS_EMAIL and FLEETOPS_PASSWORD are required')
  process.exit(2)
}

const results = []
function check(id, ok, detail) {
  results.push({ id, ok })
  console.log(`${ok ? 'PASS' : 'FAIL'} ${id} ${detail}`)
}

function claimsOf(token) {
  const part = token.split('.')[1]
  const b64 = part.replace(/-/g, '+').replace(/_/g, '/')
  return JSON.parse(Buffer.from(b64, 'base64').toString('utf8'))
}

function percentile(sorted, p) {
  if (sorted.length === 0) return NaN
  return sorted[Math.min(sorted.length - 1, Math.ceil(p * sorted.length) - 1)]
}

// Opens a socket, sends the auth frame, and resolves with auth.ok or the close code.
function connect(url, token) {
  return new Promise((resolve) => {
    const ws = new WebSocket(url)
    const timer = setTimeout(() => {
      ws.close()
      resolve({ ok: false, reason: 'no auth.ok within 5s' })
    }, 5000)
    ws.onopen = () => ws.send(JSON.stringify({ type: 'auth', token }))
    ws.onmessage = (ev) => {
      const frame = JSON.parse(ev.data)
      if (frame.type === 'auth.ok') {
        clearTimeout(timer)
        resolve({ ok: true, ws, frame })
      }
    }
    ws.onclose = (ev) => {
      clearTimeout(timer)
      resolve({ ok: false, code: ev.code, reason: ev.reason })
    }
  })
}

async function login() {
  const res = await fetch(`${PROXY}/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email: EMAIL, password: PASSWORD }),
  })
  const body = res.ok ? await res.json() : null
  return { res, body }
}

async function main() {
  // C1: login returns 200 with access_token; refresh cookie attributes.
  const { res: loginRes, body: loginBody } = await login()
  const setCookies = loginRes.headers.getSetCookie()
  const cookie = setCookies.find((c) => c.startsWith('fleetops_refresh='))
  const attrs = (cookie ?? '').toLowerCase()
  const c1 = loginRes.status === 200 && typeof loginBody?.access_token === 'string' &&
    attrs.includes('path=/auth') && attrs.includes('httponly') && attrs.includes('samesite=strict')
  check('C1', c1, `status=${loginRes.status} access_token=${loginBody?.access_token ? 'present' : 'missing'} cookie_attrs=path=/auth,httponly,samesite=strict:${c1}`)
  if (!c1) process.exit(1)

  const token = loginBody.access_token
  const claims = claimsOf(token)
  const fleetId = claims.fleet_id

  // C2: REST through the proxy on both hostnames.
  for (const base of [PROXY, PROXY_LOCALHOST]) {
    const r = await fetch(`${base}/map/vehicles?bbox=${WORLD}`, { headers: { Authorization: `Bearer ${token}` } })
    const j = r.ok ? await r.json() : null
    check('C2', r.status === 200 && Array.isArray(j?.vehicles), `${base} status=${r.status} vehicles=${j?.vehicles?.length ?? 'n/a'}`)
  }

  // C3: world box reaches 50 vehicles within 60 s of simulator start.
  const deadline = Date.now() + 60_000
  let last = null
  while (Date.now() < deadline) {
    const r = await fetch(`${API}/map/vehicles?bbox=${WORLD}`, { headers: { Authorization: `Bearer ${token}` } })
    last = await r.json()
    if (last.vehicles.length === 50 && last.truncated === false) break
    await new Promise((resolve) => setTimeout(resolve, 2000))
  }
  const allValid = last?.vehicles?.every((v) => ['idle', 'en_route', 'offline'].includes(v.status))
  const c3 = last?.vehicles?.length === 50 && last?.truncated === false && allValid
  check('C3', c3, `vehicles=${last?.vehicles?.length} truncated=${last?.truncated} statuses_valid=${allValid}`)

  // C4: refresh rotates; the old cookie value is then rejected (D-02 reuse).
  const oldCookie = cookie.split(';')[0]
  const r1 = await fetch(`${PROXY}/auth/refresh`, { method: 'POST', headers: { Cookie: oldCookie } })
  const rotated = r1.headers.getSetCookie().find((c) => c.startsWith('fleetops_refresh='))
  const r2 = await fetch(`${PROXY}/auth/refresh`, { method: 'POST', headers: { Cookie: oldCookie } })
  check('C4', r1.status === 200 && Boolean(rotated) && r2.status === 401,
    `first=${r1.status} new_cookie=${Boolean(rotated)} reuse=${r2.status}`)

  // C6: WS auth through the proxy and direct, fleet and role checked.
  const direct = await connect(`${HUB_DIRECT}/stream?fleet=${fleetId}`, token)
  const proxied = await connect(`${HUB_PROXY}/stream?fleet=${fleetId}`, token)
  const c6 = direct.ok && proxied.ok &&
    [direct, proxied].every((s) => s.frame.fleet_id === fleetId && OPS_ROLES.has(s.frame.role))
  check('C6', c6, `direct=${direct.ok ? `auth.ok role=${direct.frame.role}` : `close ${direct.code}`} proxy=${proxied.ok ? `auth.ok role=${proxied.frame.role}` : `close ${proxied.code}`}`)
  if (!c6) process.exit(1)

  // C8 + C9: collect for the window on both sockets, recording every frame type.
  const types = new Map()
  const lags = { direct: [], proxy: [] }
  const seenIds = { direct: new Set(), proxy: new Set() }
  const dupes = { direct: 0, proxy: 0 }
  const maxBatch = { direct: 0, proxy: 0 }
  const badParse = { count: 0 }

  function collect(name, sock) {
    sock.ws.onmessage = (ev) => {
      const receivedAt = Date.now()
      let frame
      try {
        frame = JSON.parse(ev.data)
      } catch {
        badParse.count += 1
        return
      }
      types.set(frame.type, (types.get(frame.type) ?? 0) + 1)
      if (frame.type !== 'position.batch') return
      maxBatch[name] = Math.max(maxBatch[name], frame.points.length)
      for (const p of frame.points) {
        if (seenIds[name].has(p.msg_id)) dupes[name] += 1
        seenIds[name].add(p.msg_id)
        lags[name].push(receivedAt - Date.parse(p.device_ts))
      }
    }
  }
  collect('direct', direct)
  collect('proxy', proxied)
  console.log(`collecting ${WINDOW_S}s of frames from direct and proxy sockets`)
  await new Promise((resolve) => setTimeout(resolve, WINDOW_S * 1000))
  direct.ws.close(1000, 'window done')
  proxied.ws.close(1000, 'window done')

  const stats = {}
  for (const name of ['direct', 'proxy']) {
    const sorted = lags[name].slice().sort((a, b) => a - b)
    stats[name] = {
      count: sorted.length,
      p50: percentile(sorted, 0.5),
      p95: percentile(sorted, 0.95),
      max: sorted.at(-1) ?? NaN,
      duplicates: dupes[name],
      max_batch: maxBatch[name],
    }
  }
  console.log(JSON.stringify(stats))
  const c8 = ['direct', 'proxy'].every((n) => stats[n].count > 0 && stats[n].p95 <= 2000 && stats[n].duplicates === 0 && stats[n].max_batch <= 200)
  check('C8', c8, `direct p50=${stats.direct.p50}ms p95=${stats.direct.p95}ms max=${stats.direct.max}ms n=${stats.direct.count} proxy p95=${stats.proxy.p95}ms n=${stats.proxy.count} duplicates=${stats.direct.duplicates + stats.proxy.duplicates} max_batch=${Math.max(stats.direct.max_batch, stats.proxy.max_batch)}`)

  const typeList = [...types.keys()]
  const c9 = typeList.every((t) => KNOWN_TYPES.has(t)) && badParse.count === 0
  check('C9', c9, `types=${JSON.stringify(Object.fromEntries(types))} unparseable=${badParse.count}`)

  // C7: negative auth. Bad token closes 4401; fleet mismatch closes 4403.
  const bad = await connect(`${HUB_DIRECT}/stream?fleet=${fleetId}`, 'not-a-token')
  const mismatch = await connect(`${HUB_DIRECT}/stream?fleet=999999`, token)
  check('C7', bad.ok === false && bad.code === 4401 && mismatch.ok === false && mismatch.code === 4403,
    `bad_token=${bad.code} fleet_mismatch=${mismatch.code}`)

  const failed = results.filter((r) => !r.ok).map((r) => r.id)
  console.log(failed.length === 0 ? 'ALL PASS' : `FAILED: ${failed.join(', ')}`)
  process.exit(failed.length === 0 ? 0 : 1)
}

main().catch((err) => {
  console.error('check-live crashed:', err)
  process.exit(1)
})
