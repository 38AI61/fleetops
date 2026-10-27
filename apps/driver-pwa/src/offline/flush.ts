import { ApiError } from '../api/client'
import {
  markConflict,
  markDone,
  markInFlight,
  markPending,
  nextRunnable,
  notifyQueueChanged,
} from './queue'
import type { ActionRow } from './db'

export type SendRow = (row: ActionRow) => Promise<unknown>

export interface FlushResult {
  sent: number
  conflicts: number
}

// The driver-facing message for each server answer that keeps a row as a conflict (GF-240).
const CONFLICT_MESSAGES: Record<number, string> = {
  400: 'Proof was rejected.',
  404: 'Job no longer available.',
  409: 'Job changed on the server; not applied.',
  413: 'Proof was rejected.',
}

// The lock name is shared by every tab on the origin when navigator.locks exists (GF-238).
const LOCK_NAME = 'fleetops-driver-queue'
let tail: Promise<unknown> = Promise.resolve()
let syncing = false

// navigator.locks where it exists, else an in-memory mutex. Either way one pass runs at a time.
export async function withQueueLock<T>(task: () => Promise<T>): Promise<T> {
  if (navigator.locks) return navigator.locks.request(LOCK_NAME, task)
  const run = tail.then(task, task)
  tail = run.catch(() => undefined)
  return run
}

// True while a pass is running. The header shows "Syncing…" from this.
export function isSyncing(): boolean {
  return syncing
}

// The conflict to keep on the row, or null when the pass must stop and keep the row pending.
function conflictOf(err: unknown): { code: string; message: string } | null {
  if (!(err instanceof ApiError)) return null
  const message = CONFLICT_MESSAGES[err.status]
  return message === undefined ? null : { code: err.code, message }
}

// Sends the queue in order. 2xx deletes the row; a conflict keeps it and the job's later
// rows wait. Anything else (503, 401, network failure) keeps the row pending and ends the
// pass, so the next trigger retries (GF-238, GF-240).
export function flushQueue(userId: string, send: SendRow): Promise<FlushResult> {
  return withQueueLock(async () => {
    const result: FlushResult = { sent: 0, conflicts: 0 }
    syncing = true
    notifyQueueChanged()
    try {
      for (;;) {
        const row = await nextRunnable(userId)
        if (!row || row.seq === undefined) return result
        await markInFlight(row.seq)
        try {
          await send(row)
        } catch (err) {
          const conflict = conflictOf(err)
          if (conflict) {
            await markConflict(row.seq, conflict)
            result.conflicts++
            continue
          }
          await markPending(row.seq)
          return result
        }
        await markDone(row.seq)
        result.sent++
      }
    } finally {
      syncing = false
      notifyQueueChanged()
    }
  })
}
