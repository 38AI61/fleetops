import { useSyncExternalStore } from 'react'

// The on-shift flag (GF-232). A client flag is the only shift signal the PWA has.
export const ON_SHIFT_STORAGE = 'fleetops.driver.onShift.v1'

const listeners = new Set<() => void>()

function read(): boolean {
  return localStorage.getItem(ON_SHIFT_STORAGE) === '1'
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

// Default is off. Writes persist and notify every reader in this tab.
export function setOnShift(on: boolean): void {
  localStorage.setItem(ON_SHIFT_STORAGE, on ? '1' : '0')
  for (const listener of listeners) listener()
}

export function useOnShift(): boolean {
  return useSyncExternalStore(subscribe, read)
}
