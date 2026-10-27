import { expect, request, test, type APIResponse, type BrowserContext } from '@playwright/test'
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

// One spec file, one test (D-37, GF-248). Helpers stay in this file.
// Stack: scripts/stack.sh starts the API, ws-hub, ingest and the two Vite dev servers.

const API = 'http://127.0.0.1:10010'
const WEB = 'http://127.0.0.1:10012'
const PWA = 'http://127.0.0.1:10013'

// A 1x1 PNG. The server sniffs the bytes, so this is a valid proof image.
// It is written to a temp file at run time; no binary is committed (GF-248).
const PROOF_PNG_BASE64 =
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=='

// The driver's browser fix. Deliver reads it through geolocation (GF-248).
const DRIVER_FIX = { latitude: 41.0082, longitude: 28.9784 }

interface JobView {
  id: number
  state: string
  delivered_at: string | null
  proof_photo_url: string | null
}

function requiredEnv(name: string): string {
  const value = process.env[name]
  if (!value) throw new Error(`${name} is not set; run this through make e2e`)
  return value
}

// Reports the status and body on failure. Request bodies are never printed (they hold passwords and keys).
async function ok<T>(res: APIResponse, what: string): Promise<T> {
  if (!res.ok()) throw new Error(`${what}: HTTP ${res.status()} ${await res.text()}`)
  return (await res.json()) as T
}

test('dispatcher creates and assigns a job, driver delivers it with proof', async ({ browser }) => {
  test.setTimeout(240_000)

  // Unique per run. Plates are uppercase, at most 16 characters (GF-35), so the suffix is base 36.
  const run = Date.now().toString(36).toUpperCase()
  const plate = `E2E-${run}`
  const dispatcherEmail = `dispatcher-${run.toLowerCase()}@fleetops.test`
  const dispatcherPassword = `e2e-dispatch-${run.toLowerCase()}-pw`
  const driverEmail = `driver-${run.toLowerCase()}@fleetops.test`
  const driverPassword = `e2e-driver-${run.toLowerCase()}-pw`

  const api = await request.newContext({ baseURL: API })
  const tmp = mkdtempSync(join(tmpdir(), 'fleetops-e2e-'))
  const photoPath = join(tmp, 'proof.png')
  writeFileSync(photoPath, Buffer.from(PROOF_PNG_BASE64, 'base64'))

  let dispatcherCtx: BrowserContext | undefined
  let driverCtx: BrowserContext | undefined

  try {
    // Provisioning through the admin API (GF-250). The seed creates only the admin.
    const login = await ok<{ access_token: string }>(
      await api.post('/auth/login', {
        data: { email: requiredEnv('E2E_ADMIN_EMAIL'), password: requiredEnv('E2E_ADMIN_PASSWORD') },
      }),
      'admin login',
    )
    const admin = { Authorization: `Bearer ${login.access_token}` }

    const dispatcher = await ok<{ id: number }>(
      await api.post('/users', {
        headers: admin,
        data: { email: dispatcherEmail, password: dispatcherPassword, role: 'dispatcher' },
      }),
      'create dispatcher',
    )
    expect(dispatcher.id).toBeGreaterThan(0)

    const driverUser = await ok<{ id: number }>(
      await api.post('/users', {
        headers: admin,
        data: { email: driverEmail, password: driverPassword, role: 'driver' },
      }),
      'create driver user',
    )
    const driver = await ok<{ id: number }>(
      await api.post('/drivers', {
        headers: admin,
        data: {
          name: `E2E Driver ${run}`,
          license_no: `LIC-${run}`,
          phone: '+905550000000',
          shift_start: '06:00',
          shift_end: '20:00',
        },
      }),
      'create driver',
    )
    await ok(
      await api.patch(`/users/${driverUser.id}`, { headers: admin, data: { driver_id: driver.id } }),
      'link driver user to driver',
    )

    const vehicle = await ok<{ id: number }>(
      await api.post('/vehicles', { headers: admin, data: { plate, model: 'E2E', status: 'active' } }),
      'create vehicle',
    )
    const tracker = await ok<{ id: number; api_key: string }>(
      await api.post('/devices', { headers: admin, data: { kind: 'tracker' } }),
      'issue tracker device',
    )
    // The pwa key comes back once and is kept in memory for KeySetup (D-30).
    const pwa = await ok<{ id: number; api_key: string }>(
      await api.post('/devices', { headers: admin, data: { kind: 'pwa' } }),
      'issue pwa device',
    )
    await ok(
      await api.post(`/vehicles/${vehicle.id}/pairings`, {
        headers: admin,
        data: { device_id: tracker.id, driver_id: driver.id },
      }),
      'pair tracker to vehicle',
    )
    // Ingest is asynchronous. The assignment panel poll in step 4 covers the latency.
    const fix = await api.post('/ingest/position', {
      headers: { 'X-Device-Key': tracker.api_key },
      data: {
        lat: DRIVER_FIX.latitude,
        lng: DRIVER_FIX.longitude,
        speed: 0,
        heading: 0,
        accuracy: 5,
        ts: new Date().toISOString(),
      },
    })
    expect(fix.ok(), `ingest position: HTTP ${fix.status()}`).toBeTruthy()

    // 1. Dispatcher login.
    dispatcherCtx = await browser.newContext({ baseURL: WEB })
    const dispatcherPage = await dispatcherCtx.newPage()
    await dispatcherPage.goto('/login')
    await dispatcherPage.getByLabel('Email').fill(dispatcherEmail)
    await dispatcherPage.getByLabel('Password').fill(dispatcherPassword)
    await dispatcherPage.getByRole('button', { name: 'Sign in' }).click()
    await expect(dispatcherPage).toHaveURL(/\/map$/)

    // 2. Create the job by clicking pickup and dropoff on the map (GF-258).
    // Vite proxies full page loads of /jobs and /map to the API (CLAUDE.md), so the dispatcher
    // moves between these pages with in-app links only.
    await dispatcherPage.getByRole('link', { name: 'Jobs' }).click()
    await expect(dispatcherPage).toHaveURL(/\/jobs$/)
    await dispatcherPage.getByRole('link', { name: 'New job' }).click()
    await expect(dispatcherPage).toHaveURL(/\/jobs\/new$/)
    const canvas = dispatcherPage.locator('.new-job-map .map-canvas')
    await expect(canvas).toBeVisible()
    const box = await canvas.boundingBox()
    if (!box) throw new Error('map canvas has no bounding box')
    await canvas.click({ position: { x: box.width * 0.35, y: box.height * 0.5 } })
    await canvas.click({ position: { x: box.width * 0.65, y: box.height * 0.5 } })
    const form = dispatcherPage.getByRole('form', { name: 'New job' })
    await expect(form).toContainText('Pickup:')
    await expect(form).not.toContainText('Pickup: not set')
    await expect(form).toContainText('Dropoff:')
    await expect(form).not.toContainText('Dropoff: not set')
    await dispatcherPage.getByRole('button', { name: 'Create job' }).click()
    await expect(dispatcherPage).toHaveURL(/\/jobs$/)

    // 3. The job just created is the highest id still in state created.
    const created = await ok<{ jobs: JobView[] }>(
      await api.get('/jobs?state=created', { headers: admin }),
      'list created jobs',
    )
    expect(created.jobs.length).toBeGreaterThan(0)
    const jobId = Math.max(...created.jobs.map((j) => j.id))
    await dispatcherPage.getByRole('button', { name: `Select job ${jobId}` }).click()

    // 4. The candidate list appears once ingest has written the position. Poll for up to 60 s.
    // Each round remounts the list through Map -> Jobs, which refetches it (a reload of /jobs is proxied).
    const panel = dispatcherPage.locator('section[aria-label="Assignment"]')
    const candidate = panel.getByRole('radio', { name: `Select ${plate}` })
    const deadline = Date.now() + 60_000
    await dispatcherPage.getByRole('button', { name: `Select job ${jobId}` }).click()
    while (!(await candidate.isVisible().catch(() => false))) {
      if (Date.now() > deadline) throw new Error(`candidate ${plate} did not appear within 60s`)
      await dispatcherPage.waitForTimeout(2_000)
      await dispatcherPage.getByRole('link', { name: 'Map' }).click()
      await dispatcherPage.getByRole('link', { name: 'Jobs' }).click()
      await dispatcherPage.getByRole('button', { name: `Select job ${jobId}` }).click()
    }
    await candidate.check()
    await panel.getByRole('button', { name: 'Assign' }).click()
    const dispatcherRow = dispatcherPage.getByRole('row').filter({
      has: dispatcherPage.getByRole('cell', { name: String(jobId), exact: true }),
    })
    await expect(dispatcherRow.getByRole('cell', { name: 'assigned', exact: true })).toBeVisible()

    // 5-6. Driver login, then device key setup. /setup-key sits behind the session guard,
    // so login comes first: it lands on /today, which sends a driver with no key to /setup-key.
    driverCtx = await browser.newContext({
      baseURL: PWA,
      isMobile: true,
      hasTouch: true,
      permissions: ['geolocation'],
      geolocation: DRIVER_FIX,
    })
    const driverPage = await driverCtx.newPage()
    await driverPage.goto('/login')
    await driverPage.getByLabel('Email').fill(driverEmail)
    await driverPage.getByLabel('Password').fill(driverPassword)
    await driverPage.getByRole('button', { name: 'Sign in' }).click()
    await expect(driverPage).toHaveURL(/\/setup-key$/)
    await driverPage.getByLabel('Device key').fill(pwa.api_key)
    await driverPage.getByRole('button', { name: 'Save device key' }).click()
    await expect(driverPage).toHaveURL(/\/today$/)

    const jobItem = driverPage.getByRole('listitem').filter({
      hasText: new RegExp(`^Job ${jobId}(\\s|$)`),
    })
    await expect(jobItem).toContainText('Assigned')

    // 7. Accept, start, pick up.
    await jobItem.getByRole('button', { name: 'Accept' }).click()
    await expect(jobItem).toContainText('Accepted')
    await jobItem.getByRole('button', { name: 'Start' }).click()
    await expect(jobItem).toContainText('En route')
    await jobItem.getByRole('button', { name: 'Pick up' }).click()
    await expect(jobItem).toContainText('Picked up')

    // 8. Deliver with the proof photo.
    await jobItem.getByRole('link', { name: 'Deliver' }).click()
    await expect(driverPage).toHaveURL(new RegExp(`/jobs/${jobId}/deliver$`))
    await driverPage.locator('#proof-photo').setInputFiles(photoPath)
    await driverPage.getByRole('button', { name: 'Confirm delivery' }).click()
    await expect(driverPage).toHaveURL(/\/today$/)

    // 10a. Dispatcher sees the job delivered.
    await dispatcherPage.getByRole('link', { name: 'Map' }).click()
    await dispatcherPage.getByRole('link', { name: 'Jobs' }).click()
    const deliveredRow = dispatcherPage.getByRole('row').filter({
      has: dispatcherPage.getByRole('cell', { name: String(jobId), exact: true }),
    })
    await expect(deliveredRow.getByRole('cell', { name: 'delivered', exact: true })).toBeVisible()

    // 10b. The API has the delivery time and a presigned proof URL (GF-259).
    const listed = await ok<{ jobs: JobView[] }>(await api.get('/jobs', { headers: admin }), 'list jobs')
    const job = listed.jobs.find((j) => j.id === jobId)
    expect(job?.state).toBe('delivered')
    expect(job?.delivered_at).not.toBeNull()
    expect(job?.proof_photo_url).toBeTruthy()
  } finally {
    await dispatcherCtx?.close()
    await driverCtx?.close()
    await api.dispose()
    rmSync(tmp, { recursive: true, force: true })
  }
})
