import type { TransitionAction } from '../api/driver'
import type { DriverJob, JobState } from '../api/types'
import { openQueueDb, type ActionRow, type QueueAction } from './db'

// The state each queued action moves a job to. It mirrors the D-23 driver edges in
// internal/jobs/transitions.go (GF-236). A test checks it against DRIVER_EDGES.
export const TARGET_STATE: Record<QueueAction, JobState> = {
  accept: 'accepted',
  start: 'en_route',
  pickup: 'picked_up',
  deliver: 'delivered',
}

// Listeners run after every queue write so the UI re-reads the rows.
const listeners = new Set<() => void>()

export function subscribeQueue(listener: () => void): () => void {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

export function notifyQueueChanged(): void {
  for (const listener of listeners) listener()
}

export function queueKey(jobId: number, action: QueueAction): string {
  return `${jobId}:${action}`
}

type Draft = Omit<ActionRow, 'seq'>

function draft(userId: string, job: Pick<DriverJob, 'id'>, action: QueueAction): Draft {
  return {
    userId,
    jobId: job.id,
    action,
    key: queueKey(job.id, action),
    status: 'pending',
    enqueuedAt: Date.now(),
    attempts: 0,
  }
}

// One readwrite transaction: an existing row with the same key (any status) wins, so a
// double tap writes one row (GF-237). The match is scoped to the user (GF-246): another
// driver's row on a shared device never absorbs this one.
async function insertRow(row: Draft): Promise<ActionRow> {
  const db = await openQueueDb()
  const tx = db.transaction('actions', 'readwrite')
  const sameKey = await tx.store.index('by_key').getAll(row.key)
  const existing = sameKey.find((r) => r.userId === row.userId)
  if (existing) {
    await tx.done
    return existing
  }
  const seq = await tx.store.add(row)
  await tx.done
  notifyQueueChanged()
  return { ...row, seq }
}

export function enqueueTransition(
  userId: string,
  job: Pick<DriverJob, 'id'>,
  action: TransitionAction,
): Promise<ActionRow> {
  return insertRow(draft(userId, job, action))
}

// Stores the photo bytes before the write, so the tap is captured before any network call.
export async function enqueueDeliver(
  userId: string,
  job: Pick<DriverJob, 'id'>,
  photo: File,
  lat: number,
  lng: number,
): Promise<ActionRow> {
  const bytes = await photo.arrayBuffer()
  return insertRow({
    ...draft(userId, job, 'deliver'),
    photo: { bytes, type: photo.type, name: photo.name, lat, lng },
  })
}

// Rows for one user in seq order (the by_user index orders by primary key within a user).
export async function listQueue(userId: string): Promise<ActionRow[]> {
  const db = await openQueueDb()
  return db.getAllFromIndex('actions', 'by_user', userId)
}

// The first pending row whose job has no earlier row still in the queue. Jobs do not
// block each other (GF-238).
export async function nextRunnable(userId: string): Promise<ActionRow | null> {
  const blocked = new Set<number>()
  for (const row of await listQueue(userId)) {
    if (row.status === 'pending' && !blocked.has(row.jobId)) return row
    blocked.add(row.jobId)
  }
  return null
}

async function updateRow(seq: number, change: (row: ActionRow) => ActionRow): Promise<void> {
  const db = await openQueueDb()
  const tx = db.transaction('actions', 'readwrite')
  const row = await tx.store.get(seq)
  // A row discarded while a send was in flight is gone; there is nothing to update.
  if (row) await tx.store.put(change(row))
  await tx.done
  notifyQueueChanged()
}

export async function markInFlight(seq: number): Promise<void> {
  await updateRow(seq, (row) => ({ ...row, status: 'in_flight', attempts: row.attempts + 1 }))
}

// Back to pending without counting an attempt: used when a pass stops before the server answers.
export async function markPending(seq: number): Promise<void> {
  await updateRow(seq, (row) => ({ ...row, status: 'pending' }))
}

export async function markConflict(
  seq: number,
  conflict: { code: string; message: string },
): Promise<void> {
  await updateRow(seq, (row) => ({ ...row, status: 'conflict', conflict }))
}

// Retry (GF-240): the conflict goes back to pending and the next pass sends it again.
export async function retry(seq: number): Promise<void> {
  await updateRow(seq, (row) => ({ ...row, status: 'pending', conflict: undefined }))
}

export async function markDone(seq: number): Promise<void> {
  const db = await openQueueDb()
  await db.delete('actions', seq)
  notifyQueueChanged()
}

// Discard (GF-240): deletes every row for this user's job, whatever its status.
export async function discardJob(userId: string, jobId: number): Promise<void> {
  const db = await openQueueDb()
  const tx = db.transaction('actions', 'readwrite')
  for (const row of await tx.store.index('by_job').getAll(jobId)) {
    if (row.userId === userId && row.seq !== undefined) await tx.store.delete(row.seq)
  }
  await tx.done
  notifyQueueChanged()
}

// Boot (GF-238): a pass that was cut off by a reload left rows in_flight. They go back
// to pending. The server treats a same-state replay as 200, so this is safe (GF-237).
export async function resetInFlight(userId: string): Promise<void> {
  const db = await openQueueDb()
  const tx = db.transaction('actions', 'readwrite')
  for (const row of await tx.store.index('by_user').getAll(userId)) {
    if (row.status === 'in_flight') await tx.store.put({ ...row, status: 'pending' })
  }
  await tx.done
  notifyQueueChanged()
}
