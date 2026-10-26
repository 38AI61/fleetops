import { describe, expect, it } from 'vitest'
import { followCenter, nextFollowId } from './follow'

describe('follow', () => {
  it('followCenter returns the new position when the followed vehicle moves', () => {
    expect(followCenter({ lat: 1, lng: 2 }, { lat: 3, lng: 4 })).toEqual([4, 3])
  })

  it('followCenter returns null when the position is unchanged', () => {
    expect(followCenter({ lat: 1, lng: 2 }, { lat: 1, lng: 2 })).toBeNull()
  })

  it('nextFollowId clears follow on a user drag only', () => {
    expect(nextFollowId(7, { type: 'dragstart', originalEvent: new Event('mousedown') })).toBeNull()
    expect(nextFollowId(7, { type: 'dragstart' })).toBe(7)
  })

  it('nextFollowId keeps follow on zoom and on programmatic moves', () => {
    expect(nextFollowId(7, { type: 'zoomstart', originalEvent: new Event('wheel') })).toBe(7)
    expect(nextFollowId(7, { type: 'zoomstart' })).toBe(7)
    expect(nextFollowId(7, { type: 'moveend' })).toBe(7)
  })
})
