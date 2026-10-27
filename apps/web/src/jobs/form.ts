import type { JobCreateBody, LatLng } from '../api/types'

export const SIZE_CLASSES = ['small', 'medium', 'large'] as const
export type SizeClass = (typeof SIZE_CLASSES)[number]

export const MAX_NOTES_RUNES = 1000

// Raw form values. Times are datetime-local strings ('' when unset). Numbers stay as typed text.
export interface JobFormValues {
  pickup: LatLng | null
  dropoff: LatLng | null
  windowStart: string
  windowEnd: string
  notes: string
  sizeClass: SizeClass | ''
  weightKg: string
}

export type JobFormField = 'pickup' | 'dropoff' | 'windowStart' | 'windowEnd' | 'notes' | 'sizeClass' | 'weightKg'
export type JobFormErrors = Partial<Record<JobFormField, string>>

// Client-side feedback only. The server stays authoritative (GF-174, GF-178, GF-220).
export function validateJobForm(v: JobFormValues): JobFormErrors {
  const errors: JobFormErrors = {}
  if (!v.pickup) errors.pickup = 'Pick a pickup point on the map'
  if (!v.dropoff) errors.dropoff = 'Pick a dropoff point on the map'
  if (v.windowStart && v.windowEnd && new Date(v.windowStart) > new Date(v.windowEnd)) {
    errors.windowEnd = 'Window end must not be before window start'
  }
  if (Array.from(v.notes).length > MAX_NOTES_RUNES) {
    errors.notes = `Notes must be ${MAX_NOTES_RUNES} characters or fewer`
  }
  if (v.sizeClass !== '' && !(SIZE_CLASSES as readonly string[]).includes(v.sizeClass)) {
    errors.sizeClass = 'Size must be small, medium or large'
  }
  if (v.weightKg !== '') {
    const w = Number(v.weightKg)
    if (!Number.isFinite(w) || w <= 0) errors.weightKg = 'Weight must be greater than 0'
  }
  return errors
}

// POST /jobs body. Local times become RFC3339 UTC. Notes are sent as typed. Unset optional fields are omitted.
export function toCreateBody(v: JobFormValues): JobCreateBody {
  if (!v.pickup || !v.dropoff) throw new Error('pickup and dropoff are required')
  const body: JobCreateBody = { pickup: v.pickup, dropoff: v.dropoff }
  if (v.windowStart) body.window_start = new Date(v.windowStart).toISOString()
  if (v.windowEnd) body.window_end = new Date(v.windowEnd).toISOString()
  if (v.notes !== '') body.notes = v.notes
  if (v.sizeClass !== '') body.size_class = v.sizeClass
  if (v.weightKg !== '') body.weight_kg = Number(v.weightKg)
  return body
}
