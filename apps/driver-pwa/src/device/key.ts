// Device key storage and validation (D-29, D-30, GF-228). The key is the phone's
// credential for POST /ingest/position only. It is never a job credential.

export const DEVICE_KEY_STORAGE = 'fleetops.driver.deviceKey.v1'

// Same grammar as the server (GF-31): fo_dev_<device_id>_<43 base64url chars>.
const KEY_PATTERN = /^fo_dev_([1-9][0-9]*)_[A-Za-z0-9_-]{43}$/

export type KeyCheck = { ok: true; key: string } | { ok: false; reason: 'empty' | 'format' }

// Pure. Trims surrounding whitespace, then matches the grammar. No network call.
export function validateDeviceKey(raw: string): KeyCheck {
  const key = raw.trim()
  if (key === '') return { ok: false, reason: 'empty' }
  if (!KEY_PATTERN.test(key)) return { ok: false, reason: 'format' }
  return { ok: true, key }
}

// Returns the stored key, or null when none is stored or the stored value is no
// longer valid.
export function loadDeviceKey(): string | null {
  const check = validateDeviceKey(localStorage.getItem(DEVICE_KEY_STORAGE) ?? '')
  return check.ok ? check.key : null
}

// Stores a key that already passed validateDeviceKey.
export function saveDeviceKey(key: string): void {
  localStorage.setItem(DEVICE_KEY_STORAGE, key.trim())
}

export function clearDeviceKey(): void {
  localStorage.removeItem(DEVICE_KEY_STORAGE)
}
