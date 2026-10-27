import { getJson, sendJson } from './client'
import type { Job, JobCreateBody, JobsFilter, NearestResult } from './types'

// The GET /jobs query string. Unset filters are omitted, so an empty filter is plain /jobs.
export function jobsQuery(filter: JobsFilter): string {
  const p = new URLSearchParams()
  if (filter.state) p.set('state', filter.state)
  if (filter.driver !== undefined) p.set('driver', String(filter.driver))
  if (filter.day) p.set('day', filter.day)
  const s = p.toString()
  return s ? `/jobs?${s}` : '/jobs'
}

export async function listJobs(filter: JobsFilter): Promise<Job[]> {
  const res = await getJson<{ jobs: Job[] }>(jobsQuery(filter))
  return res.jobs
}

export function createJob(body: JobCreateBody): Promise<Job> {
  return sendJson<Job>('POST', '/jobs', body)
}

// The existing assign route (GF-175, GF-216). No new endpoint.
export function assignJob(id: number, vehicleId: number, driverId: number): Promise<Job> {
  return sendJson<Job>('POST', `/jobs/${id}/assign`, { vehicle_id: vehicleId, driver_id: driverId })
}

export function fetchNearest(id: number, limit = 10): Promise<NearestResult> {
  return getJson<NearestResult>(`/jobs/${id}/nearest?limit=${limit}`)
}
