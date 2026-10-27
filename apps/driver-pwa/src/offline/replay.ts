import type { QueryClient } from '@tanstack/react-query'
import { deliverJob, transitionJob } from '../api/driver'
import { buildDeliverForm } from '../jobs/deliver'
import { TODAY_QUERY_KEY } from '../jobs/keys'
import type { ActionRow } from './db'
import { flushQueue, withQueueLock } from './flush'
import { listQueue, resetInFlight, subscribeQueue } from './queue'

// While the queue is non-empty, a pass runs this often (GF-238).
export const REPLAY_INTERVAL_MS = 30_000

// Sends one queued row through the same P11 and P12 calls the online path uses.
export async function sendRow(row: ActionRow): Promise<unknown> {
  if (row.action !== 'deliver') return transitionJob(row.jobId, row.action)
  if (!row.photo) throw new Error('deliver row has no photo')
  const photo = new File([row.photo.bytes], row.photo.name, { type: row.photo.type })
  return deliverJob(row.jobId, buildDeliverForm(photo, row.photo.lat, row.photo.lng))
}

// One pass under the lock. The today list refetches after it, so the driver sees server
// state (GF-240).
export async function runReplayPass(userId: string, client: QueryClient): Promise<void> {
  await flushQueue(userId, sendRow)
  await client.invalidateQueries({ queryKey: TODAY_QUERY_KEY })
}

// Starts the replay triggers: boot (after resetInFlight), online, visibilitychange to
// visible, and a timer that runs only while the queue is non-empty (GF-238). Returns the
// teardown.
export function startReplay(userId: string, client: QueryClient): () => void {
  let active = true
  let timer: ReturnType<typeof setInterval> | null = null

  const pass = () => {
    void runReplayPass(userId, client).catch(() => undefined)
  }
  const onVisible = () => {
    if (document.visibilityState === 'visible') pass()
  }

  async function syncTimer(): Promise<void> {
    const pending = (await listQueue(userId)).length > 0
    if (!active) return
    if (pending && timer === null) timer = setInterval(pass, REPLAY_INTERVAL_MS)
    if (!pending && timer !== null) {
      clearInterval(timer)
      timer = null
    }
  }

  window.addEventListener('online', pass)
  document.addEventListener('visibilitychange', onVisible)
  const unsubscribe = subscribeQueue(() => void syncTimer().catch(() => undefined))

  // A pass cut off by a reload left rows in_flight. They go back to pending first (GF-237).
  void withQueueLock(() => resetInFlight(userId)).then(pass, () => undefined)

  return () => {
    active = false
    window.removeEventListener('online', pass)
    document.removeEventListener('visibilitychange', onVisible)
    unsubscribe()
    if (timer !== null) clearInterval(timer)
    timer = null
  }
}
