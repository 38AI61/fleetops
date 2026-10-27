import type { PositionBody } from '../api/types'

// The coordinate fields the mapper reads. A GeolocationCoordinates satisfies it.
export interface FixCoords {
  latitude: number
  longitude: number
  accuracy: number
  speed: number | null
  heading: number | null
}

// The ingest schema requires numbers, so a null or NaN speed or heading becomes 0.
function numberOrZero(value: number | null): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : 0
}

// Pure. ts is the fix time in RFC3339 (GF-232, GF-56). The body has exactly these fields.
export function fixToIngestBody(coords: FixCoords, timestampMs: number): PositionBody {
  return {
    lat: coords.latitude,
    lng: coords.longitude,
    speed: numberOrZero(coords.speed),
    heading: numberOrZero(coords.heading),
    accuracy: coords.accuracy,
    ts: new Date(timestampMs).toISOString(),
  }
}
