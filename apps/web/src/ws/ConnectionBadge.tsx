import { useWsStatus, type WsStatus } from './status'

const LABEL: Record<WsStatus, string> = {
  live: 'Live',
  reconnecting: 'Reconnecting',
  offline: 'Offline',
}

// Text only, no color cue (GF-162).
export function ConnectionBadge() {
  const status = useWsStatus()
  return (
    <span className="ws-badge" data-status={status}>
      {LABEL[status]}
    </span>
  )
}
