import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ApiError } from '../api/client'
import { ackAlert, listAlerts } from './api'

// Polling interval for the bell (GF-299). The bell does not use the WS stream.
const POLL_MS = 30_000

// AlertBell is the topbar bell: an unacked badge and a dropdown of the last 24 h
// with an Ack button per alert. It hides its badge when the session is gone (401);
// the existing auth layer handles the logout.
export function AlertBell() {
  const qc = useQueryClient()
  const [open, setOpen] = useState(false)
  const [ackError, setAckError] = useState<string | null>(null)

  const page = useQuery({
    queryKey: ['alerts'],
    queryFn: listAlerts,
    refetchInterval: POLL_MS,
  })

  const ack = useMutation({
    mutationFn: (id: number) => ackAlert(id),
    onMutate: () => setAckError(null),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['alerts'] }),
    onError: (err) => {
      setAckError(
        err instanceof ApiError && err.status === 409
          ? 'This alert was already acknowledged.'
          : 'The alert could not be acknowledged. Try again.',
      )
      void qc.invalidateQueries({ queryKey: ['alerts'] })
    },
  })

  if (page.isError && page.error instanceof ApiError && page.error.status === 401) {
    return null
  }

  const unacked = page.data?.unacked ?? 0
  const alerts = page.data?.alerts ?? []

  return (
    <div className="alert-bell">
      <button
        type="button"
        aria-label="Alerts"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
      >
        Alerts
        {unacked > 0 ? (
          <span className="alert-badge" aria-label={`${unacked} unacked`}>
            {unacked}
          </span>
        ) : null}
      </button>
      {open ? (
        <div className="alert-dropdown" role="dialog" aria-label="Alerts from the last 24 hours">
          {ackError ? (
            <p role="alert" className="alert-error">
              {ackError}
            </p>
          ) : null}
          {alerts.length === 0 ? <p>No alerts in the last 24 hours.</p> : null}
          <ul>
            {alerts.map((a) => (
              <li key={a.id}>
                <span className="alert-kind">{a.kind}</span>{' '}
                <span className={`alert-severity alert-${a.severity}`}>{a.severity}</span>{' '}
                <time dateTime={a.raised_at}>{new Date(a.raised_at).toLocaleString()}</time>
                {a.acked_at === null ? (
                  <button type="button" onClick={() => ack.mutate(a.id)} disabled={ack.isPending}>
                    Ack
                  </button>
                ) : (
                  <span className="alert-acked">Acknowledged</span>
                )}
              </li>
            ))}
          </ul>
        </div>
      ) : null}
    </div>
  )
}
