import { beforeEach, describe, expect, it, vi } from 'vitest'
import { closeQueueDb, type ActionRow } from './db'
import { flushQueue, withQueueLock } from './flush'
import { enqueueTransition, listQueue, markInFlight, resetInFlight } from './queue'
import { resetQueueDb } from '../test/queue'

const USER = '7'

describe('queue database across a reload', () => {
  beforeEach(async () => {
    await resetQueueDb()
  })

  it('an in_flight row left by a reload resets to pending and replays once', async () => {
    const row = await enqueueTransition(USER, { id: 1 }, 'accept')
    await markInFlight(row.seq!)

    // The page reloads. The connection closes with it, and the server had already applied the move.
    await closeQueueDb()
    let serverEvents = 1
    const applied = new Set([1])
    const send = vi.fn(async (sent: ActionRow) => {
      if (!applied.has(sent.jobId)) {
        applied.add(sent.jobId)
        serverEvents++
      }
      return {}
    })

    // Boot: reopen the database, reset in_flight under the lock, then run one pass.
    expect((await listQueue(USER))[0].status).toBe('in_flight')
    await withQueueLock(() => resetInFlight(USER))
    expect((await listQueue(USER))[0].status).toBe('pending')
    await flushQueue(USER, send)

    expect(send).toHaveBeenCalledTimes(1)
    expect(serverEvents).toBe(1)
    expect(await listQueue(USER)).toHaveLength(0)
  })
})
