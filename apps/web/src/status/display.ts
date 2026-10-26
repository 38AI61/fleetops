import type { DisplayStatus, VehicleRow } from '../api/types'

// Client gray-out threshold (GF-160). A code constant, not an env var.
export const CLIENT_STALE_MS = 120_000

export const COLOR_IDLE = '#2563eb'
export const COLOR_EN_ROUTE = '#f59e0b'
export const COLOR_GRAY = '#9ca3af'

export function statusColor(status: DisplayStatus): string {
  switch (status) {
    case 'idle':
      return COLOR_IDLE
    case 'en_route':
      return COLOR_EN_ROUTE
    case 'offline':
      return COLOR_GRAY
  }
}

// Gray when the server says offline, or when server_ts is older than 120 s.
// Negative age (clock skew) is not grayed (GF-160).
export function isGrayed(v: Pick<VehicleRow, 'status' | 'server_ts'>, nowMs: number): boolean {
  if (v.status === 'offline') return true
  return nowMs - Date.parse(v.server_ts) > CLIENT_STALE_MS
}

export interface VehicleProperties {
  vehicle_id: number
  status: DisplayStatus
  grayed: boolean
  color: string
}

export interface VehicleFeature {
  type: 'Feature'
  geometry: { type: 'Point'; coordinates: [number, number] }
  properties: VehicleProperties
}

export interface VehicleCollection {
  type: 'FeatureCollection'
  features: VehicleFeature[]
}

// Pure. Layers read ['get','color'] and ['get','grayed'] (GF-157).
export function toFeatureCollection(vehicles: VehicleRow[], nowMs: number): VehicleCollection {
  return {
    type: 'FeatureCollection',
    features: vehicles.map((v) => {
      const grayed = isGrayed(v, nowMs)
      return {
        type: 'Feature',
        geometry: { type: 'Point', coordinates: [v.lng, v.lat] },
        properties: {
          vehicle_id: v.vehicle_id,
          status: v.status,
          grayed,
          color: grayed ? COLOR_GRAY : statusColor(v.status),
        },
      }
    }),
  }
}
