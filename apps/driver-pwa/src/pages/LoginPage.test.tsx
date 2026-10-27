import { fireEvent, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '../api/client'
import { login } from '../api/driver'
import { getAuthState, markBooted, setSession } from '../session/session'
import { renderAt } from '../test/render'
import { token } from '../test/session'

vi.mock('../api/driver', () => ({ login: vi.fn() }))

function fillAndSubmit() {
  fireEvent.change(screen.getByLabelText('Email'), { target: { value: 'driver@example.test' } })
  fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'pw' } })
  fireEvent.click(screen.getByRole('button', { name: 'Sign in' }))
}

describe('LoginPage', () => {
  beforeEach(() => {
    localStorage.clear()
    setSession(null)
    markBooted()
    vi.stubGlobal('fetch', vi.fn(async () => new Response(null, { status: 204 })))
    vi.mocked(login).mockReset()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    setSession(null)
  })

  it('shows the invalid_credentials message on 401', async () => {
    vi.mocked(login).mockRejectedValue(new ApiError(401, 'invalid_credentials'))
    renderAt('/login')

    fillAndSubmit()

    expect(await screen.findByText('Email or password is incorrect')).toBeInTheDocument()
    expect(getAuthState().session).toBeNull()
  })

  it('blocks a non-driver role and stores no session', async () => {
    const dispatcher = token({ sub: '3', fleet_id: 1, role: 'dispatcher', exp: 4102444800 })
    vi.mocked(login).mockResolvedValue({ access_token: dispatcher, token_type: 'Bearer', expires_in: 900 })
    renderAt('/login')

    fillAndSubmit()

    expect(
      await screen.findByText('This app is for drivers. Sign in with a driver account.'),
    ).toBeInTheDocument()
    expect(getAuthState().session).toBeNull()
    await waitFor(() => expect(fetch).toHaveBeenCalledWith('/auth/logout', expect.anything()))
    expect(fetch).not.toHaveBeenCalledWith(expect.stringContaining('/driver/'), expect.anything())
  })
})
