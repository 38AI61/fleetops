import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { setSession } from '../session/session'
import { cancelProactive } from '../session/refresh'
import { signInAs } from '../test/session'
import { transitionJob } from './driver'

const JOB = { id: 7, state: 'accepted' }

beforeEach(() => {
  setSession(null)
  signInAs('driver')
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => new Response(JSON.stringify(JOB), { status: 200 })),
  )
})

afterEach(() => {
  cancelProactive()
  vi.unstubAllGlobals()
  setSession(null)
})

describe('transitionJob', () => {
  it('posts an empty JSON object to the job action route', async () => {
    await transitionJob(7, 'accept')

    const [path, init] = vi.mocked(fetch).mock.calls[0] as [string, RequestInit]
    expect(path).toBe('/driver/jobs/7/accept')
    expect(init.method).toBe('POST')
    expect(JSON.parse(String(init.body))).toEqual({})
  })
})
