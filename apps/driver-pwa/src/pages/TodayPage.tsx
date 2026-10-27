import { useState } from 'react'
import { Link } from 'react-router'
import type { TransitionAction } from '../api/driver'
import { ApiError } from '../api/client'
import type { DriverJob } from '../api/types'
import { ACTION_LABELS, actionsFor } from '../jobs/actions'
import { useJobTransition, useTodayJobs } from '../jobs/queries'

const STATE_LABELS: Record<string, string> = {
  assigned: 'Assigned',
  accepted: 'Accepted',
  en_route: 'En route',
  picked_up: 'Picked up',
}

const CHANGED_MESSAGE = 'Job changed; refreshed.'
const FAILED_MESSAGE = 'Could not update the job. Try again.'

export function TodayPage() {
  const jobs = useTodayJobs()
  const transition = useJobTransition()
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
        <Link to="/setup-key">Device key</Link>
      </header>
      {notice && <p role="alert">{notice}</p>}
      {jobs.isPending && <p role="status">Loading jobs…</p>}
      {jobs.isError && <p role="alert">Could not load jobs.</p>}
      {jobs.data?.length === 0 && <p>No jobs for today.</p>}
      {jobs.data && jobs.data.length > 0 && (
        <ul>
          {jobs.data.map((job) => (
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

function formatTime(iso: string): string {
  return new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}
