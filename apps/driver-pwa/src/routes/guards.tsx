import { Navigate, Outlet, useLocation, useNavigate } from 'react-router'
import { loadDeviceKey } from '../device/key'
import { PositionReporter } from '../position/PositionReporter'
import { logout } from '../session/logout'
import { useAuthState } from '../session/session'

export const NOT_DRIVER_MESSAGE = 'This app is for drivers. Sign in with a driver account.'

// Session gate (GF-227). Waits for the boot refresh, then: no session → /login;
// a session whose role is not driver → blocked screen, and no job call is made.
export function RequireSession() {
  const { session, booted } = useAuthState()
  const location = useLocation()
  if (!booted) return <p role="status">Loading…</p>
  if (!session) return <Navigate to="/login" replace state={{ from: location.pathname }} />
  if (session.claims.role !== 'driver') return <BlockedPage />
  return <Outlet />
}

// Device gate (GF-228). A session with no stored device key goes to key setup. With
// a key, the position reporter mounts here and runs only while on shift (GF-232).
export function RequireDeviceKey() {
  const navigate = useNavigate()
  const deviceKey = loadDeviceKey()
  if (!deviceKey) return <Navigate to="/setup-key" replace />
  return (
    <>
      <PositionReporter
        deviceKey={deviceKey}
        onKeyRejected={() => navigate('/setup-key', { replace: true, state: { rejected: true } })}
      />
      <Outlet />
    </>
  )
}

function BlockedPage() {
  const navigate = useNavigate()

  async function signOut() {
    await logout()
    navigate('/login', { replace: true })
  }

  return (
    <main>
      <h1>FleetOps Driver</h1>
      <p role="alert">{NOT_DRIVER_MESSAGE}</p>
      <button type="button" onClick={() => void signOut()}>
        Sign out
      </button>
    </main>
  )
}
