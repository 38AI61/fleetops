import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { deliverJob, listTodayJobs, transitionJob, type TransitionAction } from '../api/driver'

export const TODAY_QUERY_KEY = ['driver', 'jobs', 'today'] as const

// Polling is the freshness path (GF-230). There is no WebSocket in P14.
const POLL_MS = 30_000

export function useTodayJobs() {
  return useQuery({
    queryKey: TODAY_QUERY_KEY,
    queryFn: listTodayJobs,
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
