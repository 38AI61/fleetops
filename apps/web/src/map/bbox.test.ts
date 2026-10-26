import { describe, expect, it } from 'vitest'
import { bboxParam } from './bbox'

describe('bboxParam', () => {
  it('bboxParam clamps to world bounds and formats six decimals', () => {
    expect(bboxParam({ west: -200, south: -95, east: 10.1234567, north: 95 })).toBe(
      '-180.000000,-90.000000,10.123457,90.000000',
    )
    expect(bboxParam({ west: 13.3, south: 52.45, east: 13.5, north: 52.6 })).toBe(
      '13.300000,52.450000,13.500000,52.600000',
    )
  })

  it('bboxParam returns the world box when the view wraps the antimeridian', () => {
    expect(bboxParam({ west: 170, south: 0, east: -170, north: 10 })).toBe('-180,-90,180,90')
    expect(bboxParam({ west: -200, south: 0, east: 200, north: 10 })).toBe('-180,-90,180,90')
  })
})
