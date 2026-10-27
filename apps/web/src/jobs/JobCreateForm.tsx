import { useState } from 'react'
import type { JobCreateBody, LatLng } from '../api/types'
import { MAX_NOTES_RUNES, SIZE_CLASSES, toCreateBody, validateJobForm, type JobFormValues, type SizeClass } from './form'

export interface JobCreateFormProps {
  pickup: LatLng | null
  dropoff: LatLng | null
  onClearPickup: () => void
  onClearDropoff: () => void
  onSubmit: (body: JobCreateBody) => void
  pending: boolean
}

function fmtPoint(p: LatLng): string {
  return `${p.lat.toFixed(5)}, ${p.lng.toFixed(5)}`
}

// The create form (GF-220). Pickup and dropoff come from the map; the rest is typed here.
// Client errors show inline for feedback only. The server still validates.
export function JobCreateForm(props: JobCreateFormProps) {
  const [windowStart, setWindowStart] = useState('')
  const [windowEnd, setWindowEnd] = useState('')
  const [notes, setNotes] = useState('')
  const [sizeClass, setSizeClass] = useState<SizeClass | ''>('')
  const [weightKg, setWeightKg] = useState('')

  const values: JobFormValues = {
    pickup: props.pickup,
    dropoff: props.dropoff,
    windowStart,
    windowEnd,
    notes,
    sizeClass,
    weightKg,
  }
  const errors = validateJobForm(values)
  const invalid = Object.keys(errors).length > 0

  return (
    <form
      aria-label="New job"
      onSubmit={(e) => {
        e.preventDefault()
        if (invalid || props.pending) return
        props.onSubmit(toCreateBody(values))
      }}
    >
      <section>
        <p>
          Pickup: {props.pickup ? fmtPoint(props.pickup) : 'not set'}{' '}
          <button type="button" onClick={props.onClearPickup} disabled={!props.pickup}>
            Clear pickup
          </button>
        </p>
        {errors.pickup && <p role="alert">{errors.pickup}</p>}
        <p>
          Dropoff: {props.dropoff ? fmtPoint(props.dropoff) : 'not set'}{' '}
          <button type="button" onClick={props.onClearDropoff} disabled={!props.dropoff}>
            Clear dropoff
          </button>
        </p>
        {errors.dropoff && <p role="alert">{errors.dropoff}</p>}
      </section>

      <label>
        Window start
        <input type="datetime-local" value={windowStart} onChange={(e) => setWindowStart(e.target.value)} />
      </label>
      <label>
        Window end
        <input type="datetime-local" value={windowEnd} onChange={(e) => setWindowEnd(e.target.value)} />
      </label>
      {errors.windowEnd && <p role="alert">{errors.windowEnd}</p>}

      <label>
        Notes
        <textarea value={notes} onChange={(e) => setNotes(e.target.value)} />
      </label>
      <p>
        {Array.from(notes).length} / {MAX_NOTES_RUNES}
      </p>
      {errors.notes && <p role="alert">{errors.notes}</p>}

      <label>
        Size
        <select value={sizeClass} onChange={(e) => setSizeClass(e.target.value as SizeClass | '')}>
          <option value="">No size</option>
          {SIZE_CLASSES.map((s) => (
            <option key={s} value={s}>
              {s}
            </option>
          ))}
        </select>
      </label>
      {errors.sizeClass && <p role="alert">{errors.sizeClass}</p>}

      <label>
        Weight (kg)
        <input type="number" step="any" value={weightKg} onChange={(e) => setWeightKg(e.target.value)} />
      </label>
      {errors.weightKg && <p role="alert">{errors.weightKg}</p>}

      <button type="submit" disabled={props.pending || invalid}>
        Create job
      </button>
    </form>
  )
}
