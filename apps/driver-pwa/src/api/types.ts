// Wire types for the driver routes (P11, P12) and auth (P2). Field names match
// the Go JSON tags; see internal/jobs/store.go and internal/auth/http.go.

export interface TokenResponse {
  access_token: string
  token_type: string
  expires_in: number
}

// Claims are read for UI gating only. The server enforces every route.
export interface Claims {
  sub: string
  fleet_id: number
  role: string
  exp: number
}

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

// One job as GET /driver/jobs/today returns it (GF-173, GF-196).
export interface DriverJob {
  id: number
  state: JobState
  pickup: LatLng
  dropoff: LatLng
  window_start: string | null
  window_end: string | null
  notes: string | null
  size_class: string | null
  weight_kg: number | null
  failure_reason: string | null
  delivered_at: string | null
  proof_photo_url: string | null
}

// The body POST /ingest/position takes (GF-232). Every field is required.
export interface PositionBody {
  lat: number
  lng: number
  speed: number
  heading: number
  accuracy: number
  ts: string
}
