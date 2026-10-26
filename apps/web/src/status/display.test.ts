import { describe, expect, it } from 'vitest'
import type { VehicleRow } from '../api/types'
import { isGrayed, statusColor, toFeatureCollection } from './display'

const SERVER_TS = '2026-10-26T10:00:00.000Z'
const SERVER_MS = Date.parse(SERVER_TS)

function row(overrides: Partial<VehicleRow> = {}): VehicleRow {
  return {
    vehicle_id: 1,
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

describe('status display', () => {
  it('statusColor maps idle, en_route, offline to the GF-157 hexes', () => {
    expect(statusColor('idle')).toBe('#2563eb')
    expect(statusColor('en_route')).toBe('#f59e0b')
    expect(statusColor('offline')).toBe('#9ca3af')
  })

  it('isGrayed is false at exactly 120000 ms and true at 120001 ms', () => {
    expect(isGrayed(row(), SERVER_MS + 120_000)).toBe(false)
    expect(isGrayed(row(), SERVER_MS + 120_001)).toBe(true)
  })

  it('isGrayed is true for status offline regardless of age', () => {
    expect(isGrayed(row({ status: 'offline' }), SERVER_MS)).toBe(true)
  })

  it('isGrayed is false for negative age (clock skew)', () => {
    expect(isGrayed(row(), SERVER_MS - 5_000)).toBe(false)
  })

  it('toFeatureCollection sets color and grayed per vehicle', () => {
    const fresh = row({ vehicle_id: 1, status: 'idle' })
    const silent = row({ vehicle_id: 2, status: 'idle' })
    const enRoute = row({ vehicle_id: 3, status: 'en_route' })
    const fc = toFeatureCollection([fresh, silent, enRoute], SERVER_MS + 1_000)
    expect(fc.features.map((f) => f.properties)).toEqual([
      { vehicle_id: 1, status: 'idle', grayed: false, color: '#2563eb' },
      { vehicle_id: 2, status: 'idle', grayed: false, color: '#2563eb' },
      { vehicle_id: 3, status: 'en_route', grayed: false, color: '#f59e0b' },
    ])
    const later = toFeatureCollection([fresh], SERVER_MS + 121_000)
    expect(later.features[0]?.properties).toEqual({
      vehicle_id: 1,
      status: 'idle',
      grayed: true,
      color: '#9ca3af',
    })
    expect(fc.features[0]?.geometry.coordinates).toEqual([13.405, 52.52])
  })
})
