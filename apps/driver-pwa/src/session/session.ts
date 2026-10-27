import { useSyncExternalStore } from 'react'
import type { Claims } from '../api/types'

// The access token lives only in this module store (GF-227). Nothing is written
// to localStorage, sessionStorage or a JS-readable cookie.
export interface Session {
  accessToken: string
  expiresAt: number
  claims: Claims
}

export interface AuthState {
  session: Session | null
  booted: boolean
}

let state: AuthState = { session: null, booted: false }
const listeners = new Set<() => void>()

function emit(): void {
  for (const listener of listeners) listener()
}

export function getAuthState(): AuthState {
  return state
}

export function subscribeAuth(listener: () => void): () => void {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

export function setSession(session: Session | null): void {
  state = { ...state, session }
  emit()
}

export function markBooted(): void {
  if (state.booted) return
  state = { ...state, booted: true }
  emit()
}

export function currentAccessToken(): string | null {
  return state.session?.accessToken ?? null
}

// Re-renders a component when the session or boot flag changes.
export function useAuthState(): AuthState {
  return useSyncExternalStore(subscribeAuth, getAuthState)
}

// Decodes the JWT payload for UI gating only. There is no signature check; the
// server enforces every route (GF-227).
export function decodeClaims(token: string): Claims | null {
  const part = token.split('.')[1]
  if (!part) return null
  try {
    const b64 = part.replace(/-/g, '+').replace(/_/g, '/')
    const padded = b64 + '='.repeat((4 - (b64.length % 4)) % 4)
    const payload = JSON.parse(atob(padded)) as Partial<Claims>
    if (
      typeof payload.fleet_id !== 'number' ||
      typeof payload.role !== 'string' ||
      typeof payload.exp !== 'number'
    ) {
      return null
    }
    return payload as Claims
  } catch {
    return null
  }
}
