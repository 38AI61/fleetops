import { useState } from 'react'
import { Link } from 'react-router'
import type { TransitionAction } from '../api/driver'
import { ApiError } from '../api/client'
import type { DriverJob } from '../api/types'
import { ACTION_LABELS, actionsFor } from '../jobs/actions'
import { loadDeviceKey } from '../device/key'
import { useJobTransition, useTodayJobs } from '../jobs/queries'
import { setOnShift, useOnShift } from '../position/shift'

const STATE_LABELS: Record<string, string> = {
  assigned: 'Assigned',
  accepted: 'Accepted',
  en_route: 'En route',
  picked_up: 'Picked up',
}

const CHANGED_MESSAGE = 'Job changed; refreshed'
const FAILED_MESSAGE = 'Could not update the job. Try again.'

export function TodayPage() {
  const jobs = useTodayJobs()
  const transition = useJobTransition()
  const onShift = useOnShift()
  const hasKey = loadDeviceKey() !== null
  const rows = jobs.data?.jobs
  const [notice, setNotice] = useState<string | null>(null)

  function act(job: DriverJob, action: TransitionAction) {
    setNotice(null)
    transition.mutate(
      { id: job.id, action },
      {
        onError: (err) => {
          setNotice(err instanceof ApiError && err.status === 409 ? CHANGED_MESSAGE : FAILED_MESSAGE)
        },
      },
    )
  }

  return (
    <main>
      <header>
        <h1>Today</h1>
        <label>
          <input
            type="checkbox"
            checked={onShift}
            disabled={!hasKey}
            onChange={(event) => setOnShift(event.target.checked)}
          />{' '}
          On shift
        </label>
        <Link to="/setup-key">Device key</Link>
      </header>
      {!hasKey && <p role="status">Set up the device key first</p>}
      {notice && <p role="alert">{notice}</p>}
      {jobs.isPending && <p role="status">Loading jobs…</p>}
      {jobs.isError && <p role="alert">Could not load jobs.</p>}
      {jobs.data?.stale && <p role="status">Offline — as of {formatTime(jobs.data.fetchedAt)}</p>}
      {rows?.length === 0 && <p>No jobs for today.</p>}
      {rows && rows.length > 0 && (
        <ul>
          {rows.map((job) => (
            <li key={job.id}>
              <strong>Job {job.id}</strong>{' '}
              <span>{STATE_LABELS[job.state] ?? job.state}</span>
              {job.window_start && (
                <time dateTime={job.window_start}> {formatTime(job.window_start)}</time>
              )}{' '}
              <JobAction
                job={job}
                pending={transition.isPending && transition.variables?.id === job.id}
                onAct={act}
              />
            </li>
          ))}
        </ul>
      )}
    </main>
  )
}

interface JobActionProps {
  job: DriverJob
  pending: boolean
  onAct: (job: DriverJob, action: TransitionAction) => void
}

function JobAction({ job, pending, onAct }: JobActionProps) {
  const action = actionsFor(job.state)
  if (action === null) return null
  if (action === 'deliver') {
    return <Link to={`/jobs/${job.id}/deliver`}>{ACTION_LABELS.deliver}</Link>
  }
  return (
    <button type="button" disabled={pending} onClick={() => onAct(job, action)}>
      {ACTION_LABELS[action]}
    </button>
  )
}

// Accepts an ISO string (window_start) or epoch milliseconds (fetchedAt).
function formatTime(value: string | number): string {
  return new Date(value).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}
