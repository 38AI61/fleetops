// Wire shapes the dispatcher console consumes. Field names mirror the Go JSON tags.

export type DisplayStatus = 'idle' | 'en_route' | 'offline'

export interface Claims {
  sub: string
  fleet_id: number
  role: string
  exp: number
}

export interface TokenResponse {
  access_token: string
  token_type: string
  expires_in: number
}

// GF-130 row. Timestamps are RFC3339 UTC with milliseconds.
export interface VehicleRow {
  vehicle_id: number
  lat: number
  lng: number
  speed_kmh: number | null
  heading_deg: number | null
  status: DisplayStatus
  device_ts: string
  server_ts: string
}

export interface MapVehiclesResult {
  vehicles: VehicleRow[]
  truncated: boolean
}

// GET /vehicles (registry). Only paired_device_id is used by the UI.
export interface Vehicle {
  id: number
  fleet_id: number
  plate: string
  model: string | null
  status: string
  paired_device_id: number | null
}
