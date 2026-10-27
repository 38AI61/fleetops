import { useState, type FormEvent } from 'react'
import { useLocation, useNavigate } from 'react-router'
import { saveDeviceKey, validateDeviceKey } from '../device/key'

const MESSAGES = {
  empty: 'Enter the device key your dispatcher issued.',
  format: 'That is not a valid device key. Check it and try again.',
} as const

// Route state set by the position reporter when the server rejects the key (GF-228).
interface SetupState {
  rejected?: boolean
}

export function KeySetupPage() {
  const navigate = useNavigate()
  const location = useLocation()
  const rejected = (location.state as SetupState | null)?.rejected === true
  const [raw, setRaw] = useState('')
  const [error, setError] = useState<string | null>(null)

  function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const check = validateDeviceKey(raw)
    if (!check.ok) {
      setError(MESSAGES[check.reason])
      return
    }
    saveDeviceKey(check.key)
    navigate('/today', { replace: true })
  }

  return (
    <main>
      <h1>Device key</h1>
      {rejected && (
        <p role="alert">Device key was rejected. Enter the key your dispatcher issued.</p>
      )}
      <form onSubmit={onSubmit} noValidate>
        <label htmlFor="device-key">Device key</label>
        <input
          id="device-key"
          name="deviceKey"
          type="text"
          autoComplete="off"
          autoCapitalize="off"
          spellCheck={false}
          value={raw}
          onChange={(event) => {
            setRaw(event.target.value)
            setError(null)
          }}
        />
        {error && <p role="alert">{error}</p>}
        <button type="submit">Save device key</button>
      </form>
    </main>
  )
}
