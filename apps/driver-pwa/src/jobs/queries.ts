import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { deliverJob, listTodayJobs, transitionJob, type TransitionAction } from '../api/driver'
import type { DriverJob } from '../api/types'
import { useAuthState } from '../session/session'
import { loadSnapshot, saveSnapshot } from '../offline/snapshot'

export const TODAY_QUERY_KEY = ['driver', 'jobs', 'today'] as const

// Polling is the freshness path (GF-230). There is no WebSocket in P14.
const POLL_MS = 30_000

// stale is true when the list came from the saved snapshot (GF-239).
export interface TodayResult {
  jobs: DriverJob[]
  stale: boolean
  fetchedAt: number
}

// Only a network failure falls back to the snapshot. An ApiError is never masked (GF-239).
export async function fetchToday(userId: string): Promise<TodayResult> {
  const fetchedAt = Date.now()
  try {
    const jobs = await listTodayJobs()
    // A failed save must not fail an online answer.
    await saveSnapshot(userId, jobs, fetchedAt).catch(() => undefined)
    return { jobs, stale: false, fetchedAt }
  } catch (err) {
    if (!(err instanceof TypeError)) throw err
    const snapshot = await loadSnapshot(userId)
    if (!snapshot) throw err
    return { jobs: snapshot.payload, stale: true, fetchedAt: snapshot.fetchedAt }
  }
}

export function useTodayJobs() {
  const { session } = useAuthState()
  const userId = session?.claims.sub ?? ''
  return useQuery({
    queryKey: [...TODAY_QUERY_KEY, userId],
    queryFn: () => fetchToday(userId),
    // Runs offline too, so the fallback can answer (GF-239).
    networkMode: 'always',
    refetchInterval: POLL_MS,
    refetchOnWindowFocus: true,
  })
}

// Refetches the list once the move settles, so a 409 also shows the current state.
export function useJobTransition() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ id, action }: { id: number; action: TransitionAction }) =>
      transitionJob(id, action),
    onSettled: () => client.invalidateQueries({ queryKey: TODAY_QUERY_KEY }),
  })
}

// A delivery settles the same way as a transition: the list refetches either way.
export function useDeliverJob() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ id, form }: { id: number; form: FormData }) => deliverJob(id, form),
    onSettled: () => client.invalidateQueries({ queryKey: TODAY_QUERY_KEY }),
  })
}
