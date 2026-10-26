import { useSyncExternalStore, type ReactNode } from 'react'
import { Navigate } from 'react-router'
import { LoginPage } from './LoginPage'
import { getAuthState, subscribeAuth } from './session'

function useAuthState() {
  return useSyncExternalStore(subscribeAuth, getAuthState)
}

// Renders children only with a session. Also sends logout back to /login.
export function RequireSession({ children }: { children: ReactNode }) {
  const { session, booted } = useAuthState()
  if (!booted) return <p>Loading</p>
  if (!session) return <Navigate to="/login" replace />
  return children
}

// /login: Loading during boot, /map when a session exists, the form otherwise.
export function LoginRoute() {
  const { session, booted } = useAuthState()
  if (!booted) return <p>Loading</p>
  if (session) return <Navigate to="/map" replace />
  return <LoginPage />
}
