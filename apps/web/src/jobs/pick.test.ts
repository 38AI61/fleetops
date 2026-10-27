import { describe, expect, it } from 'vitest'
import { clearDropoff, clearPickup, emptyPick, marksCollection, pickReducer } from './pick'

const a = { lat: 41.0, lng: 29.0 }
const b = { lat: 41.1, lng: 29.1 }
const c = { lat: 41.2, lng: 29.2 }

describe('pickReducer', () => {
  it('first click sets the pickup', () => {
    expect(pickReducer(emptyPick, a)).toEqual({ pickup: a, dropoff: null })
  })

  it('second click sets the dropoff', () => {
    expect(pickReducer({ pickup: a, dropoff: null }, b)).toEqual({ pickup: a, dropoff: b })
  })

  it('third click replaces the dropoff and keeps the pickup', () => {
    expect(pickReducer({ pickup: a, dropoff: b }, c)).toEqual({ pickup: a, dropoff: c })
  })

  it('clearing the pickup makes the next click a pickup again', () => {
    const cleared = clearPickup({ pickup: a, dropoff: b })
    expect(cleared).toEqual({ pickup: null, dropoff: b })
    expect(pickReducer(cleared, c)).toEqual({ pickup: c, dropoff: b })
  })

  it('clearing the dropoff keeps the pickup', () => {
    expect(clearDropoff({ pickup: a, dropoff: b })).toEqual({ pickup: a, dropoff: null })
  })

  it('clear functions return a new state and leave the input alone', () => {
    const before = { pickup: a, dropoff: b }
    const after = clearPickup(before)
    expect(after).not.toBe(before)
    expect(before).toEqual({ pickup: a, dropoff: b })
  })
})

describe('marksCollection', () => {
  it('emits one feature per picked point, in lng, lat order', () => {
    expect(marksCollection({ pickup: a, dropoff: b })).toEqual({
      type: 'FeatureCollection',
      features: [
        { type: 'Feature', properties: { kind: 'pickup' }, geometry: { type: 'Point', coordinates: [29.0, 41.0] } },
        { type: 'Feature', properties: { kind: 'dropoff' }, geometry: { type: 'Point', coordinates: [29.1, 41.1] } },
      ],
    })
  })

  it('skips unset points', () => {
    expect(marksCollection({ pickup: null }).features).toEqual([])
    expect(marksCollection({ dropoff: b }).features).toHaveLength(1)
  })
})
