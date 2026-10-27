import { QueryClient } from '@tanstack/react-query'
import { waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { transitionJob } from '../api/driver'
import { resetQueueDb } from '../test/queue'
import { enqueueTransition, listQueue } from './queue'
import { REPLAY_INTERVAL_MS, startReplay } from './replay'

vi.mock('../api/driver', () => ({ listTodayJobs: vi.fn(), transitionJob: vi.fn(), deliverJob: vi.fn() }))

const USER = '7'
const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
let stop: (() => void) | null = null

// Lets the boot pass and any queued IDB work finish.
const settle = () => new Promise((resolve) => setTimeout(resolve, 40))

describe('startReplay', () => {
  beforeEach(async () => {
    await resetQueueDb()
    vi.mocked(transitionJob).mockReset()
    vi.mocked(transitionJob).mockResolvedValue({} as never)
  })

  afterEach(() => {
    stop?.()
    stop = null
    vi.restoreAllMocks()
  })

  it('an online event triggers a flush', async () => {
    vi.mocked(transitionJob).mockRejectedValueOnce(new TypeError('Failed to fetch'))
    await enqueueTransition(USER, { id: 1 }, 'accept')
    stop = startReplay(USER, client)
    await waitFor(() => expect(transitionJob).toHaveBeenCalledTimes(1))
    await waitFor(async () => expect((await listQueue(USER))[0].status).toBe('pending'))

    window.dispatchEvent(new Event('online'))

    await waitFor(() => expect(transitionJob).toHaveBeenCalledTimes(2))
    await waitFor(async () => expect(await listQueue(USER)).toHaveLength(0))
  })

  it('the interval is armed only while the queue is non-empty', async () => {
    const setIntervalSpy = vi.spyOn(globalThis, 'setInterval')
    const clearIntervalSpy = vi.spyOn(globalThis, 'clearInterval')
    stop = startReplay(USER, client)
    await settle()
    expect(setIntervalSpy).not.toHaveBeenCalledWith(expect.any(Function), REPLAY_INTERVAL_MS)

    await enqueueTransition(USER, { id: 1 }, 'accept')
    await waitFor(() =>
      expect(setIntervalSpy).toHaveBeenCalledWith(expect.any(Function), REPLAY_INTERVAL_MS),
    )

    window.dispatchEvent(new Event('online'))

    await waitFor(() => expect(clearIntervalSpy).toHaveBeenCalled())
  })

  it('teardown clears the listeners and the timer', async () => {
    vi.mocked(transitionJob).mockRejectedValue(new TypeError('Failed to fetch'))
    const clearIntervalSpy = vi.spyOn(globalThis, 'clearInterval')
    stop = startReplay(USER, client)
    await settle()
    await enqueueTransition(USER, { id: 1 }, 'accept')
    await settle()
    const before = vi.mocked(transitionJob).mock.calls.length

    stop()
    stop = null
    window.dispatchEvent(new Event('online'))
    await settle()

    expect(clearIntervalSpy).toHaveBeenCalled()
    expect(vi.mocked(transitionJob).mock.calls.length).toBe(before)
  })
})
