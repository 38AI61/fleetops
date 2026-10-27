import { describe, expect, it } from 'vitest'
import { formatDistance } from './format'

describe('formatDistance', () => {
  it('rounds below 1000 m to whole meters', () => {
    expect(formatDistance(0)).toBe('0 m')
    expect(formatDistance(999.4)).toBe('999 m')
  })

  it('switches to kilometers at 1000 m with two decimals', () => {
    expect(formatDistance(1000)).toBe('1.00 km')
    expect(formatDistance(1234.5)).toBe('1.23 km')
    expect(formatDistance(12345.6)).toBe('12.35 km')
  })

  it('never shows 1000 m', () => {
    expect(formatDistance(999.6)).toBe('1.00 km')
  })
})
