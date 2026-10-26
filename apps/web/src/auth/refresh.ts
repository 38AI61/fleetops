import type { TokenResponse } from '../api/types'
import { decodeClaims, setSession } from './session'

// Proactive refresh fires this long before the access token expires (GF-154).
const PROACTIVE_MARGIN_MS = 60_000

let inflight: Promise<boolean> | null = null
let timer: ReturnType<typeof setTimeout> | undefined

// Stores a token response in the session and re-arms the proactive timer.
export function applyToken(resp: TokenResponse): boolean {
  const claims = decodeClaims(resp.access_token)
  if (!claims) return false
  const expiresAt = Date.now() + resp.expires_in * 1000
  setSession({ accessToken: resp.access_token, expiresAt, claims })
  armProactive(expiresAt)
  return true
}

// One in-flight POST /auth/refresh. Every caller awaits the same promise, so
// the rotated refresh token is never presented twice (D-02, GF-154).
export function singleFlightRefresh(): Promise<boolean> {
  if (!inflight) {
    inflight = doRefresh().finally(() => {
      inflight = null
    })
  }
  return inflight
}

async function doRefresh(): Promise<boolean> {
  try {
    const res = await fetch('/auth/refresh', { method: 'POST', credentials: 'same-origin' })
    if (!res.ok) return false
    return applyToken((await res.json()) as TokenResponse)
  } catch {
    return false
  }
}

function armProactive(expiresAt: number): void {
  clearTimeout(timer)
  const delay = Math.max(0, expiresAt - PROACTIVE_MARGIN_MS - Date.now())
  timer = setTimeout(() => {
    void singleFlightRefresh()
  }, delay)
}

export function cancelProactive(): void {
  clearTimeout(timer)
  timer = undefined
}
