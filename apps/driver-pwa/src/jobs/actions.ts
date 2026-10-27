import type { JobState } from '../api/types'

// One-tap driver actions (GF-229). deliver opens the photo page instead of calling
// a transition, so it has no POST of its own here.
export type DriverAction = 'accept' | 'start' | 'pickup' | 'deliver'

export interface DriverEdge {
  from: JobState
  to: JobState
  action: DriverAction | null
}

// Every D-23 driver edge (internal/jobs/transitions.go), with the one-tap action it
// shows. The fail edges are listed with a null action: fail is not exposed in P14.
export const DRIVER_EDGES: readonly DriverEdge[] = [
  { from: 'assigned', to: 'accepted', action: 'accept' },
  { from: 'accepted', to: 'en_route', action: 'start' },
  { from: 'en_route', to: 'picked_up', action: 'pickup' },
  { from: 'picked_up', to: 'delivered', action: 'deliver' },
  { from: 'accepted', to: 'failed', action: null },
  { from: 'en_route', to: 'failed', action: null },
  { from: 'picked_up', to: 'failed', action: null },
]

export const ACTION_LABELS: Record<DriverAction, string> = {
  accept: 'Accept',
  start: 'Start',
  pickup: 'Pick up',
  deliver: 'Deliver',
}

// The one-tap action for a job state, or null when the state offers none. Pure.
export function actionsFor(state: string): DriverAction | null {
  return DRIVER_EDGES.find((edge) => edge.from === state && edge.action !== null)?.action ?? null
}
