export interface Bounds {
  west: number
  south: number
  east: number
  north: number
}

// Used for wrapped or full-width views, which the API cannot express (GF-156).
const WORLD_BOX = '-180,-90,180,90'

function clamp(value: number, lo: number, hi: number): number {
  return Math.min(hi, Math.max(lo, value))
}

// Grammar matches GF-127: minLng,minLat,maxLng,maxLat with six decimals.
// Zero-area boxes are sent as the world box, because the API rejects them.
export function bboxParam(bounds: Bounds): string {
  const west = clamp(bounds.west, -180, 180)
  const east = clamp(bounds.east, -180, 180)
  const south = clamp(bounds.south, -90, 90)
  const north = clamp(bounds.north, -90, 90)
  if (west >= east || south >= north || east - west >= 360) return WORLD_BOX
  return [west, south, east, north].map((v) => v.toFixed(6)).join(',')
}
