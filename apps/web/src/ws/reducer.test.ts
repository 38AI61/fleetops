import { describe, expect, it } from 'vitest'
import type { VehicleRow } from '../api/types'
import { backoffDelay, initialWsState, reduceWsEvent, type DeviceIndex } from './reducer'

const FLEET = 1
const T0 = '2026-10-26T10:00:00.000Z'
const T1 = '2026-10-26T10:00:05.000Z'

function row(vehicleId: number, overrides: Partial<VehicleRow> = {}): VehicleRow {
  return {
    vehicle_id: vehicleId,
    lat: 52.5,
    lng: 13.4,
    speed_kmh: 10,
    heading_deg: 90,
    status: 'idle',
    device_ts: T0,
    server_ts: T0,
    ...overrides,
  }
}

function point(msgId: string, deviceId: number, deviceTs: string, lat = 52.6, lng = 13.5) {
  return {
    msg_id: msgId,
    device_id: deviceId,
    lat,
    lng,
    speed_kmh: 42,
    heading_deg: 271.5,
    accuracy_m: 5,
    device_ts: deviceTs,
    server_ts: deviceTs,
  }
}

function batch(points: ReturnType<typeof point>[], fleetId = FLEET): string {
  return JSON.stringify({ type: 'position.batch', fleet_id: fleetId, points })
}

const index: DeviceIndex = { deviceToVehicle: new Map([[101, 1], [102, 2]]) }

describe('ws reducer', () => {
  it('position.batch merges points into rows by device-to-vehicle index', () => {
    const state = initialWsState(FLEET)
    const rows = [row(1), row(2)]
    const res = reduceWsEvent(state, batch([point('m1', 101, T1)]), index, rows)
    expect(res.rows?.[0]).toMatchObject({ vehicle_id: 1, lat: 52.6, lng: 13.5, device_ts: T1, speed_kmh: 42 })
    expect(res.rows?.[1]).toEqual(rows[1])
    expect(res.effects).toEqual(['refetch-map'])
    expect(rows[0]?.lat).toBe(52.5)
  })

  it('a replayed msg_id is skipped', () => {
    const rows = [row(1)]
    const first = reduceWsEvent(initialWsState(FLEET), batch([point('m1', 101, T1)]), index, rows)
    const second = reduceWsEvent(first.state, batch([point('m1', 101, T1)]), index, first.rows ?? rows)
    expect(second.rows).toBeNull()
    expect(second.effects).toEqual([])
  })

  it('an older device_ts does not overwrite a newer row', () => {
    const rows = [row(1, { device_ts: T1, server_ts: T1 })]
    const res = reduceWsEvent(initialWsState(FLEET), batch([point('m1', 101, T0, 1, 1)]), index, rows)
    expect(res.rows).toBeNull()
  })

  it('presence.changed does not mutate rows and requests a map refetch', () => {
    const rows = [row(1)]
    const raw = JSON.stringify({ type: 'presence.changed', fleet_id: FLEET, device_id: 101, state: 'offline', ts: T1 })
    const res = reduceWsEvent(initialWsState(FLEET), raw, index, rows)
    expect(res.rows).toBeNull()
    expect(res.effects).toEqual(['refetch-map'])
  })

  it('unknown event type is ignored without throwing', () => {
    const state = initialWsState(FLEET)
    for (const raw of ['{"type":"job.updated","job_id":1}', 'not json', '42', '[1,2]']) {
      const res = reduceWsEvent(state, raw, index, [row(1)])
      expect(res).toEqual({ state, rows: null, effects: [] })
    }
  })

  it('a 200-point batch is applied atomically in one call', () => {
    const rows = Array.from({ length: 200 }, (_, i) => row(i + 1))
    const idx: DeviceIndex = { deviceToVehicle: new Map(rows.map((r) => [r.vehicle_id + 1000, r.vehicle_id])) }
    const points = rows.map((r) => point(`m${r.vehicle_id}`, r.vehicle_id + 1000, T1, 40, 20))
    const res = reduceWsEvent(initialWsState(FLEET), batch(points), idx, rows)
    expect(res.rows).toHaveLength(200)
    expect(res.rows?.every((r) => r.lat === 40 && r.lng === 20 && r.device_ts === T1)).toBe(true)
    expect(res.state.seen.size).toBe(200)
  })

  it('auth.ok marks the state connected', () => {
    const res = reduceWsEvent(initialWsState(FLEET), '{"type":"auth.ok","fleet_id":1,"role":"dispatcher"}', index, [])
    expect(res.state.connected).toBe(true)
    expect(res.rows).toBeNull()
  })

  it('backoffDelay caps at 30 s and stays within jitter bounds', () => {
    expect(backoffDelay(0, () => 0.5)).toBe(1000)
    expect(backoffDelay(0, () => 0)).toBe(800)
    expect(backoffDelay(2, () => 1)).toBeCloseTo(4800, 6)
    expect(backoffDelay(20, () => 1)).toBe(30_000)
    expect(backoffDelay(20, () => 0)).toBe(24_000)
  })
})
