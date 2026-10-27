import type { LatLng } from '../api/types'

// Pick state for the create form (GF-219). Null means not picked yet.
export interface PickState {
  pickup: LatLng | null
  dropoff: LatLng | null
}

export const emptyPick: PickState = { pickup: null, dropoff: null }

// The map marks the create form draws. PickState fits this shape.
export interface PickMarks {
  pickup?: LatLng | null
  dropoff?: LatLng | null
}

// A click sets the pickup first and the dropoff second. Later clicks replace the dropoff (GF-219).
export function pickReducer(state: PickState, click: LatLng): PickState {
  if (state.pickup === null) return { ...state, pickup: click }
  return { ...state, dropoff: click }
}

// Re-picking the pickup is an explicit clear, never a side effect of a click (GF-219).
export function clearPickup(state: PickState): PickState {
  return { ...state, pickup: null }
}

export function clearDropoff(state: PickState): PickState {
  return { ...state, dropoff: null }
}

// GeoJSON for the 'picks' source. kind drives the layer color. Typed locally so this module stays map-free.
export interface MarksCollection {
  type: 'FeatureCollection'
  features: Array<{
    type: 'Feature'
    properties: { kind: 'pickup' | 'dropoff' }
    geometry: { type: 'Point'; coordinates: [number, number] }
  }>
}

export function marksCollection(marks: PickMarks): MarksCollection {
  const features: MarksCollection['features'] = []
  for (const kind of ['pickup', 'dropoff'] as const) {
    const p = marks[kind]
    if (p) features.push({ type: 'Feature', properties: { kind }, geometry: { type: 'Point', coordinates: [p.lng, p.lat] } })
  }
  return { type: 'FeatureCollection', features }
}
