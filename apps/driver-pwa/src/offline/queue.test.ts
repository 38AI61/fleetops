import { beforeEach, describe, expect, it } from 'vitest'
import { DRIVER_EDGES } from '../jobs/actions'
import { resetQueueDb } from '../test/queue'
import { openQueueDb, type ActionRow } from './db'
import {
  discardJob,
  enqueueDeliver,
  enqueueTransition,
  listQueue,
  markConflict,
  markInFlight,
  nextRunnable,
  resetInFlight,
  retry,
  TARGET_STATE,
} from './queue'

const ME = '7'
const OTHER = '9'

// Helper: the id of a stored row, asserting it exists.
function seqOf(row: ActionRow): number {
  if (row.seq === undefined) throw new Error('row has no seq')
  return row.seq
}

describe('queue store', () => {
  beforeEach(async () => {
    await resetQueueDb()
  })

  it('enqueueTransition writes one pending row with a seq and key', async () => {
    const row = await enqueueTransition(ME, { id: 4 }, 'accept')

    expect(row).toMatchObject({ userId: ME, jobId: 4, action: 'accept', key: '4:accept', status: 'pending', attempts: 0 })
    expect(row.seq).toBeTypeOf('number')
    expect(await listQueue(ME)).toHaveLength(1)
  })

  it('a repeated enqueue of the same job and action writes one row (dedupe)', async () => {
    const first = await enqueueTransition(ME, { id: 4 }, 'start')
    const second = await enqueueTransition(ME, { id: 4 }, 'start')

    expect(second.seq).toBe(first.seq)
    expect(await listQueue(ME)).toHaveLength(1)
  })

  it('enqueueDeliver dedupes on the :deliver key', async () => {
    const photo = new File(['proof'], 'proof.jpg', { type: 'image/jpeg' })
    await enqueueDeliver(ME, { id: 4 }, photo, 41, 29)
    await enqueueDeliver(ME, { id: 4 }, photo, 41, 29)

    const rows = await listQueue(ME)
    expect(rows).toHaveLength(1)
    expect(rows[0].key).toBe('4:deliver')
  })

  it('enqueueDeliver stores the photo bytes, type, name and coordinates', async () => {
    const photo = new File(['proof-bytes'], 'proof.png', { type: 'image/png' })
    const row = await enqueueDeliver(ME, { id: 4 }, photo, 41.0082, 28.9784)

    const stored = (await listQueue(ME))[0]
    expect(stored.photo?.type).toBe('image/png')
    expect(stored.photo?.name).toBe('proof.png')
    expect(stored.photo?.lat).toBe(41.0082)
    expect(stored.photo?.lng).toBe(28.9784)
    expect(stored.photo?.bytes.byteLength).toBe('proof-bytes'.length)
    expect(row.seq).toBe(stored.seq)
  })

  it('a File rebuilt from the stored bytes has the same bytes, type and name', async () => {
    const photo = new File(['proof-bytes'], 'proof.webp', { type: 'image/webp' })
    await enqueueDeliver(ME, { id: 4 }, photo, 41, 29)

    const stored = (await listQueue(ME))[0].photo!
    const rebuilt = new File([stored.bytes], stored.name, { type: stored.type })

    expect(rebuilt.name).toBe('proof.webp')
    expect(rebuilt.type).toBe('image/webp')
    expect(new Uint8Array(await rebuilt.arrayBuffer())).toEqual(new Uint8Array(await photo.arrayBuffer()))
  })

  it('listQueue isolates rows per user (by_user)', async () => {
    await enqueueTransition(ME, { id: 1 }, 'accept')
    await enqueueTransition(OTHER, { id: 2 }, 'accept')

    expect((await listQueue(ME)).map((r) => r.jobId)).toEqual([1])
    expect((await listQueue(OTHER)).map((r) => r.jobId)).toEqual([2])
  })

  it('nextRunnable returns rows in seq order across jobs, and blocks only the same job', async () => {
    const a1 = await enqueueTransition(ME, { id: 1 }, 'accept')
    const b1 = await enqueueTransition(ME, { id: 2 }, 'accept')
    await enqueueTransition(ME, { id: 1 }, 'start')

    expect(await nextRunnable(ME)).toMatchObject({ seq: seqOf(a1) })

    await markInFlight(seqOf(a1))
    // Job 1 is held by its in-flight row. Job 2 is not blocked.
    expect(await nextRunnable(ME)).toMatchObject({ seq: seqOf(b1) })
  })

  it('a conflict blocks later rows of its job and no others', async () => {
    const a1 = await enqueueTransition(ME, { id: 1 }, 'accept')
    await enqueueTransition(ME, { id: 1 }, 'start')
    const b1 = await enqueueTransition(ME, { id: 2 }, 'accept')

    await markConflict(seqOf(a1), { code: 'conflict', message: 'x' })

    expect(await nextRunnable(ME)).toMatchObject({ seq: seqOf(b1) })
  })

  it('resetInFlight moves only in_flight rows back to pending', async () => {
    const a1 = await enqueueTransition(ME, { id: 1 }, 'accept')
    const b1 = await enqueueTransition(ME, { id: 2 }, 'accept')
    const c1 = await enqueueTransition(ME, { id: 3 }, 'accept')
    await markInFlight(seqOf(a1))
    await markConflict(seqOf(b1), { code: 'conflict', message: 'x' })
    await markConflict(seqOf(c1), { code: 'conflict', message: 'y' })
    await retry(seqOf(c1))

    await resetInFlight(ME)

    const statuses = (await listQueue(ME)).map((r) => [r.jobId, r.status])
    expect(statuses).toEqual([
      [1, 'pending'],
      [2, 'conflict'],
      [3, 'pending'],
    ])
  })

  it('retry turns a conflict back to pending and clears its message', async () => {
    const row = await enqueueTransition(ME, { id: 1 }, 'accept')
    await markConflict(seqOf(row), { code: 'conflict', message: 'x' })

    await retry(seqOf(row))

    const [stored] = await listQueue(ME)
    expect(stored.status).toBe('pending')
    expect(stored.conflict).toBeUndefined()
  })

  it('discardJob removes every row for that job of this user, and no other rows', async () => {
    await enqueueTransition(ME, { id: 1 }, 'accept')
    await enqueueTransition(ME, { id: 1 }, 'start')
    await enqueueTransition(ME, { id: 2 }, 'accept')
    await enqueueTransition(OTHER, { id: 1 }, 'accept')

    await discardJob(ME, 1)

    expect((await listQueue(ME)).map((r) => [r.jobId, r.action])).toEqual([[2, 'accept']])
    expect(await listQueue(OTHER)).toHaveLength(1)
  })

  it('TARGET_STATE matches the driver edge for each action', () => {
    for (const [action, target] of Object.entries(TARGET_STATE)) {
      const edge = DRIVER_EDGES.find((e) => e.action === action)
      expect(edge?.to).toBe(target)
    }
  })

  it('the queue store exposes the two object stores of version 1', async () => {
    const db = await openQueueDb()
    expect([...db.objectStoreNames].sort()).toEqual(['actions', 'snapshots'])
    expect(db.version).toBe(1)
  })
})
