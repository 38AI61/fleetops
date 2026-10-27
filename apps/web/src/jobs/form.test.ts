import { describe, expect, it } from 'vitest'
import { toCreateBody, validateJobForm, type JobFormValues } from './form'

const pickup = { lat: 41.0, lng: 29.0 }
const dropoff = { lat: 41.1, lng: 29.1 }

function values(over: Partial<JobFormValues> = {}): JobFormValues {
  return {
    pickup,
    dropoff,
    windowStart: '',
    windowEnd: '',
    notes: '',
    sizeClass: '',
    weightKg: '',
    ...over,
  }
}

describe('validateJobForm', () => {
  it('requires pickup and dropoff', () => {
    expect(validateJobForm(values({ pickup: null, dropoff: null }))).toEqual({
      pickup: expect.any(String),
      dropoff: expect.any(String),
    })
  })

  it('accepts a minimal valid form', () => {
    expect(validateJobForm(values())).toEqual({})
  })

  it('rejects a window end before the start, and allows equal times to reach the server', () => {
    expect(validateJobForm(values({ windowStart: '2026-10-27T10:00', windowEnd: '2026-10-27T09:00' })).windowEnd).toBeDefined()
    expect(validateJobForm(values({ windowStart: '2026-10-27T10:00', windowEnd: '2026-10-27T10:00' }))).toEqual({})
  })

  it('counts notes in runes, so 1000 emoji is fine and 1001 is not', () => {
    expect(validateJobForm(values({ notes: '😀'.repeat(1000) }))).toEqual({})
    expect(validateJobForm(values({ notes: '😀'.repeat(1001) })).notes).toBeDefined()
    expect(validateJobForm(values({ notes: 'a'.repeat(1001) })).notes).toBeDefined()
  })

  it('checks size class only when set', () => {
    expect(validateJobForm(values({ sizeClass: 'medium' }))).toEqual({})
    expect(validateJobForm(values({ sizeClass: 'huge' as never })).sizeClass).toBeDefined()
  })

  it('requires weight above zero only when set', () => {
    expect(validateJobForm(values({ weightKg: '' }))).toEqual({})
    expect(validateJobForm(values({ weightKg: '2.5' }))).toEqual({})
    expect(validateJobForm(values({ weightKg: '0' })).weightKg).toBeDefined()
    expect(validateJobForm(values({ weightKg: '-1' })).weightKg).toBeDefined()
    expect(validateJobForm(values({ weightKg: 'abc' })).weightKg).toBeDefined()
  })
})

describe('toCreateBody', () => {
  it('sends only the required fields when the rest are unset', () => {
    expect(toCreateBody(values())).toEqual({ pickup, dropoff })
  })

  it('converts local times to RFC3339 UTC', () => {
    const body = toCreateBody(values({ windowStart: '2026-10-27T10:00', windowEnd: '2026-10-27T12:30' }))
    expect(body.window_start).toBe(new Date('2026-10-27T10:00').toISOString())
    expect(body.window_end).toBe(new Date('2026-10-27T12:30').toISOString())
    expect(body.window_start).toMatch(/Z$/)
  })

  it('sends notes as typed, size class and weight as numbers and strings as chosen', () => {
    const body = toCreateBody(values({ notes: '  Ring twice ', sizeClass: 'large', weightKg: '12.5' }))
    expect(body).toEqual({ pickup, dropoff, notes: '  Ring twice ', size_class: 'large', weight_kg: 12.5 })
  })

  it('throws when a point is missing', () => {
    expect(() => toCreateBody(values({ pickup: null }))).toThrow()
  })
})
