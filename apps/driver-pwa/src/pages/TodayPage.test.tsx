import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '../api/client'
import { listTodayJobs, transitionJob } from '../api/driver'
import type { DriverJob, JobState } from '../api/types'
import { DEVICE_KEY_STORAGE } from '../device/key'
import { setSession } from '../session/session'
import { ON_SHIFT_STORAGE } from '../position/shift'
import { renderAt } from '../test/render'
import { TodayPage } from './TodayPage'
import { signInAs } from '../test/session'

vi.mock('../api/driver', () => ({ listTodayJobs: vi.fn(), transitionJob: vi.fn() }))

// Fixture only: id 12, secret of 43 'A' characters.
const VALID = `fo_dev_12_${'A'.repeat(43)}`

function job(id: number, state: JobState): DriverJob {
  return {
    id,
    state,
    pickup: { lat: 41.0, lng: 29.0 },
    dropoff: { lat: 41.1, lng: 29.1 },
    window_start: null,
    window_end: null,
    notes: null,
    size_class: null,
    weight_kg: null,
    failure_reason: null,
    delivered_at: null,
    proof_photo_url: null,
  }
}

function row(id: number): HTMLElement {
  return screen.getByText(`Job ${id}`).closest('li') as HTMLElement
}

// Waits for the list to load, then returns the row.
async function findRow(id: number): Promise<HTMLElement> {
  await screen.findByText(`Job ${id}`)
  return row(id)
}

describe('TodayPage', () => {
  beforeEach(() => {
    localStorage.clear()
    localStorage.setItem(DEVICE_KEY_STORAGE, VALID)
    signInAs('driver')
    vi.mocked(listTodayJobs).mockReset()
    vi.mocked(transitionJob).mockReset()
  })

  afterEach(() => {
    setSession(null)
    localStorage.clear()
  })

  it('shows the one button each state offers', async () => {
    vi.mocked(listTodayJobs).mockResolvedValue([
      job(1, 'assigned'),
      job(2, 'accepted'),
      job(3, 'en_route'),
      job(4, 'picked_up'),
      job(5, 'created'),
    ])
    renderAt('/today')

    expect(await within(await findRow(1)).findByRole('button', { name: 'Accept' })).toBeInTheDocument()
    expect(within(row(2)).getByRole('button', { name: 'Start' })).toBeInTheDocument()
    expect(within(row(3)).getByRole('button', { name: 'Pick up' })).toBeInTheDocument()
    expect(within(row(4)).getByRole('link', { name: 'Deliver' })).toHaveAttribute(
      'href',
      '/jobs/4/deliver',
    )
    expect(within(row(5)).queryByRole('button')).toBeNull()
    expect(within(row(5)).queryByRole('link')).toBeNull()
  })

  it('a tap calls transitionJob with the job id and the action', async () => {
    vi.mocked(listTodayJobs).mockResolvedValue([job(1, 'assigned')])
    vi.mocked(transitionJob).mockResolvedValue({ ...job(1, 'accepted') })
    renderAt('/today')

    fireEvent.click(await within(await findRow(1)).findByRole('button', { name: 'Accept' }))

    await waitFor(() => expect(transitionJob).toHaveBeenCalledWith(1, 'accept'))
  })

  it('disables the tapped button while the move is pending', async () => {
    vi.mocked(listTodayJobs).mockResolvedValue([job(1, 'accepted')])
    vi.mocked(transitionJob).mockReturnValue(new Promise(() => {}))
    renderAt('/today')

    fireEvent.click(await within(await findRow(1)).findByRole('button', { name: 'Start' }))

    await waitFor(() => expect(within(row(1)).getByRole('button', { name: 'Start' })).toBeDisabled())
  })

  it('shows the empty message for an empty list', async () => {
    vi.mocked(listTodayJobs).mockResolvedValue([])
    renderAt('/today')
    expect(await screen.findByText('No jobs for today.')).toBeInTheDocument()
  })

  it('a 409 shows a message and refetches the list', async () => {
    vi.mocked(listTodayJobs).mockResolvedValueOnce([job(1, 'assigned')])
    vi.mocked(listTodayJobs).mockResolvedValueOnce([job(1, 'created')])
    vi.mocked(transitionJob).mockRejectedValue(new ApiError(409, 'conflict'))
    renderAt('/today')

    fireEvent.click(await within(await findRow(1)).findByRole('button', { name: 'Accept' }))

    expect(await screen.findByText('Job changed; refreshed')).toBeInTheDocument()
    await waitFor(() => expect(listTodayJobs).toHaveBeenCalledTimes(2))
  })

  it('stores the on-shift flag when the toggle is switched on', async () => {
    vi.mocked(listTodayJobs).mockResolvedValue([])
    renderAt('/today')

    fireEvent.click(await screen.findByRole('checkbox', { name: 'On shift' }))

    expect(localStorage.getItem(ON_SHIFT_STORAGE)).toBe('1')
  })

  it('disables the toggle with a message when no device key is stored', async () => {
    // The route guard sends a keyless driver to setup, so render the page alone.
    localStorage.removeItem(DEVICE_KEY_STORAGE)
    vi.mocked(listTodayJobs).mockResolvedValue([])
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <MemoryRouter>
          <TodayPage />
        </MemoryRouter>
      </QueryClientProvider>,
    )

    expect(await screen.findByRole('checkbox', { name: 'On shift' })).toBeDisabled()
    expect(screen.getByText('Set up the device key first')).toBeInTheDocument()
  })
})
