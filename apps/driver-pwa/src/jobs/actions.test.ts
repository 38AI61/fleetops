import { describe, expect, it } from 'vitest'
import { DRIVER_EDGES, actionsFor } from './actions'

// The D-23 driver edges, copied from internal/jobs/transitions.go. The table below
// must cover every one of them.
const D23_DRIVER_EDGES = [
  ['assigned', 'accepted'],
  ['accepted', 'en_route'],
  ['en_route', 'picked_up'],
  ['picked_up', 'delivered'],
  ['accepted', 'failed'],
  ['en_route', 'failed'],
  ['picked_up', 'failed'],
] as const

describe('transition mapping (BINDING)', () => {
  it.each([
    ['assigned', 'accept'],
    ['accepted', 'start'],
    ['en_route', 'pickup'],
    ['picked_up', 'deliver'],
    ['created', null],
    ['delivered', null],
    ['failed', null],
    ['cancelled', null],
    ['bogus', null],
  ] as const)('%s → %s', (state, expected) => {
    expect(actionsFor(state)).toBe(expected)
  })

  it('covers every D-23 driver edge', () => {
    expect(DRIVER_EDGES).toHaveLength(D23_DRIVER_EDGES.length)
    for (const [from, to] of D23_DRIVER_EDGES) {
      expect(DRIVER_EDGES.some((edge) => edge.from === from && edge.to === to)).toBe(true)
    }
  })

  it.each(['accepted', 'en_route', 'picked_up'] as const)(
    'offers the fail edge from %s with no action',
    (from) => {
      const edge = DRIVER_EDGES.find((e) => e.from === from && e.to === 'failed')
      expect(edge).toBeDefined()
      expect(edge?.action).toBeNull()
    },
  )
})
