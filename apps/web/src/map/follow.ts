export interface Position {
  lat: number
  lng: number
}

// Returns the new camera center when the followed vehicle moved, otherwise null
// (GF-161). Zoom is never touched here.
export function followCenter(
  prev: Position | null | undefined,
  next: Position | null | undefined,
): [number, number] | null {
  if (!next) return null
  if (prev && prev.lat === next.lat && prev.lng === next.lng) return null
  return [next.lng, next.lat]
}

// Map and selection events that can change the follow target. originalEvent is
// set only for user gestures, which is how a drag is told apart from code (GF-161).
export type FollowEvent =
  | { type: 'dragstart' | 'zoomstart' | 'moveend'; originalEvent?: unknown }
  | { type: 'select'; vehicleId: number }
  | { type: 'deselect' }
  | { type: 'stop' }

export function nextFollowId(current: number | null, event: FollowEvent): number | null {
  switch (event.type) {
    case 'select':
      return event.vehicleId
    case 'deselect':
    case 'stop':
      return null
    case 'dragstart':
      // A user drag exits follow. Wheel zoom and programmatic moves do not.
      return event.originalEvent !== undefined ? null : current
    case 'zoomstart':
    case 'moveend':
      return current
  }
}
