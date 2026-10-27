import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import {
  DEVICE_KEY_STORAGE,
  clearDeviceKey,
  loadDeviceKey,
  saveDeviceKey,
  validateDeviceKey,
} from './key'

// Fixture only: the id is 12 and the secret is 43 'A' characters. No real key.
const VALID = `fo_dev_12_${'A'.repeat(43)}`

describe('validateDeviceKey (BINDING)', () => {
  it.each([
    ['valid key', VALID, { ok: true, key: VALID }],
    ['valid with surrounding whitespace (trimmed)', `  \t${VALID}\n `, { ok: true, key: VALID }],
    ['bad prefix', `fo_tracker_12_${'A'.repeat(43)}`, { ok: false, reason: 'format' }],
    ['non-numeric id', `fo_dev_abc_${'A'.repeat(43)}`, { ok: false, reason: 'format' }],
    ['leading zero in id', `fo_dev_012_${'A'.repeat(43)}`, { ok: false, reason: 'format' }],
    ['secret 42 chars', `fo_dev_12_${'A'.repeat(42)}`, { ok: false, reason: 'format' }],
    ['secret 44 chars', `fo_dev_12_${'A'.repeat(44)}`, { ok: false, reason: 'format' }],
    ['forbidden +', `fo_dev_12_${'A'.repeat(42)}+`, { ok: false, reason: 'format' }],
    ['forbidden /', `fo_dev_12_${'A'.repeat(42)}/`, { ok: false, reason: 'format' }],
    ['forbidden =', `fo_dev_12_${'A'.repeat(42)}=`, { ok: false, reason: 'format' }],
    ['forbidden .', `fo_dev_12_${'A'.repeat(42)}.`, { ok: false, reason: 'format' }],
    [
      'internal whitespace',
      `fo_dev_12_${'A'.repeat(20)} ${'A'.repeat(22)}`,
      { ok: false, reason: 'format' },
    ],
    ['empty', '', { ok: false, reason: 'empty' }],
    ['whitespace only', ' \n\t ', { ok: false, reason: 'empty' }],
  ])('%s', (_name, raw, expected) => {
    expect(validateDeviceKey(raw)).toEqual(expected)
  })
})

describe('device key storage', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  afterEach(() => {
    localStorage.clear()
  })

  it('stores under the versioned key and loads it back', () => {
    saveDeviceKey(VALID)
    expect(localStorage.getItem(DEVICE_KEY_STORAGE)).toBe(VALID)
    expect(loadDeviceKey()).toBe(VALID)
  })

  it('returns null for a stored value that no longer validates', () => {
    localStorage.setItem(DEVICE_KEY_STORAGE, 'fo_tracker_12_nope')
    expect(loadDeviceKey()).toBeNull()
  })

  it('clears the stored key', () => {
    saveDeviceKey(VALID)
    clearDeviceKey()
    expect(loadDeviceKey()).toBeNull()
  })
})
