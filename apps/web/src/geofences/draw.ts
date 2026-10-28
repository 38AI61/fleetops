import type { LatLng } from '../api/types'

// Draw-to-GeoJSON for the zones page (GF-272). Pure, so it is testable without
// WebGL (GF-167). Coordinates are [lng, lat] (RFC 7946), the server's order.

export type LngLat = [number, number]

// Client bounds mirror the server (GF-265). The server stays authoritative.
export const MIN_RADIUS_M = 1
export const MAX_RADIUS_M = 100000
export const MAX_NAME_RUNES = 100

// A click within this many degrees of the first vertex closes a polygon (about 11 m).
const CLOSE_TOLERANCE_DEG = 0.0001

// Edge count for the display ring. circleRing returns 64 + the closing position.
const CIRCLE_SEGMENTS = 64
const EARTH_RADIUS_M = 6371008.8

export type DrawState =
  | { mode: 'idle' }
  | { mode: 'polygon'; vertices: LngLat[]; closed: boolean }
  | { mode: 'circle'; centre: LngLat | null }

export const idleDraw: DrawState = { mode: 'idle' }

export function startPolygon(): DrawState {
  return { mode: 'polygon', vertices: [], closed: false }
}

export function startCircle(): DrawState {
  return { mode: 'circle', centre: null }
}

// Escape cancels to idle from any mode (GF-272).
export function cancelDraw(): DrawState {
  return idleDraw
}

// A polygon click adds a vertex. A click on the first vertex closes the ring
// once there are at least 3 vertices, and that click is not added (GF-272).
export function clickPolygon(state: DrawState, point: LngLat): DrawState {
  if (state.mode !== 'polygon' || state.closed) return state
  const [first] = state.vertices
  if (first && state.vertices.length >= 3 && near(point, first)) {
    return { ...state, closed: true }
  }
  return { ...state, vertices: [...state.vertices, point] }
}

// Finish closes a polygon that has at least 3 vertices. With fewer it is a no-op.
export function finishPolygon(state: DrawState): DrawState {
  if (state.mode !== 'polygon' || state.closed || state.vertices.length < 3) return state
  return { ...state, closed: true }
}

// The first click places the centre. Later clicks are ignored; the radius comes from the number input.
export function clickCircle(state: DrawState, point: LngLat): DrawState {
  if (state.mode !== 'circle' || state.centre) return state
  return { mode: 'circle', centre: point }
}

// The closed polygon ring in [lng, lat] order, first position repeated last. Null until closed.
export function polygonRing(state: DrawState): LngLat[] | null {
  if (state.mode !== 'polygon' || !state.closed) return null
  return [...state.vertices, state.vertices[0] as LngLat]
}

export function isValidRadius(radiusM: number): boolean {
  return Number.isFinite(radiusM) && radiusM >= MIN_RADIUS_M && radiusM <= MAX_RADIUS_M
}

// The name is trimmed, and 1 to 100 runes (GF-265).
export function isValidName(name: string): boolean {
  const n = [...name.trim()].length
  return n >= 1 && n <= MAX_NAME_RUNES
}

// Display ring around a centre: 64 segments, closed, so 65 positions (GF-272).
// It is for drawing only. The stored shape is the centre plus radius.
export function circleRing(centre: LngLat, radiusM: number, segments = CIRCLE_SEGMENTS): LngLat[] {
  const ring: LngLat[] = []
  for (let i = 0; i < segments; i++) {
    ring.push(destination(centre, radiusM, (360 * i) / segments))
  }
  ring.push(ring[0] as LngLat)
  return ring
}

// A GeoJSON Feature ready for POST /geofences, or null when the draft is not complete.
export interface FenceFeature {
  type: 'Feature'
  geometry: { type: 'Polygon'; coordinates: LngLat[][] } | { type: 'Point'; coordinates: LngLat }
  properties: { name: string; kind: 'polygon' | 'circle'; radius_m?: number }
}

export function draftFeature(state: DrawState, radiusM: number, name: string): FenceFeature | null {
  if (!isValidName(name)) return null
  const trimmed = name.trim()
  const ring = polygonRing(state)
  if (ring) {
    return {
      type: 'Feature',
      geometry: { type: 'Polygon', coordinates: [ring] },
      properties: { name: trimmed, kind: 'polygon' },
    }
  }
  if (state.mode === 'circle' && state.centre && isValidRadius(radiusM)) {
    return {
      type: 'Feature',
      geometry: { type: 'Point', coordinates: state.centre },
      properties: { name: trimmed, kind: 'circle', radius_m: radiusM },
    }
  }
  return null
}

// Converts a map click to the [lng, lat] order the draw functions use.
export function toLngLat(p: LatLng): LngLat {
  return [p.lng, p.lat]
}

// GeoJSON for the draft source: the in-progress shape, drawn with the stored-fence layers' look.
export interface OverlayCollection {
  type: 'FeatureCollection'
  features: OverlayFeature[]
}

export type OverlayFeature = {
  type: 'Feature'
  properties: Record<string, unknown>
  geometry:
    | { type: 'Polygon'; coordinates: LngLat[][] }
    | { type: 'LineString'; coordinates: LngLat[] }
    | { type: 'Point'; coordinates: LngLat }
}

// Draft layers: polygon vertices and the open or closed outline, or the circle centre and its display ring.
export function draftCollection(state: DrawState, radiusM: number): OverlayCollection {
  const features: OverlayFeature[] = []
  if (state.mode === 'polygon') {
    for (const v of state.vertices) features.push(point(v, {}))
    const ring = polygonRing(state)
    if (ring) {
      features.push({ type: 'Feature', properties: {}, geometry: { type: 'Polygon', coordinates: [ring] } })
    } else if (state.vertices.length >= 2) {
      features.push({ type: 'Feature', properties: {}, geometry: { type: 'LineString', coordinates: state.vertices } })
    }
  } else if (state.mode === 'circle' && state.centre) {
    features.push(point(state.centre, {}))
    if (isValidRadius(radiusM)) {
      features.push({
        type: 'Feature',
        properties: {},
        geometry: { type: 'LineString', coordinates: circleRing(state.centre, radiusM) },
      })
    }
  }
  return { type: 'FeatureCollection', features }
}

// Stored fences for the read-only layer (GF-274). Polygons stay polygons. A
// circle becomes its centre point plus the display ring. The circle's
// stored geometry is not changed.
export interface StoredFence {
  id: number
  properties: { name: string; kind: string; radius_m?: number }
  geometry: { type: 'Polygon'; coordinates: LngLat[][] } | { type: 'Point'; coordinates: LngLat }
}

export function fencesCollection(fences: StoredFence[]): OverlayCollection {
  const features: OverlayFeature[] = []
  for (const f of fences) {
    const props = { id: f.id, name: f.properties.name, kind: f.properties.kind }
    if (f.geometry.type === 'Polygon') {
      features.push({ type: 'Feature', properties: props, geometry: f.geometry })
    } else {
      const radius = f.properties.radius_m ?? 0
      features.push(point(f.geometry.coordinates, props))
      features.push({
        type: 'Feature',
        properties: props,
        geometry: { type: 'LineString', coordinates: circleRing(f.geometry.coordinates, radius) },
      })
    }
  }
  return { type: 'FeatureCollection', features }
}

function point(c: LngLat, properties: Record<string, unknown>): OverlayFeature {
  return { type: 'Feature', properties, geometry: { type: 'Point', coordinates: c } }
}

function near(a: LngLat, b: LngLat): boolean {
  return Math.abs(a[0] - b[0]) <= CLOSE_TOLERANCE_DEG && Math.abs(a[1] - b[1]) <= CLOSE_TOLERANCE_DEG
}

// The point dist metres from centre on the given bearing (degrees clockwise from north), on a sphere.
function destination(centre: LngLat, distM: number, bearingDeg: number): LngLat {
  const rad = Math.PI / 180
  const delta = distM / EARTH_RADIUS_M
  const theta = bearingDeg * rad
  const lat1 = centre[1] * rad
  const lng1 = centre[0] * rad
  const lat2 = Math.asin(Math.sin(lat1) * Math.cos(delta) + Math.cos(lat1) * Math.sin(delta) * Math.cos(theta))
  const lng2 =
    lng1 + Math.atan2(Math.sin(theta) * Math.sin(delta) * Math.cos(lat1), Math.cos(delta) - Math.sin(lat1) * Math.sin(lat2))
  const lng = ((((lng2 / rad) + 540) % 360) + 360) % 360 - 180
  return [lng, lat2 / rad]
}
