import { fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { DEVICE_KEY_STORAGE } from '../device/key'
import { KeySetupPage } from './KeySetupPage'

// Fixture only: id 12, secret of 43 'A' characters.
const VALID = `fo_dev_12_${'A'.repeat(43)}`

function renderPage(state?: unknown) {
  return render(
    <MemoryRouter initialEntries={[{ pathname: '/setup-key', state }]}>
      <Routes>
        <Route path="/setup-key" element={<KeySetupPage />} />
        <Route path="/today" element={<p>today page</p>} />
      </Routes>
    </MemoryRouter>,
  )
}

describe('KeySetupPage', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    localStorage.clear()
  })

  it('blocks an invalid key with a message, no storage write, and no request', () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    const setItem = vi.spyOn(localStorage, 'setItem')
    renderPage()

    fireEvent.change(screen.getByLabelText('Device key'), { target: { value: 'fo_tracker_12_x' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save device key' }))

    expect(screen.getByText('That is not a valid device key. Check it and try again.')).toBeInTheDocument()
    expect(setItem).not.toHaveBeenCalled()
    expect(fetchMock).not.toHaveBeenCalled()
    expect(localStorage.getItem(DEVICE_KEY_STORAGE)).toBeNull()
    setItem.mockRestore()
  })

  it('shows the empty message for a blank key', () => {
    renderPage()
    fireEvent.click(screen.getByRole('button', { name: 'Save device key' }))
    expect(screen.getByText('Enter the device key your dispatcher issued.')).toBeInTheDocument()
  })

  it('stores a valid key trimmed and goes to /today', () => {
    renderPage()

    fireEvent.change(screen.getByLabelText('Device key'), { target: { value: `  ${VALID}  ` } })
    fireEvent.click(screen.getByRole('button', { name: 'Save device key' }))

    expect(localStorage.getItem(DEVICE_KEY_STORAGE)).toBe(VALID)
    expect(screen.getByText('today page')).toBeInTheDocument()
  })

  it('explains a rejected key when routed here from the reporter', () => {
    renderPage({ rejected: true })
    expect(
      screen.getByText('Device key was rejected. Enter the key your dispatcher issued.'),
    ).toBeInTheDocument()
  })
})
