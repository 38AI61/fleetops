import type { PositionBody } from './types'

// POST /ingest/position authenticates with the device key only (GF-232). No JWT
// is sent, so this does not go through apiFetch. The caller reads the status.
export function postPosition(key: string, body: PositionBody): Promise<Response> {
  return fetch('/ingest/position', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Device-Key': key },
    body: JSON.stringify(body),
  })
}
