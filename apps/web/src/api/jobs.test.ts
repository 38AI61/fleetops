import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { assignJob, createJob, fetchNearest, jobsQuery, listJobs } from './jobs'

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

describe('jobs api', () => {
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('jobsQuery sends only the filters that are set', () => {
    expect(jobsQuery({})).toBe('/jobs')
    expect(jobsQuery({ state: 'created', driver: 7, day: '2026-10-27' })).toBe(
      '/jobs?state=created&driver=7&day=2026-10-27',
    )
    expect(jobsQuery({ driver: 0 })).toBe('/jobs?driver=0')
  })

  it('listJobs GETs the filtered path and returns the jobs array', async () => {
    fetchMock.mockResolvedValue(json({ jobs: [{ id: 1 }] }))
    await expect(listJobs({ state: 'assigned' })).resolves.toEqual([{ id: 1 }])
    const [path, init] = fetchMock.mock.calls[0]
    expect(path).toBe('/jobs?state=assigned')
    expect(init.method).toBeUndefined()
  })

  it('createJob POSTs the body as JSON', async () => {
    const body = { pickup: { lat: 41, lng: 29 }, dropoff: { lat: 41.1, lng: 29.1 }, size_class: 'small' as const }
    fetchMock.mockResolvedValue(json({ id: 9 }, 201))
    await expect(createJob(body)).resolves.toEqual({ id: 9 })
    const [path, init] = fetchMock.mock.calls[0]
    expect(path).toBe('/jobs')
    expect(init.method).toBe('POST')
    expect((init.headers as Headers).get('Content-Type')).toBe('application/json')
    expect(JSON.parse(init.body as string)).toEqual(body)
  })

  it('assignJob POSTs vehicle_id and driver_id to the job route', async () => {
    fetchMock.mockResolvedValue(json({ id: 5, state: 'assigned' }))
    await assignJob(5, 11, 22)
    const [path, init] = fetchMock.mock.calls[0]
    expect(path).toBe('/jobs/5/assign')
    expect(init.method).toBe('POST')
    expect(JSON.parse(init.body as string)).toEqual({ vehicle_id: 11, driver_id: 22 })
  })

  it('assignJob surfaces the error code from the envelope', async () => {
    fetchMock.mockResolvedValue(json({ error: 'vehicle_not_active' }, 409))
    await expect(assignJob(5, 11, 22)).rejects.toMatchObject({ status: 409, code: 'vehicle_not_active' })
  })

  it('fetchNearest GETs the nearest route with the limit', async () => {
    fetchMock.mockResolvedValue(json({ job_id: 5, candidates: [] }))
    await expect(fetchNearest(5, 10)).resolves.toEqual({ job_id: 5, candidates: [] })
    expect(fetchMock.mock.calls[0][0]).toBe('/jobs/5/nearest?limit=10')
  })
})
