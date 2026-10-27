import { act, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { DEVICE_KEY_STORAGE } from '../device/key'
import { setOnShift } from './shift'
import { PositionReporter } from './PositionReporter'

// Fixture only: id 12, secret of 43 'A' characters.
const VALID = `fo_dev_12_${'A'.repeat(43)}`

function stubFix() {
  Object.defineProperty(navigator, 'geolocation', {
    configurable: true,
    value: {
      getCurrentPosition: (success: PositionCallback) =>
        success({
          coords: { latitude: 41.0, longitude: 29.0, accuracy: 8, speed: null, heading: null },
          timestamp: 1_700_000_000_000,
        } as unknown as GeolocationPosition),
    },
  })
}

function respond(...statuses: number[]) {
  const queue = [...statuses]
  return vi.fn(async () => new Response(null, { status: queue.shift() ?? 204 }))
}

function renderReporter(onKeyRejected = vi.fn()) {
  return render(<PositionReporter deviceKey={VALID} onKeyRejected={onKeyRejected} />)
}

// Advances fake time by one tick and lets the tick's promises settle.
async function tick(ms = 10_000) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms)
  })
}

describe('PositionReporter', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    localStorage.clear()
    localStorage.setItem(DEVICE_KEY_STORAGE, VALID)
    setOnShift(true)
    stubFix()
    Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => 'visible' })
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
    Reflect.deleteProperty(navigator, 'geolocation')
    Reflect.deleteProperty(document, 'visibilityState')
    localStorage.clear()
  })

  it('posts on each tick while on shift, with the device key header and body', async () => {
    vi.stubGlobal('fetch', respond(204, 204))
    renderReporter()

    await tick()
    expect(fetch).toHaveBeenCalledTimes(1)
    const [path, init] = vi.mocked(fetch).mock.calls[0] as [string, RequestInit]
    expect(path).toBe('/ingest/position')
    expect(init.method).toBe('POST')
    expect(new Headers(init.headers).get('X-Device-Key')).toBe(VALID)
    expect(new Headers(init.headers).get('Authorization')).toBeNull()
    expect(JSON.parse(String(init.body))).toEqual({
      lat: 41,
      lng: 29,
      speed: 0,
      heading: 0,
      accuracy: 8,
      ts: '2023-11-14T22:13:20.000Z',
    })

    await tick()
    expect(fetch).toHaveBeenCalledTimes(2)
  })

  it('posts nothing after the shift is turned off', async () => {
    vi.stubGlobal('fetch', respond(204))
    renderReporter()
    await tick()
    expect(fetch).toHaveBeenCalledTimes(1)

    act(() => setOnShift(false))
    await tick(30_000)

    expect(fetch).toHaveBeenCalledTimes(1)
  })

  it('drops a 503 and the next tick posts', async () => {
    vi.stubGlobal('fetch', respond(503, 204))
    const onKeyRejected = vi.fn()
    renderReporter(onKeyRejected)

    await tick()
    expect(fetch).toHaveBeenCalledTimes(1)
    expect(localStorage.getItem(DEVICE_KEY_STORAGE)).toBe(VALID)

    await tick()
    expect(fetch).toHaveBeenCalledTimes(2)
    expect(onKeyRejected).not.toHaveBeenCalled()
  })

  it('a 401 clears the key, calls onKeyRejected once, and stops', async () => {
    vi.stubGlobal('fetch', respond(401))
    const onKeyRejected = vi.fn()
    renderReporter(onKeyRejected)

    await tick()

    expect(localStorage.getItem(DEVICE_KEY_STORAGE)).toBeNull()
    expect(onKeyRejected).toHaveBeenCalledTimes(1)

    await tick()
    expect(fetch).toHaveBeenCalledTimes(1)
  })

  it('a 400 shows the rejected status and does not clear the key', async () => {
    vi.stubGlobal('fetch', respond(400))
    renderReporter()

    await tick()

    expect(screen.getByRole('status')).toHaveTextContent('Position rejected')
    expect(localStorage.getItem(DEVICE_KEY_STORAGE)).toBe(VALID)
  })

  it('skips ticks while the page is hidden', async () => {
    vi.stubGlobal('fetch', respond(204))
    Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => 'hidden' })
    renderReporter()

    await tick()
    expect(fetch).not.toHaveBeenCalled()

    Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => 'visible' })
    await tick()
    expect(fetch).toHaveBeenCalledTimes(1)
  })
})
