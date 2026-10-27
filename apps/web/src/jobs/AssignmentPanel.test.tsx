import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { assignJob, fetchNearest } from '../api/jobs'
import { ApiError } from '../api/client'
import type { Job, NearestCandidate } from '../api/types'
import { AssignmentPanel } from './AssignmentPanel'

vi.mock('../api/jobs', () => ({
  fetchNearest: vi.fn(),
  assignJob: vi.fn(),
  listJobs: vi.fn(),
  createJob: vi.fn(),
}))

// Server order is deliberately not distance order: the panel must not re-sort.
const candidates: NearestCandidate[] = [
  {
    vehicle_id: 11,
    plate: 'FAR-1',
    driver_id: 101,
    driver_name: 'Ayse',
    distance_m: 1234.5,
    lat: 41.01,
    lng: 28.98,
    server_ts: '2026-10-27T09:00:00.000Z',
  },
  {
    vehicle_id: 12,
    plate: 'NEAR-2',
    driver_id: 202,
    driver_name: 'Mehmet',
    distance_m: 250.4,
    lat: 41.009,
    lng: 28.978,
    server_ts: '2026-10-27T09:00:05.000Z',
  },
]

function renderPanel(onAssigned?: () => void) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <AssignmentPanel jobId={5} onAssigned={onAssigned} />
    </QueryClientProvider>,
  )
}

const assignBtn = () => screen.getByRole('button', { name: 'Assign' }) as HTMLButtonElement

describe('AssignmentPanel', () => {
  beforeEach(() => {
    vi.mocked(fetchNearest).mockReset()
    vi.mocked(assignJob).mockReset()
    vi.mocked(fetchNearest).mockResolvedValue({ job_id: 5, candidates })
  })

  it('renders rows in server order with distances formatted', async () => {
    renderPanel()
    await screen.findByText('FAR-1')
    const plates = screen.getAllByRole('row').slice(1).map((r) => r.querySelectorAll('td')[1]?.textContent)
    expect(plates).toEqual(['FAR-1', 'NEAR-2'])
    expect(screen.getByText('1.23 km')).toBeInTheDocument()
    expect(screen.getByText('250 m')).toBeInTheDocument()
    expect(fetchNearest).toHaveBeenCalledWith(5)
  })

  it('keeps assign disabled until a row is selected', async () => {
    renderPanel()
    await screen.findByText('FAR-1')
    expect(assignBtn()).toBeDisabled()
  })

  it('assigns the picked vehicle and driver', async () => {
    const job = { id: 5, state: 'assigned' } as Job
    vi.mocked(assignJob).mockResolvedValue(job)
    const onAssigned = vi.fn()
    renderPanel(onAssigned)
    await screen.findByText('NEAR-2')
    fireEvent.click(screen.getByRole('radio', { name: 'Select NEAR-2' }))
    fireEvent.click(assignBtn())
    await waitFor(() => expect(onAssigned).toHaveBeenCalledTimes(1))
    expect(assignJob).toHaveBeenCalledWith(5, 12, 202)
  })

  it('disables assign while the request is pending', async () => {
    vi.mocked(assignJob).mockReturnValue(new Promise(() => undefined))
    renderPanel()
    await screen.findByText('FAR-1')
    fireEvent.click(screen.getByRole('radio', { name: 'Select FAR-1' }))
    fireEvent.click(assignBtn())
    await waitFor(() => expect(assignBtn()).toBeDisabled())
  })

  it('maps vehicle_not_active to its message', async () => {
    vi.mocked(assignJob).mockRejectedValue(new ApiError(409, 'vehicle_not_active'))
    renderPanel()
    await screen.findByText('FAR-1')
    fireEvent.click(screen.getByRole('radio', { name: 'Select FAR-1' }))
    fireEvent.click(assignBtn())
    expect(await screen.findByText('Vehicle is no longer active')).toBeInTheDocument()
  })

  it('maps conflict and other errors', async () => {
    vi.mocked(assignJob).mockRejectedValueOnce(new ApiError(409, 'conflict'))
    renderPanel()
    await screen.findByText('FAR-1')
    fireEvent.click(screen.getByRole('radio', { name: 'Select FAR-1' }))
    fireEvent.click(assignBtn())
    expect(await screen.findByText('Job changed; refresh')).toBeInTheDocument()
  })

  it('shows the generic message for unmapped errors', async () => {
    vi.mocked(assignJob).mockRejectedValue(new ApiError(500, 'internal'))
    renderPanel()
    await screen.findByText('FAR-1')
    fireEvent.click(screen.getByRole('radio', { name: 'Select FAR-1' }))
    fireEvent.click(assignBtn())
    expect(await screen.findByText('Could not assign the job')).toBeInTheDocument()
  })
})
