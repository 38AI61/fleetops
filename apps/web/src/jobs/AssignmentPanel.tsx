import { useState } from 'react'
import { ApiError } from '../api/client'
import type { NearestCandidate } from '../api/types'
import { formatDistance } from './format'
import { useAssignJob, useNearest } from './queries'

export interface AssignmentPanelProps {
  jobId: number
  onAssigned?: () => void
}

// Error copy for the assign route (GF-175, GF-221). Anything unmapped gets the generic line.
export function assignErrorMessage(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.code === 'vehicle_not_active') return 'Vehicle is no longer active'
    if (err.code === 'conflict') return 'Job changed; refresh'
  }
  return 'Could not assign the job'
}

// Container: the nearest query and the assign mutation (GF-216, GF-221).
// Rows come back in server order with no client re-sort or presence filter (GF-223).
export function AssignmentPanel({ jobId, onAssigned }: AssignmentPanelProps) {
  const nearest = useNearest(jobId, true)
  const assign = useAssignJob()
  const [selectedId, setSelectedId] = useState<number | null>(null)

  const rows = nearest.data?.candidates ?? []
  const chosen = rows.find((c) => c.vehicle_id === selectedId) ?? null

  return (
    <section aria-label="Assignment">
      <h2>Nearest vehicles</h2>
      {nearest.isPending && <p>Loading nearest vehicles</p>}
      {nearest.isError && <p role="alert">Could not load nearest vehicles</p>}
      {nearest.isSuccess && rows.length === 0 && <p>No eligible vehicles</p>}
      {rows.length > 0 && <CandidateTable rows={rows} selectedId={selectedId} onSelect={setSelectedId} />}

      <button
        type="button"
        disabled={!chosen || assign.isPending}
        onClick={() => {
          if (!chosen) return
          assign.mutate(
            { jobId, vehicleId: chosen.vehicle_id, driverId: chosen.driver_id },
            { onSuccess: () => onAssigned?.() },
          )
        }}
      >
        Assign
      </button>
      {assign.isError && <p role="alert">{assignErrorMessage(assign.error)}</p>}
    </section>
  )
}

export interface CandidateTableProps {
  rows: NearestCandidate[]
  selectedId: number | null
  onSelect: (vehicleId: number) => void
}

// Presentational: renders rows in the order given, with distance formatted and last seen from server_ts.
export function CandidateTable({ rows, selectedId, onSelect }: CandidateTableProps) {
  return (
    <table>
      <thead>
        <tr>
          <th scope="col">Select</th>
          <th scope="col">Plate</th>
          <th scope="col">Driver</th>
          <th scope="col">Distance</th>
          <th scope="col">Last seen</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((c) => (
          <tr key={c.vehicle_id}>
            <td>
              <input
                type="radio"
                name="candidate"
                aria-label={`Select ${c.plate}`}
                checked={selectedId === c.vehicle_id}
                onChange={() => onSelect(c.vehicle_id)}
              />
            </td>
            <td>{c.plate}</td>
            <td>{c.driver_name}</td>
            <td>{formatDistance(c.distance_m)}</td>
            <td>{new Date(c.server_ts).toLocaleTimeString()}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}
