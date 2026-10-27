import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { assignJob, createJob, fetchNearest, listJobs } from '../api/jobs'
import type { JobCreateBody, JobsFilter } from '../api/types'

export function jobsKey(filter: JobsFilter) {
  return ['jobs', filter] as const
}

export function nearestKey(jobId: number) {
  return ['nearest', jobId] as const
}

export function useJobs(filter: JobsFilter) {
  return useQuery({
    queryKey: jobsKey(filter),
    queryFn: () => listJobs(filter),
  })
}

// Rows change with positions and assignments, so every open panel refetches (GF-217).
export function useNearest(jobId: number, enabled: boolean) {
  return useQuery({
    queryKey: nearestKey(jobId),
    queryFn: () => fetchNearest(jobId),
    enabled,
    staleTime: 0,
  })
}

export function useCreateJob() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: JobCreateBody) => createJob(body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['jobs'] }),
  })
}

export function useAssignJob() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ jobId, vehicleId, driverId }: { jobId: number; vehicleId: number; driverId: number }) =>
      assignJob(jobId, vehicleId, driverId),
    onSuccess: (_job, vars) => {
      void qc.invalidateQueries({ queryKey: ['jobs'] })
      void qc.invalidateQueries({ queryKey: nearestKey(vars.jobId) })
    },
  })
}
