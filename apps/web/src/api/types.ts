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

// Job state vocabulary (D-23). Mirrors internal/jobs.
export type JobState =
  | 'created'
  | 'assigned'
  | 'accepted'
  | 'en_route'
  | 'picked_up'
  | 'delivered'
  | 'failed'
  | 'cancelled'

export interface LatLng {
  lat: number
  lng: number
}

// GET /jobs row and mutation reply (GF-173, GF-196). Timestamps are RFC3339 strings or null.
export interface Job {
  id: number
  fleet_id: number
  state: JobState
  vehicle_id: number | null
  driver_id: number | null
  pickup: LatLng
  dropoff: LatLng
  window_start: string | null
  window_end: string | null
  eta_minutes: number | null
  notes: string | null
  size_class: string | null
  weight_kg: number | null
  failure_reason: string | null
  delivered_at: string | null
  created_at: string
  proof_photo_url: string | null
}

// POST /jobs body (GF-174). Optional fields are omitted when unset.
export interface JobCreateBody {
  pickup: LatLng
  dropoff: LatLng
  window_start?: string
  window_end?: string
  notes?: string
  size_class?: 'small' | 'medium' | 'large'
  weight_kg?: number
}

// GET /jobs filters (GF-170). Unset fields are not sent.
export interface JobsFilter {
  state?: JobState
  driver?: number
  day?: string
}

// GET /jobs/:id/nearest row (GF-214). distance_m is rounded to 0.1 m.
export interface NearestCandidate {
  vehicle_id: number
  plate: string
  driver_id: number
  driver_name: string
  distance_m: number
  lat: number
  lng: number
  server_ts: string
}

export interface NearestResult {
  job_id: number
  candidates: NearestCandidate[]
}
