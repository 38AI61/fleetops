import { fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '../api/client'
import { setSession } from '../auth/session'
import { ReportsPage } from './ReportsPage'

// The query result the hook returns in each test. The page reads only its states.
const state = vi.hoisted(() => ({ value: null as unknown }))

vi.mock('./api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./api')>()),
  useDailyReport: () => state.value,
}))

// The topbar bell needs a QueryClient this page test does not provide. Its own tests cover it.
vi.mock('../alerts/AlertBell', () => ({ AlertBell: () => null }))

function signIn() {
  setSession({
    accessToken: 'token',
    expiresAt: Date.now() + 60_000,
    claims: { sub: '1', fleet_id: 1, role: 'admin', exp: Math.floor(Date.now() / 1000) + 60 },
  })
}

function renderPage() {
  render(
    <MemoryRouter>
      <ReportsPage />
    </MemoryRouter>,
  )
}

function success(rows: unknown[]) {
  return { isPending: false, isError: false, isSuccess: true, error: null, data: { date: '2026-03-08', rows } }
}

describe('ReportsPage (P21)', () => {
  afterEach(() => {
    state.value = null
  })

  it('renders a driver row, with a dash for a null on-time percentage', () => {
    signIn()
    state.value = success([
      {
        driver_id: 1,
        driver_name: 'Ada',
        jobs_completed: 4,
        on_time_count: 1,
        on_time_denom: 2,
        on_time_pct: 50,
        distance_m: 667100,
        idle_s: 180,
      },
      {
        driver_id: 3,
        driver_name: 'Cy',
        jobs_completed: 1,
        on_time_count: 0,
        on_time_denom: 0,
        on_time_pct: null,
        distance_m: 0,
        idle_s: 0,
      },
    ])
    renderPage()
    expect(screen.getByText('Ada')).toBeTruthy()
    expect(screen.getByText('1 of 2 (50%)')).toBeTruthy()
    expect(screen.getByText('667.1')).toBeTruthy()
    expect(screen.getByText('—')).toBeTruthy()
  })

  it('says so for an empty day', () => {
    signIn()
    state.value = success([])
    renderPage()
    expect(screen.getByText('No driving or deliveries were recorded for this day.')).toBeTruthy()
  })

  it('shows the role message on 403', () => {
    signIn()
    state.value = {
      isPending: false,
      isError: true,
      isSuccess: false,
      error: new ApiError(403, 'forbidden'),
      data: undefined,
    }
    renderPage()
    expect(screen.getByText('This report is not available for your role.')).toBeTruthy()
  })

  it('links the CSV export to the selected date', () => {
    signIn()
    state.value = success([])
    renderPage()
    fireEvent.change(screen.getByLabelText('Date'), { target: { value: '2026-03-08' } })
    const link = screen.getByRole('link', { name: 'Download CSV' })
    expect(link.getAttribute('href')).toBe('/reports/daily?date=2026-03-08&format=csv')
  })
})
