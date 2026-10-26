import { useEffect } from 'react'
import type { DisplayStatus, VehicleRow } from '../api/types'
import { isGrayed } from '../status/display'

export interface VehicleCardProps {
  snapshot: VehicleRow
  nowMs: number
  onClose: () => void
  // False when the vehicle has left the current data set (GF-159).
  inView?: boolean
  following?: boolean
  onStopFollow?: () => void
}

const STATUS_TEXT: Record<DisplayStatus, string> = {
  idle: 'Idle',
  en_route: 'En route',
  offline: 'Offline',
}

function formatTime(ts: string): string {
  const d = new Date(ts)
  return Number.isNaN(d.getTime()) ? ts : d.toLocaleString()
}

// Seconds below a minute, otherwise minutes and seconds (GF-159).
export function formatAge(ageMs: number): string {
  const s = Math.max(0, Math.floor(ageMs / 1000))
  return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m ${s % 60}s`
}

// Shows exactly the GF-130 fields, plus the derived age and stale badge (GF-159).
export function VehicleCard({
  snapshot,
  nowMs,
  onClose,
  inView = true,
  following = false,
  onStopFollow,
}: VehicleCardProps) {
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [onClose])

  const grayed = isGrayed(snapshot, nowMs)
  const age = nowMs - Date.parse(snapshot.server_ts)

  return (
    <aside className="vehicle-card" aria-label="Vehicle details">
      <div className="card-head">
        <h2>Vehicle details</h2>
        <button type="button" onClick={onClose} aria-label="Close">
          Close
        </button>
      </div>
      {grayed ? <span className="stale-badge">Stale</span> : null}
      {inView ? null : <p className="note">Not in current view</p>}
      <dl>
        <dt>Vehicle</dt>
        <dd>{snapshot.vehicle_id}</dd>
        <dt>Latitude</dt>
        <dd>{snapshot.lat.toFixed(6)}</dd>
        <dt>Longitude</dt>
        <dd>{snapshot.lng.toFixed(6)}</dd>
        <dt>Speed</dt>
        <dd>{snapshot.speed_kmh === null ? '—' : `${snapshot.speed_kmh} km/h`}</dd>
        <dt>Heading</dt>
        <dd>{snapshot.heading_deg === null ? '—' : `${snapshot.heading_deg}°`}</dd>
        <dt>Status</dt>
        <dd>{STATUS_TEXT[snapshot.status]}</dd>
        <dt>Device time</dt>
        <dd>{formatTime(snapshot.device_ts)}</dd>
        <dt>Server time</dt>
        <dd>{formatTime(snapshot.server_ts)}</dd>
        <dt>Age</dt>
        <dd>{formatAge(age)}</dd>
      </dl>
      {following && onStopFollow ? (
        <button type="button" onClick={onStopFollow}>
          Stop following
        </button>
      ) : null}
    </aside>
  )
}
