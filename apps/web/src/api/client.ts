import { logout } from '../auth/logout'
import { singleFlightRefresh } from '../auth/refresh'
import { currentAccessToken } from '../auth/session'

export class ApiError extends Error {
  readonly status: number
  readonly code: string

  constructor(status: number, code: string) {
    super(code)
    this.status = status
    this.code = code
  }
}

async function send(path: string, init: RequestInit): Promise<Response> {
  const headers = new Headers(init.headers)
  const token = currentAccessToken()
  if (token) headers.set('Authorization', `Bearer ${token}`)
  return fetch(path, { ...init, headers, credentials: 'same-origin' })
}

// On 401, refresh once and retry. A second 401 logs out locally (GF-154).
export async function apiFetch(path: string, init: RequestInit = {}): Promise<Response> {
  const first = await send(path, init)
  if (first.status !== 401 || path === '/auth/login') return first
  if (!(await singleFlightRefresh())) {
    await logout()
    return first
  }
  const second = await send(path, init)
  if (second.status === 401) await logout()
  return second
}

export async function getJson<T>(path: string): Promise<T> {
  const res = await apiFetch(path)
  if (!res.ok) throw new ApiError(res.status, await errorCode(res))
  return (await res.json()) as T
}

// Mutations send a JSON body and answer JSON. Errors map to ApiError like getJson.
export async function sendJson<T>(method: string, path: string, body: unknown): Promise<T> {
  const res = await apiFetch(path, {
    method,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (!res.ok) throw new ApiError(res.status, await errorCode(res))
  return (await res.json()) as T
}

async function errorCode(res: Response): Promise<string> {
  try {
    const body = (await res.json()) as { error?: unknown }
    return typeof body.error === 'string' ? body.error : 'http_error'
  } catch {
    return 'http_error'
  }
}
