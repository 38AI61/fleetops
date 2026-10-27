import { describe, expect, it } from 'vitest'
import { fixToIngestBody } from './payload'

const BASE = { latitude: 41.0082, longitude: 28.9784, accuracy: 12.5 }

describe('fixToIngestBody', () => {
  it('maps null speed and heading to 0', () => {
    const body = fixToIngestBody({ ...BASE, speed: null, heading: null }, 0)
    expect(body.speed).toBe(0)
    expect(body.heading).toBe(0)
  })

  it('maps NaN speed and heading to 0', () => {
    const body = fixToIngestBody({ ...BASE, speed: Number.NaN, heading: Number.NaN }, 0)
    expect(body.speed).toBe(0)
    expect(body.heading).toBe(0)
  })

  it('keeps real speed and heading', () => {
    const body = fixToIngestBody({ ...BASE, speed: 8.4, heading: 270 }, 0)
    expect(body.speed).toBe(8.4)
    expect(body.heading).toBe(270)
  })

  it('writes ts as an ISO string from the fix timestamp', () => {
    const body = fixToIngestBody({ ...BASE, speed: null, heading: null }, 1_700_000_000_000)
    expect(body.ts).toBe('2023-11-14T22:13:20.000Z')
  })

  it('has exactly the ingest field set', () => {
    const body = fixToIngestBody({ ...BASE, speed: 1, heading: 2 }, 0)
    expect(Object.keys(body).sort()).toEqual(['accuracy', 'heading', 'lat', 'lng', 'speed', 'ts'])
    expect(body).toMatchObject({ lat: 41.0082, lng: 28.9784, accuracy: 12.5 })
  })
})
