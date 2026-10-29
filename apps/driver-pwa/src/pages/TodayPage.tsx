import { useState } from 'react'
import { Link, useLocation } from 'react-router'
import type { TransitionAction } from '../api/driver'
import type { DriverJob } from '../api/types'
import { ACTION_LABELS, actionsFor } from '../jobs/actions'
import { loadDeviceKey } from '../device/key'
import { useJobTransition, useTodayJobs } from '../jobs/queries'
import { ConflictList } from '../offline/ConflictList'
import { useOnline, useQueue } from '../offline/useQueue'
import { setOnShift, useOnShift } from '../position/shift'
import { useAuthState } from '../session/session'

const STATE_LABELS: Record<string, string> = {
  assigned: 'Assigned',
  accepted: 'Accepted',
  en_route: 'En route',
  picked_up: 'Picked up',
}

const FAILED_MESSAGE = 'Could not update the job. Try again.'
const QUEUED_DELIVERY = 'Delivery queued. It will send when the connection is back.'

export function TodayPage() {
  const jobs = useTodayJobs()
  const transition = useJobTransition()
  const onShift = useOnShift()
  const userId = useAuthState().session?.claims.sub ?? ''
  const queue = useQueue(userId)
  const hasKey = loadDeviceKey() !== null
  const arrival = useLocation().state as { queued?: boolean } | null
  const rows = jobs.data?.jobs
  const online = useOnline()
  const queuedJobIds = new Set(queue.rows.map((row) => row.jobId))
  const conflicts = queue.rows.filter((row) => row.status === 'conflict')
  const pendingCount = queue.rows.length - conflicts.length
  const [notice, setNotice] = useState<string | null>(null)

  function act(job: DriverJob, action: TransitionAction) {
    setNotice(null)
    transition.mutate({ job, action }, { onError: () => setNotice(FAILED_MESSAGE) })
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
        <p>
          {!online && <span>Offline</span>}
          {pendingCount > 0 && <span>{pendingCount} queued</span>}
          {queue.syncing && <span>Syncing…</span>}
        </p>
      </header>
      {!hasKey && <p role="status">Set up the device key first</p>}
      {arrival?.queued && <p role="status">{QUEUED_DELIVERY}</p>}
      {notice && <p role="alert">{notice}</p>}
      <ConflictList userId={userId} conflicts={conflicts} />
      {jobs.isPending && <p role="status">Loading jobs…</p>}
      {jobs.isError && <p role="alert">Could not load jobs.</p>}
      {jobs.data?.stale && <p role="status">Offline — as of {formatTime(jobs.data.fetchedAt)}</p>}
      {rows?.length === 0 && <p>No jobs for today.</p>}
      {rows && rows.length > 0 && (
        <ul>
          {rows.map((job) => (
            <li key={job.id}>
              <strong>Job {job.id}</strong>{' '}
              <span className={`state-chip state-${job.state}`}>{STATE_LABELS[job.state] ?? job.state}</span>
              {job.window_start && (
                <time dateTime={job.window_start}> {formatTime(job.window_start)}</time>
              )}{' '}
              <JobAction job={job} queued={queuedJobIds.has(job.id)} onAct={act} />
            </li>
          ))}
        </ul>
      )}
    </main>
  )
}

interface JobActionProps {
  job: DriverJob
  queued: boolean
  onAct: (job: DriverJob, action: TransitionAction) => void
}

// A job with a queued row holds its button until the queue clears it (GF-241).
function JobAction({ job, queued, onAct }: JobActionProps) {
  const action = actionsFor(job.state)
  if (action === null) return null
  if (action === 'deliver') {
    if (queued) {
      return (
        <>
          <button type="button" disabled>
            {ACTION_LABELS.deliver}
          </button>{' '}
          <span>Queued</span>
        </>
      )
    }
    return <Link to={`/jobs/${job.id}/deliver`}>{ACTION_LABELS.deliver}</Link>
  }
  return (
    <>
      <button type="button" disabled={queued} onClick={() => onAct(job, action)}>
        {ACTION_LABELS[action]}
      </button>
      {queued && <span> Queued</span>}
    </>
  )
}

// Accepts an ISO string (window_start) or epoch milliseconds (fetchedAt).
function formatTime(value: string | number): string {
  return new Date(value).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}
