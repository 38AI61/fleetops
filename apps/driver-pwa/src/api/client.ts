import { logout } from '../session/logout'
import { singleFlightRefresh } from '../session/refresh'
import { currentAccessToken } from '../session/session'

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

// On 401, refresh once and retry. A second 401 clears the session (GF-154 model).
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
  return expectJson<T>(await apiFetch(path))
}

// Mutations send a JSON body and answer JSON.
export async function sendJson<T>(method: string, path: string, body: unknown): Promise<T> {
  return expectJson<T>(
    await apiFetch(path, {
      method,
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    }),
  )
}

// Multipart mutations. The browser sets the Content-Type and boundary.
export async function sendForm<T>(method: string, path: string, form: FormData): Promise<T> {
  return expectJson<T>(await apiFetch(path, { method, body: form }))
}

// Maps a non-2xx response to ApiError with the server's error code.
export async function expectJson<T>(res: Response): Promise<T> {
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
