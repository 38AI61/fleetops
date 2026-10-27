import { fireEvent, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { DEVICE_KEY_STORAGE } from './device/key'
import { markBooted, setSession } from './session/session'
import { renderAt } from './test/render'
import { signInAs } from './test/session'

// Fixture only: id 12, secret of 43 'A' characters.
const VALID = `fo_dev_12_${'A'.repeat(43)}`

describe('App route gating', () => {
  beforeEach(() => {
    localStorage.clear()
    setSession(null)
    markBooted()
    vi.stubGlobal('fetch', vi.fn(async () => new Response('{"jobs":[]}', { status: 200 })))
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    localStorage.clear()
    setSession(null)
  })

  it('shows sign-in when there is no session, and makes no job call', () => {
    renderAt('/today')
    expect(screen.getByRole('heading', { name: 'FleetOps Driver' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Sign in' })).toBeInTheDocument()
    expect(fetch).not.toHaveBeenCalled()
  })

  it('sends a driver with no device key to key setup', () => {
    signInAs('driver')
    renderAt('/today')
    expect(screen.getByRole('heading', { name: 'Device key' })).toBeInTheDocument()
  })

  it('blocks a non-driver session with a message and no job call', () => {
    signInAs('dispatcher')
    localStorage.setItem(DEVICE_KEY_STORAGE, VALID)
    renderAt('/today')
    expect(screen.getByText('This app is for drivers. Sign in with a driver account.')).toBeInTheDocument()
    expect(fetch).not.toHaveBeenCalled()
  })

  it('opens today for a driver with a session and a device key', () => {
    signInAs('driver')
    localStorage.setItem(DEVICE_KEY_STORAGE, VALID)
    renderAt('/')
    expect(screen.getByRole('heading', { name: 'Today' })).toBeInTheDocument()
  })

  it('a sign-out from the blocked screen returns to sign-in', async () => {
    signInAs('dispatcher')
    renderAt('/today')
    fireEvent.click(screen.getByRole('button', { name: 'Sign out' }))
    expect(await screen.findByRole('button', { name: 'Sign in' })).toBeInTheDocument()
  })
})
