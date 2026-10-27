import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { deliverJob, listTodayJobs, type TransitionAction } from '../api/driver'
import type { DriverJob } from '../api/types'
import { enqueueTransition } from '../offline/queue'
import { runReplayPass } from '../offline/replay'
import { loadSnapshot, saveSnapshot } from '../offline/snapshot'
import { useAuthState } from '../session/session'
import { TODAY_QUERY_KEY } from './keys'

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

function useUserId(): string {
  const { session } = useAuthState()
  return session?.claims.sub ?? ''
}

export function useTodayJobs() {
  const userId = useUserId()
  return useQuery({
    queryKey: [...TODAY_QUERY_KEY, userId],
    queryFn: () => fetchToday(userId),
    // Runs offline too, so the fallback can answer (GF-239).
    networkMode: 'always',
    refetchInterval: POLL_MS,
    refetchOnWindowFocus: true,
  })
}

export interface TransitionInput {
  job: DriverJob
  action: TransitionAction
}

// Captures the tap first: the IDB write is awaited before any request (GF-241). Online,
// the pass sends it at once; offline, it waits for the next trigger.
export function useJobTransition() {
  const client = useQueryClient()
  const userId = useUserId()
  return useMutation({
    mutationFn: async ({ job, action }: TransitionInput) => {
      await enqueueTransition(userId, job, action)
      if (navigator.onLine) await runReplayPass(userId, client)
    },
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
