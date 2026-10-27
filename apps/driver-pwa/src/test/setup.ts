import { cleanup } from '@testing-library/react'
import { afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
// Real IndexedDB semantics for the offline queue (D-37).
import 'fake-indexeddb/auto'

// Node 26 ships an experimental localStorage global that is undefined without
// --localstorage-file, and it hides jsdom's. Install an in-memory Storage only
// when the global does not work, so the device-key tests run on every host.
if (typeof globalThis.localStorage?.getItem !== 'function') {
  const data = new Map<string, string>()
  const memory: Storage = {
    get length() {
      return data.size
    },
    clear: () => data.clear(),
    getItem: (key) => data.get(key) ?? null,
    key: (index) => [...data.keys()][index] ?? null,
    removeItem: (key) => {
      data.delete(key)
    },
    setItem: (key, value) => {
      data.set(key, String(value))
    },
  }
  Object.defineProperty(globalThis, 'localStorage', { value: memory, configurable: true, writable: true })
}

// Vitest globals are off, so Testing Library's auto-cleanup does not run.
afterEach(() => {
  cleanup()
})
