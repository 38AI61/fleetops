import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '../api/client'
import { ackAlert, listAlerts, type Alert, type AlertPage } from './api'
import { AlertBell } from './AlertBell'

// The bell talks to the API only through ./api, so the test mocks that module.
vi.mock('./api', () => ({
  listAlerts: vi.fn(),
  ackAlert: vi.fn(),
}))

const listMock = vi.mocked(listAlerts)
const ackMock = vi.mocked(ackAlert)

const alert: Alert = {
  id: 7,
  fleet_id: 1,
  kind: 'device_silent',
  severity: 'warning',
  payload: { vehicle_id: 3 },
  raised_at: '2026-10-28T09:11:40Z',
  acked_by: null,
  acked_at: null,
}

function page(over: Partial<AlertPage> = {}): AlertPage {
  return { alerts: [alert], unacked: 1, ...over }
}

function renderBell() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <AlertBell />
    </QueryClientProvider>,
  )
  return client
}

describe('AlertBell', () => {
  beforeEach(() => {
    listMock.mockReset()
    ackMock.mockReset()
  })

  afterEach(() => {
    vi.clearAllMocks()
  })

  it('shows the unacked count as the badge', async () => {
    listMock.mockResolvedValue(page({ unacked: 4 }))
    renderBell()
    expect(await screen.findByLabelText('4 unacked')).toHaveTextContent('4')
  })

  it('lists kind and severity when opened', async () => {
    listMock.mockResolvedValue(page())
    renderBell()
    await screen.findByLabelText('1 unacked')
    fireEvent.click(screen.getByRole('button', { name: 'Alerts' }))
    expect(screen.getByText('device_silent')).toBeInTheDocument()
    expect(screen.getByText('warning')).toBeInTheDocument()
  })

  it('Ack calls the PATCH and refreshes the list', async () => {
    listMock.mockResolvedValueOnce(page()).mockResolvedValue(page({ unacked: 0, alerts: [{ ...alert, acked_at: '2026-10-28T09:20:00Z', acked_by: 5 }] }))
    ackMock.mockResolvedValue({ ...alert, acked_at: '2026-10-28T09:20:00Z', acked_by: 5 })
    renderBell()
    await screen.findByLabelText('1 unacked')
    fireEvent.click(screen.getByRole('button', { name: 'Alerts' }))
    fireEvent.click(screen.getByRole('button', { name: 'Ack' }))

    await waitFor(() => expect(ackMock).toHaveBeenCalledWith(7))
    await waitFor(() => expect(listMock).toHaveBeenCalledTimes(2))
    expect(await screen.findByText('Acknowledged')).toBeInTheDocument()
  })

  it('shows a message when the ack answers 409', async () => {
    listMock.mockResolvedValue(page())
    ackMock.mockRejectedValue(new ApiError(409, 'conflict'))
    renderBell()
    await screen.findByLabelText('1 unacked')
    fireEvent.click(screen.getByRole('button', { name: 'Alerts' }))
    fireEvent.click(screen.getByRole('button', { name: 'Ack' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('already acknowledged')
  })

  it('renders no bell when the request answers 401', async () => {
    listMock.mockRejectedValue(new ApiError(401, 'unauthorized'))
    renderBell()
    await waitFor(() => expect(listMock).toHaveBeenCalled())
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Alerts' })).not.toBeInTheDocument())
    expect(screen.queryByLabelText(/unacked/)).not.toBeInTheDocument()
  })
})
