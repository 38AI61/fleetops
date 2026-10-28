import { getJson, sendJson } from '../api/client'

// One alert as GET /alerts and PATCH /alerts/:id/ack return it (GF-298).
export type Alert = {
  id: number
  fleet_id: number
  kind: string
  severity: 'warning' | 'critical'
  payload: Record<string, unknown>
  raised_at: string
  acked_by: number | null
  acked_at: string | null
}

// GET /alerts. unacked counts every unacked alert in the fleet, not just the 24 h list.
export type AlertPage = { alerts: Alert[]; unacked: number }

export function listAlerts(): Promise<AlertPage> {
  return getJson<AlertPage>('/alerts')
}

// The one-time ack (GF-295). A second ack answers 409 conflict.
export function ackAlert(id: number): Promise<Alert> {
  return sendJson<Alert>('PATCH', `/alerts/${id}/ack`, {})
}
