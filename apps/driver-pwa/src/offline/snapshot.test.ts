import { beforeEach, describe, expect, it } from 'vitest'
import type { DriverJob } from '../api/types'
import { resetQueueDb } from '../test/queue'
import { loadSnapshot, saveSnapshot } from './snapshot'

function job(id: number): DriverJob {
  return {
    id,
    state: 'assigned',
    pickup: { lat: 41.0, lng: 29.0 },
    dropoff: { lat: 41.1, lng: 29.1 },
    window_start: null,
    window_end: null,
    notes: null,
    size_class: null,
    weight_kg: null,
    failure_reason: null,
    delivered_at: null,
    proof_photo_url: null,
  }
}

describe('snapshot', () => {
  beforeEach(async () => {
    await resetQueueDb()
  })

  it('saves and loads the payload with fetchedAt set', async () => {
    const before = Date.now()
    await saveSnapshot('7', [job(1), job(2)])

    const loaded = await loadSnapshot('7')

    expect(loaded?.payload.map((j) => j.id)).toEqual([1, 2])
    expect(loaded?.fetchedAt).toBeGreaterThanOrEqual(before)
    expect(loaded?.fetchedAt).toBeLessThanOrEqual(Date.now())
  })

  it('a later save replaces the earlier payload for that user', async () => {
    await saveSnapshot('7', [job(1)], 1_000)
    await saveSnapshot('7', [job(2)], 2_000)

    expect(await loadSnapshot('7')).toEqual({ payload: [job(2)], fetchedAt: 2_000 })
  })

  it('keeps each user snapshot separate', async () => {
    await saveSnapshot('7', [job(1)], 1_000)
    await saveSnapshot('9', [job(5)], 2_000)

    expect((await loadSnapshot('7'))?.payload.map((j) => j.id)).toEqual([1])
    expect((await loadSnapshot('9'))?.payload.map((j) => j.id)).toEqual([5])
  })

  it('returns null for a user with no snapshot', async () => {
    expect(await loadSnapshot('404')).toBeNull()
  })
})
