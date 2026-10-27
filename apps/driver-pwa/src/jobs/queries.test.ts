import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { createElement, type ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '../api/client'
import { listTodayJobs } from '../api/driver'
import type { DriverJob } from '../api/types'
import { loadSnapshot, saveSnapshot } from '../offline/snapshot'
import { resetQueueDb } from '../test/queue'
import { signInAs } from '../test/session'
import { setSession } from '../session/session'
import { useTodayJobs } from './queries'

vi.mock('../api/driver', () => ({ listTodayJobs: vi.fn(), transitionJob: vi.fn(), deliverJob: vi.fn() }))

const USER = '7'

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

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return createElement(QueryClientProvider, { client }, children)
}

describe('useTodayJobs', () => {
  beforeEach(async () => {
    await resetQueueDb()
    signInAs('driver')
    vi.mocked(listTodayJobs).mockReset()
  })

  afterEach(() => {
    setSession(null)
  })

  it('an online success saves the snapshot and returns stale false', async () => {
    vi.mocked(listTodayJobs).mockResolvedValue([job(1)])

    const { result } = renderHook(() => useTodayJobs(), { wrapper })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data).toMatchObject({ stale: false, jobs: [job(1)] })
    expect((await loadSnapshot(USER))?.payload).toEqual([job(1)])
  })

  it('a network TypeError with a snapshot returns the snapshot as stale', async () => {
    await saveSnapshot(USER, [job(2)], 1_700_000_000_000)
    vi.mocked(listTodayJobs).mockRejectedValue(new TypeError('Failed to fetch'))

    const { result } = renderHook(() => useTodayJobs(), { wrapper })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data).toEqual({
      stale: true,
      jobs: [job(2)],
      fetchedAt: 1_700_000_000_000,
    })
  })

  it('a network TypeError without a snapshot is an error', async () => {
    vi.mocked(listTodayJobs).mockRejectedValue(new TypeError('Failed to fetch'))

    const { result } = renderHook(() => useTodayJobs(), { wrapper })

    await waitFor(() => expect(result.current.isError).toBe(true))
    expect(result.current.error).toBeInstanceOf(TypeError)
  })

  it('an ApiError 401 is not masked by the snapshot fallback', async () => {
    await saveSnapshot(USER, [job(3)], 1_700_000_000_000)
    vi.mocked(listTodayJobs).mockRejectedValue(new ApiError(401, 'unauthorized'))

    const { result } = renderHook(() => useTodayJobs(), { wrapper })

    await waitFor(() => expect(result.current.isError).toBe(true))
    expect(result.current.error).toBeInstanceOf(ApiError)
    expect(result.current.data).toBeUndefined()
  })
})
