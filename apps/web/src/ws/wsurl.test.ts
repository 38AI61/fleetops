import { afterEach, describe, expect, it, vi } from 'vitest'

import { wsUrl } from './client'

const loc = { protocol: 'https:', host: 'fleetops.pages.dev' } as Location

describe('wsUrl (GF-352)', () => {
  afterEach(() => {
    vi.unstubAllEnvs()
  })

  it('stays same-origin when VITE_WS_BASE is unset', () => {
    vi.stubEnv('VITE_WS_BASE', '')
    expect(wsUrl(7, loc)).toBe('wss://fleetops.pages.dev/stream?fleet=7')
  })

  it('uses VITE_WS_BASE when it is set, without a trailing slash', () => {
    vi.stubEnv('VITE_WS_BASE', 'wss://fleetops.onrender.com/')
    expect(wsUrl(7, loc)).toBe('wss://fleetops.onrender.com/stream?fleet=7')
  })
})
