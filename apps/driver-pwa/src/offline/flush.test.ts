import { beforeEach, describe, expect, it } from 'vitest'
import { ApiError } from '../api/client'
import type { JobState } from '../api/types'
import { DRIVER_EDGES } from '../jobs/actions'
import { resetQueueDb } from '../test/queue'
import type { ActionRow } from './db'
import { flushQueue, isSyncing } from './flush'
import {
  discardJob,
  enqueueTransition,
  listQueue,
  markInFlight,
  resetInFlight,
  retry,
  TARGET_STATE,
} from './queue'

const ME = '7'

// A fake server that counts an event only when a job's state changes (D-23 edges).
// A same-state replay answers 200 with no event, as P11 and P12 do.
class FakeServer {
  states = new Map<number, JobState>()
  events = 0
  calls: string[] = []
  failNext: unknown = null

  constructor(initial: Record<number, JobState>) {
    for (const [id, state] of Object.entries(initial)) this.states.set(Number(id), state)
  }

  send = async (row: ActionRow): Promise<unknown> => {
    this.calls.push(`${row.action} ${row.jobId}`)
    if (this.failNext) {
      const err = this.failNext
      this.failNext = null
      throw err
    }
    const state = this.states.get(row.jobId)
    const target = TARGET_STATE[row.action]
    if (state === target) return {}
    const from = DRIVER_EDGES.find((edge) => edge.action === row.action)?.from
    if (state !== undefined && state === from) {
      this.states.set(row.jobId, target)
      this.events++
      return {}
    }
    throw new ApiError(409, 'conflict')
  }
}

describe('flushQueue', () => {
  let server: FakeServer

  beforeEach(async () => {
    await resetQueueDb()
    server = new FakeServer({ 1: 'assigned', 2: 'assigned' })
  })

  it('FIFO across jobs', async () => {
    await enqueueTransition(ME, { id: 1 }, 'accept')
    await enqueueTransition(ME, { id: 2 }, 'accept')
    await enqueueTransition(ME, { id: 1 }, 'start')

    const result = await flushQueue(ME, server.send)

    expect(server.calls).toEqual(['accept 1', 'accept 2', 'start 1'])
    expect(result).toEqual({ sent: 3, conflicts: 0 })
    expect(server.states.get(1)).toBe('en_route')
    expect(await listQueue(ME)).toHaveLength(0)
  })

  it('a blocked job does not stall the others', async () => {
    server.states.set(1, 'created')
    await enqueueTransition(ME, { id: 1 }, 'accept')
    await enqueueTransition(ME, { id: 1 }, 'start')
    await enqueueTransition(ME, { id: 2 }, 'accept')

    await flushQueue(ME, server.send)

    expect(server.calls).toEqual(['accept 1', 'accept 2'])
    expect(server.states.get(2)).toBe('accepted')
  })

  it('replaying the same queued transition twice produces one server event', async () => {
    await enqueueTransition(ME, { id: 1 }, 'accept')
    await enqueueTransition(ME, { id: 1 }, 'accept')
    expect(await listQueue(ME)).toHaveLength(1)

    await Promise.all([flushQueue(ME, server.send), flushQueue(ME, server.send)])
    await flushQueue(ME, server.send)

    expect(server.events).toBe(1)
  })

  it('a row replayed after resetInFlight yields no second event', async () => {
    const row = await enqueueTransition(ME, { id: 1 }, 'accept')
    await markInFlight(row.seq!)
    // The server applied the move, then a reload cut the pass off before markDone.
    await server.send(row)

    await resetInFlight(ME)
    await flushQueue(ME, server.send)

    expect(server.events).toBe(1)
    expect(await listQueue(ME)).toHaveLength(0)
  })

  it('a 409 on replay is surfaced, not dropped', async () => {
    server.states.set(1, 'created')
    await enqueueTransition(ME, { id: 1 }, 'accept')
    await enqueueTransition(ME, { id: 1 }, 'start')

    const result = await flushQueue(ME, server.send)

    expect(result).toEqual({ sent: 0, conflicts: 1 })
    const rows = await listQueue(ME)
    expect(rows.map((r) => [r.action, r.status])).toEqual([
      ['accept', 'conflict'],
      ['start', 'pending'],
    ])
    expect(rows[0].conflict).toEqual({ code: 'conflict', message: 'Job changed on the server; not applied.' })
    expect(server.calls).toEqual(['accept 1'])
  })

  it.each([
    [404, 'Job no longer available.'],
    [400, 'Proof was rejected.'],
    [413, 'Proof was rejected.'],
  ])('a %i keeps the row as a conflict with its message', async (status, message) => {
    server.failNext = new ApiError(status, 'code')
    await enqueueTransition(ME, { id: 1 }, 'accept')

    await flushQueue(ME, server.send)

    const [row] = await listQueue(ME)
    expect(row.status).toBe('conflict')
    expect(row.conflict?.message).toBe(message)
  })

  it('retry sends the conflict again; discard removes the job rows', async () => {
    server.states.set(1, 'created')
    const accept = await enqueueTransition(ME, { id: 1 }, 'accept')
    await enqueueTransition(ME, { id: 1 }, 'start')
    await flushQueue(ME, server.send)

    await retry(accept.seq!)
    await flushQueue(ME, server.send)
    expect(server.calls).toEqual(['accept 1', 'accept 1'])
    expect((await listQueue(ME))[0].status).toBe('conflict')

    await discardJob(ME, 1)
    expect(await listQueue(ME)).toHaveLength(0)
  })

  it.each([
    ['503', new ApiError(503, 'unavailable')],
    ['401', new ApiError(401, 'unauthorized')],
    ['a network TypeError', new TypeError('Failed to fetch')],
  ])('%s leaves rows pending and stops the pass', async (_label, err) => {
    server.failNext = err
    await enqueueTransition(ME, { id: 1 }, 'accept')
    await enqueueTransition(ME, { id: 2 }, 'accept')

    const result = await flushQueue(ME, server.send)

    expect(result).toEqual({ sent: 0, conflicts: 0 })
    expect(server.calls).toEqual(['accept 1'])
    expect((await listQueue(ME)).map((r) => [r.jobId, r.status])).toEqual([
      [1, 'pending'],
      [2, 'pending'],
    ])
  })

  it('reports syncing while a pass is running', async () => {
    await enqueueTransition(ME, { id: 1 }, 'accept')
    let during = false
    const send = async () => {
      during = isSyncing()
    }

    await flushQueue(ME, send)

    expect(during).toBe(true)
    expect(isSyncing()).toBe(false)
  })
})
