import { useState, type FormEvent } from 'react'
import { apiFetch } from '../api/client'
import type { TokenResponse } from '../api/types'
import { logout } from './logout'
import { applyToken } from './refresh'
import { decodeClaims } from './session'

const SIGN_IN_FAILED = 'Sign-in failed. Try again.'

export function LoginPage() {
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setBusy(true)
    setError(null)
    let res: Response
    try {
      res = await apiFetch('/auth/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ email, password }),
      })
    } catch {
      setError('Cannot reach the server')
      setBusy(false)
      return
    }
    if (res.status === 401) {
      setError('Email or password is incorrect')
      setBusy(false)
      return
    }
    if (!res.ok) {
      setError(SIGN_IN_FAILED)
      setBusy(false)
      return
    }
    const body = (await res.json()) as TokenResponse
    const claims = decodeClaims(body.access_token)
    if (!claims) {
      setError(SIGN_IN_FAILED)
      setBusy(false)
      return
    }
    // Driver role is refused before any session exists. No map or vehicle
    // request is made (GF-154).
    if (claims.role === 'driver') {
      await logout()
      setError('The dispatcher console requires an operations role.')
      setBusy(false)
      return
    }
    applyToken(body)
  }

  return (
    <main className="login">
      <h1>FleetOps dispatcher</h1>
      <form onSubmit={onSubmit}>
        <label>
          Email
          <input
            type="email"
            name="email"
            autoComplete="username"
            required
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
        </label>
        <label>
          Password
          <input
            type="password"
            name="password"
            autoComplete="current-password"
            required
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </label>
        {error ? (
          <p role="alert" className="error">
            {error}
          </p>
        ) : null}
        <button type="submit" disabled={busy}>
          Sign in
        </button>
      </form>
    </main>
  )
}
