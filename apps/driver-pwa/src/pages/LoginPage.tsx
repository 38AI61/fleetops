import { useState, type FormEvent } from 'react'
import { useNavigate } from 'react-router'
import { ApiError } from '../api/client'
import { login } from '../api/driver'
import { NOT_DRIVER_MESSAGE } from '../routes/guards'
import { logout } from '../session/logout'
import { applyToken } from '../session/refresh'
import { decodeClaims } from '../session/session'

const SIGN_IN_FAILED = 'Sign-in failed. Try again.'
const CANNOT_REACH = 'Cannot reach the server'

const CODE_MESSAGES: Record<string, string> = {
  invalid_credentials: 'Email or password is incorrect',
  rate_limited: 'Too many attempts. Wait a moment and try again.',
}

export function LoginPage() {
  const navigate = useNavigate()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const body = await login(email, password)
      const claims = decodeClaims(body.access_token)
      if (!claims) {
        setError(SIGN_IN_FAILED)
        return
      }
      // A non-driver token is never stored. Revoke it server-side as the
      // dispatcher console does (GF-154), then show the blocked message.
      if (claims.role !== 'driver') {
        await logout()
        setError(NOT_DRIVER_MESSAGE)
        return
      }
      applyToken(body)
      navigate('/today', { replace: true })
    } catch (err) {
      if (err instanceof ApiError) {
        setError(CODE_MESSAGES[err.code] ?? SIGN_IN_FAILED)
      } else {
        setError(CANNOT_REACH)
      }
    } finally {
      setBusy(false)
    }
  }

  return (
    <main>
      <h1>FleetOps Driver</h1>
      <form onSubmit={(event) => void onSubmit(event)}>
        <label htmlFor="email">Email</label>
        <input
          id="email"
          name="email"
          type="email"
          autoComplete="username"
          required
          value={email}
          onChange={(event) => setEmail(event.target.value)}
        />
        <label htmlFor="password">Password</label>
        <input
          id="password"
          name="password"
          type="password"
          autoComplete="current-password"
          required
          value={password}
          onChange={(event) => setPassword(event.target.value)}
        />
        {error && <p role="alert">{error}</p>}
        <button type="submit" disabled={busy}>
          Sign in
        </button>
      </form>
    </main>
  )
}
