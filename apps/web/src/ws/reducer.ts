import type { DisplayStatus, VehicleRow } from '../api/types'

// Bounded ring of seen msg_id values for at-least-once replay (GF-163).
export const SEEN_CAP = 10_000
const BACKOFF_CAP_MS = 30_000

export type WsEffect = 'refetch-map' | 'refetch-vehicles'

export interface WsState {
  connected: boolean
  fleetId: number
  seen: Set<string>
  seenOrder: string[]
}

export interface DeviceIndex {
  deviceToVehicle: Map<number, number>
}

export interface ReduceResult {
  state: WsState
  rows: VehicleRow[] | null
  effects: WsEffect[]
}

export function initialWsState(fleetId: number): WsState {
  return { connected: false, fleetId, seen: new Set(), seenOrder: [] }
}

interface WirePoint {
  msg_id: string
  device_id: number
  lat: number
  lng: number
  speed_kmh: number | null
  heading_deg: number | null
  device_ts: string
  server_ts: string
}

interface WireBatch {
  type: 'position.batch'
  fleet_id: number
  points: WirePoint[]
}

function unchanged(state: WsState): ReduceResult {
  return { state, rows: null, effects: [] }
}

function parseEnvelope(raw: string): Record<string, unknown> | null {
  try {
    const value: unknown = JSON.parse(raw)
    if (typeof value !== 'object' || value === null || Array.isArray(value)) return null
    return value as Record<string, unknown>
  } catch {
    return null
  }
}

// Pure. Unknown types, malformed JSON, and non-objects return the state
// unchanged with no effects and no throw (GF-163). P10 and P11 add types.
export function reduceWsEvent(
  state: WsState,
  raw: string,
  index: DeviceIndex,
  rows: VehicleRow[],
): ReduceResult {
  const msg = parseEnvelope(raw)
  if (!msg) return unchanged(state)
  switch (msg.type) {
    case 'auth.ok':
      return { state: { ...state, connected: true }, rows: null, effects: [] }
    case 'presence.changed':
      // No row mutation. Status follows the server on the next map refetch (GF-164).
      return { state, rows: null, effects: ['refetch-map'] }
    case 'position.batch':
      return applyBatch(state, msg as unknown as WireBatch, index, rows)
    default:
      return unchanged(state)
  }
}

function applyBatch(
  state: WsState,
  batch: WireBatch,
  index: DeviceIndex,
  rows: VehicleRow[],
): ReduceResult {
  if (batch.fleet_id !== state.fleetId || !Array.isArray(batch.points)) return unchanged(state)

  const seen = new Set(state.seen)
  const seenOrder = state.seenOrder.slice()
  const next = rows.slice()
  const position = new Map<number, number>()
  rows.forEach((row, i) => position.set(row.vehicle_id, i))

  const effects = new Set<WsEffect>()
  let changed = false

  for (const point of batch.points) {
    if (seen.has(point.msg_id)) continue
    seen.add(point.msg_id)
    seenOrder.push(point.msg_id)
    if (seenOrder.length > SEEN_CAP) {
      const evicted = seenOrder.shift()
      if (evicted !== undefined) seen.delete(evicted)
    }

    const vehicleId = index.deviceToVehicle.get(point.device_id)
    if (vehicleId === undefined) {
      effects.add('refetch-vehicles')
      continue
    }
    const i = position.get(vehicleId)
    // Vehicles outside the active box are not in the cache and are not added.
    if (i === undefined) continue
    const row = next[i]
    if (!row) continue
    if (Date.parse(point.device_ts) < Date.parse(row.device_ts)) continue

    const status: DisplayStatus = row.status === 'offline' ? 'idle' : row.status
    next[i] = {
      ...row,
      lat: point.lat,
      lng: point.lng,
      speed_kmh: point.speed_kmh,
      heading_deg: point.heading_deg,
      device_ts: point.device_ts,
      server_ts: point.server_ts,
      status,
    }
    changed = true
  }

  if (changed) effects.add('refetch-map')
  return {
    state: { connected: state.connected, fleetId: state.fleetId, seen, seenOrder },
    rows: changed ? next : null,
    effects: [...effects],
  }
}

// min(30 s, 1 s · 2^attempt), jittered by ±20%, then capped at 30 s (GF-162).
// rand returns a value in [0, 1).
export function backoffDelay(attempt: number, rand: () => number): number {
  const base = Math.min(BACKOFF_CAP_MS, 1000 * 2 ** attempt)
  const jittered = base * (0.8 + 0.4 * rand())
  return Math.min(BACKOFF_CAP_MS, jittered)
}
