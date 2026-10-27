import { useMemo, useReducer } from 'react'
import { Link, useNavigate } from 'react-router'
import { toFeatureCollection } from '../status/display'
import { MapView } from '../map/MapView'
import { JobCreateForm } from './JobCreateForm'
import { clearDropoff, clearPickup, emptyPick, pickReducer, type PickState } from './pick'
import { useCreateJob } from './queries'
import type { LatLng } from '../api/types'

// Pick sequence as an action: a click adds a point, clears remove one (GF-219).
type PickAction = { type: 'click'; point: LatLng } | { type: 'clearPickup' } | { type: 'clearDropoff' }

function reducePick(state: PickState, action: PickAction): PickState {
  switch (action.type) {
    case 'click':
      return pickReducer(state, action.point)
    case 'clearPickup':
      return clearPickup(state)
    case 'clearDropoff':
      return clearDropoff(state)
  }
}

// Create a job with the pickup and dropoff picked on the map (GF-218, GF-220).
export function NewJobPage() {
  const navigate = useNavigate()
  const [picks, dispatch] = useReducer(reducePick, emptyPick)
  const create = useCreateJob()
  // The map shows only the picks here, so the vehicle layer stays empty.
  const emptyVehicles = useMemo(() => toFeatureCollection([], 0), [])

  return (
    <div className="new-job-page">
      <header className="topbar">
        <span className="title">New job</span>
        <Link to="/jobs">Back to jobs</Link>
      </header>
      <div className="new-job">
        <div className="new-job-map">
          <MapView
            collection={emptyVehicles}
            onBoundsChange={() => undefined}
            onSelect={() => undefined}
            onDeselect={() => undefined}
            onInteraction={() => undefined}
            followTarget={null}
            onMapClick={(point) => dispatch({ type: 'click', point })}
            marks={picks}
          />
        </div>
        <div className="new-job-form">
          <JobCreateForm
            pickup={picks.pickup}
            dropoff={picks.dropoff}
            onClearPickup={() => dispatch({ type: 'clearPickup' })}
            onClearDropoff={() => dispatch({ type: 'clearDropoff' })}
            pending={create.isPending}
            onSubmit={(body) => create.mutate(body, { onSuccess: () => navigate('/jobs') })}
          />
          {create.isError && <p role="alert">Could not create the job</p>}
        </div>
      </div>
    </div>
  )
}
