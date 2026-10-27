import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { setSession, currentAccessToken, type Session } from '../session/session'
import { cancelProactive } from '../session/refresh'
import { token } from '../test/session'
import { getJson, ApiError } from './client'

const OLD_TOKEN = token({ sub: '1', fleet_id: 1, role: 'driver', exp: 4102444800 })
const NEW_TOKEN = token({ sub: '1', fleet_id: 1, role: 'driver', exp: 4102444800, rev: 2 })

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function signIn(): void {
  const session: Session = {
    accessToken: OLD_TOKEN,
    expiresAt: Date.now() + 900_000,
    claims: { sub: '1', fleet_id: 1, role: 'driver', exp: 4102444800 },
  }
  setSession(session)
}

beforeEach(() => {
  setSession(null)
})

afterEach(() => {
  cancelProactive()
  vi.unstubAllGlobals()
})

describe('getJson error mapping', () => {
  it('maps the server error code onto ApiError', async () => {
    signIn()
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse(409, { error: 'conflict' })))

    const err = await getJson('/driver/jobs/today').catch((e: unknown) => e)

    expect(err).toBeInstanceOf(ApiError)
    expect(err).toMatchObject({ status: 409, code: 'conflict' })
  })

  it('falls back to http_error when the body is not JSON', async () => {
    signIn()
    vi.stubGlobal('fetch', vi.fn(async () => new Response('gateway down', { status: 502 })))

    await expect(getJson('/driver/jobs/today')).rejects.toMatchObject({
      status: 502,
      code: 'http_error',
    })
  })

  it('falls back to http_error when the body has no error string', async () => {
    signIn()
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse(400, { detail: 'x' })))

    await expect(getJson('/driver/jobs/today')).rejects.toMatchObject({
      status: 400,
      code: 'http_error',
    })
  })
})

describe('401 handling', () => {
  it('clears the session when refresh also fails', async () => {
    signIn()
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url === '/driver/jobs/today') return jsonResponse(401, { error: 'unauthorized' })
      if (url === '/auth/refresh') return jsonResponse(401, { error: 'unauthorized' })
      return new Response(null, { status: 204 })
    })
    vi.stubGlobal('fetch', fetchMock)

    await expect(getJson('/driver/jobs/today')).rejects.toMatchObject({ status: 401 })

    expect(currentAccessToken()).toBeNull()
    expect(fetchMock).toHaveBeenCalledWith('/auth/logout', expect.objectContaining({ method: 'POST' }))
  })

  it('refreshes once and retries with the new token', async () => {
    signIn()
    const seen: string[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input)
        if (url === '/auth/refresh') {
          return jsonResponse(200, { access_token: NEW_TOKEN, token_type: 'Bearer', expires_in: 900 })
        }
        const auth = new Headers(init?.headers).get('Authorization') ?? ''
        seen.push(auth)
        if (auth === `Bearer ${OLD_TOKEN}`) return jsonResponse(401, { error: 'unauthorized' })
        return jsonResponse(200, { jobs: [] })
      }),
    )

    await expect(getJson<{ jobs: unknown[] }>('/driver/jobs/today')).resolves.toEqual({ jobs: [] })

    expect(seen).toEqual([`Bearer ${OLD_TOKEN}`, `Bearer ${NEW_TOKEN}`])
    expect(currentAccessToken()).toBe(NEW_TOKEN)
  })
})
