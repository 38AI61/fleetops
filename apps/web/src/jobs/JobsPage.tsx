import { useState } from 'react'
import { Link } from 'react-router'
import type { Job, JobState, JobsFilter } from '../api/types'
import { AssignmentPanel } from './AssignmentPanel'
import { useJobs } from './queries'
import { AlertBell } from '../alerts/AlertBell'

const STATES: JobState[] = ['created', 'assigned', 'accepted', 'en_route', 'picked_up', 'delivered', 'failed', 'cancelled']

// Only these states can take a new assignment from the dispatcher (GF-218).
const SELECTABLE: JobState[] = ['created', 'assigned']

function windowText(s: string | null): string {
  return s ? new Date(s).toLocaleString() : '—'
}

// Dispatcher jobs list with filters (GF-218). Selecting a created or assigned row opens the assignment panel.
export function JobsPage() {
  const [state, setState] = useState<JobState | ''>('')
  const [driver, setDriver] = useState('')
  const [day, setDay] = useState('')
  const [selectedId, setSelectedId] = useState<number | null>(null)

  const filter: JobsFilter = {
    state: state || undefined,
    driver: driver === '' || Number.isNaN(Number(driver)) ? undefined : Number(driver),
    day: day || undefined,
  }
  const jobs = useJobs(filter)

  return (
    <div className="jobs-page">
      <header className="topbar">
        <span className="title">Jobs</span>
        <Link to="/jobs/new">New job</Link>
        <Link to="/map">Map</Link>
        <Link to="/reports">Reports</Link>
        <AlertBell />
      </header>

      <form aria-label="Filters" onSubmit={(e) => e.preventDefault()}>
        <label>
          State
          <select value={state} onChange={(e) => setState(e.target.value as JobState | '')}>
            <option value="">All states</option>
            {STATES.map((s) => (
              <option key={s} value={s}>
                {s}
              </option>
            ))}
          </select>
        </label>
        <label>
          Driver id
          <input type="number" min={1} value={driver} onChange={(e) => setDriver(e.target.value)} />
        </label>
        <label>
          Day
          <input type="date" value={day} onChange={(e) => setDay(e.target.value)} />
        </label>
      </form>

      {jobs.isPending && <p>Loading jobs</p>}
      {jobs.isError && <p role="alert">Could not load jobs</p>}
      {jobs.isSuccess && jobs.data.length === 0 && <p>No jobs match these filters</p>}
      {jobs.isSuccess && jobs.data.length > 0 && (
        <table>
          <thead>
            <tr>
              <th scope="col">ID</th>
              <th scope="col">State</th>
              <th scope="col">Driver</th>
              <th scope="col">Vehicle</th>
              <th scope="col">Window start</th>
              <th scope="col">Action</th>
            </tr>
          </thead>
          <tbody>
            {jobs.data.map((j: Job) => (
              <tr key={j.id}>
                <td>{j.id}</td>
                <td className={`state-chip state-${j.state}`}>{j.state}</td>
                <td>{j.driver_id ?? '—'}</td>
                <td>{j.vehicle_id ?? '—'}</td>
                <td>{windowText(j.window_start)}</td>
                <td>
                  {SELECTABLE.includes(j.state) ? (
                    <button type="button" aria-label={`Select job ${j.id}`} onClick={() => setSelectedId(j.id)}>
                      Select
                    </button>
                  ) : (
                    '—'
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {selectedId !== null && <AssignmentPanel key={selectedId} jobId={selectedId} />}
    </div>
  )
}
