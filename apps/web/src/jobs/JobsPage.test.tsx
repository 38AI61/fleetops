import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fetchNearest, listJobs } from '../api/jobs'
import type { Job } from '../api/types'
import { JobsPage } from './JobsPage'

vi.mock('../api/jobs', () => ({
  listJobs: vi.fn(),
  fetchNearest: vi.fn(),
  assignJob: vi.fn(),
  createJob: vi.fn(),
}))

function job(over: Partial<Job>): Job {
  return {
    id: 1,
    fleet_id: 1,
    state: 'created',
    vehicle_id: null,
    driver_id: null,
    pickup: { lat: 41, lng: 29 },
    dropoff: { lat: 41.1, lng: 29.1 },
    window_start: null,
    window_end: null,
    eta_minutes: null,
    notes: null,
    size_class: null,
    weight_kg: null,
    failure_reason: null,
    delivered_at: null,
    created_at: '2026-10-27T08:00:00.000Z',
    proof_photo_url: null,
    ...over,
  }
}

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <JobsPage />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('JobsPage', () => {
  beforeEach(() => {
    vi.mocked(listJobs).mockReset()
    vi.mocked(fetchNearest).mockReset()
    vi.mocked(listJobs).mockResolvedValue([
      job({ id: 3, state: 'created' }),
      job({ id: 4, state: 'delivered', driver_id: 9, vehicle_id: 2 }),
    ])
  })

  it('filters change the query arguments', async () => {
    renderPage()
    await screen.findByText('created')
    expect(listJobs).toHaveBeenLastCalledWith({ state: undefined, driver: undefined, day: undefined })

    fireEvent.change(screen.getByLabelText('State'), { target: { value: 'assigned' } })
    await waitFor(() => expect(listJobs).toHaveBeenLastCalledWith({ state: 'assigned', driver: undefined, day: undefined }))

    fireEvent.change(screen.getByLabelText('Driver id'), { target: { value: '7' } })
    await waitFor(() => expect(listJobs).toHaveBeenLastCalledWith({ state: 'assigned', driver: 7, day: undefined }))

    fireEvent.change(screen.getByLabelText('Day'), { target: { value: '2026-10-27' } })
    await waitFor(() => expect(listJobs).toHaveBeenLastCalledWith({ state: 'assigned', driver: 7, day: '2026-10-27' }))
  })

  it('opens the assignment panel for a selectable row only', async () => {
    vi.mocked(fetchNearest).mockResolvedValue({ job_id: 3, candidates: [] })
    renderPage()
    const table = await screen.findByRole('table')

    // Delivered rows have no select action.
    const deliveredRow = within(table).getByText('delivered').closest('tr') as HTMLElement
    expect(within(deliveredRow).queryByRole('button', { name: /Select job/ })).toBeNull()

    expect(screen.queryByRole('region', { name: 'Assignment' })).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'Select job 3' }))
    expect(await screen.findByRole('region', { name: 'Assignment' })).toBeInTheDocument()
    expect(await screen.findByText('No eligible vehicles')).toBeInTheDocument()
    expect(fetchNearest).toHaveBeenCalledWith(3)
  })
})
