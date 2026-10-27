import type { DriverJob } from '../api/types'
import { openQueueDb } from './db'

export interface Snapshot {
  payload: DriverJob[]
  fetchedAt: number
}

// The last successful today payload, saved per user (GF-239). Hand-rolled on purpose:
// TanStack persist would store the whole cache and cannot scope to a user.
export async function saveSnapshot(
  userId: string,
  payload: DriverJob[],
  fetchedAt = Date.now(),
): Promise<void> {
  const db = await openQueueDb()
  await db.put('snapshots', { userId, payload, fetchedAt })
}

export async function loadSnapshot(userId: string): Promise<Snapshot | null> {
  const db = await openQueueDb()
  const row = await db.get('snapshots', userId)
  return row ? { payload: row.payload, fetchedAt: row.fetchedAt } : null
}
