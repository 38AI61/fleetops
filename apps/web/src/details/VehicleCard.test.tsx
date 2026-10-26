import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { VehicleRow } from '../api/types'
import { VehicleCard } from './VehicleCard'

const SERVER_TS = '2026-10-26T10:00:00.000Z'
const SERVER_MS = Date.parse(SERVER_TS)

function snapshot(overrides: Partial<VehicleRow> = {}): VehicleRow {
  return {
    vehicle_id: 7,
    lat: 52.52,
    lng: 13.405,
    speed_kmh: 42,
    heading_deg: 271.5,
    status: 'idle',
    device_ts: SERVER_TS,
    server_ts: SERVER_TS,
    ...overrides,
  }
}

describe('VehicleCard', () => {
  it('VehicleCard renders exactly the GF-130 fields', () => {
    const { container } = render(
      <VehicleCard snapshot={snapshot()} nowMs={SERVER_MS + 5_000} onClose={() => undefined} />,
    )
    const labels = Array.from(container.querySelectorAll('dt')).map((dt) => dt.textContent)
    expect(labels).toEqual([
      'Vehicle',
      'Latitude',
      'Longitude',
      'Speed',
      'Heading',
      'Status',
      'Device time',
      'Server time',
      'Age',
    ])
    expect(screen.getByText('52.520000')).toBeInTheDocument()
    expect(screen.getByText('13.405000')).toBeInTheDocument()
    expect(screen.getByText('Idle')).toBeInTheDocument()
    expect(screen.getByText('5s')).toBeInTheDocument()
  })

  it('VehicleCard shows a dash for null speed and heading', () => {
    render(
      <VehicleCard
        snapshot={snapshot({ speed_kmh: null, heading_deg: null })}
        nowMs={SERVER_MS}
        onClose={() => undefined}
      />,
    )
    expect(screen.getAllByText('—')).toHaveLength(2)
  })

  it('VehicleCard closes on Escape and on the close button', () => {
    const onClose = vi.fn()
    render(<VehicleCard snapshot={snapshot()} nowMs={SERVER_MS} onClose={onClose} />)
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(onClose).toHaveBeenCalledTimes(1)
    fireEvent.click(screen.getByRole('button', { name: 'Close' }))
    expect(onClose).toHaveBeenCalledTimes(2)
  })

  it('VehicleCard shows the stale badge when grayed', () => {
    const { rerender } = render(
      <VehicleCard snapshot={snapshot()} nowMs={SERVER_MS + 60_000} onClose={() => undefined} />,
    )
    expect(screen.queryByText('Stale')).not.toBeInTheDocument()
    rerender(<VehicleCard snapshot={snapshot()} nowMs={SERVER_MS + 121_000} onClose={() => undefined} />)
    expect(screen.getByText('Stale')).toBeInTheDocument()
  })
})
