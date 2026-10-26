import { useSyncExternalStore } from 'react'

export type WsStatus = 'live' | 'reconnecting' | 'offline'

let status: WsStatus = 'reconnecting'
const listeners = new Set<() => void>()

export function setWsStatus(next: WsStatus): void {
  if (next === status) return
  status = next
  for (const listener of listeners) listener()
}

export function useWsStatus(): WsStatus {
  return useSyncExternalStore(
    (listener) => {
      listeners.add(listener)
      return () => {
        listeners.delete(listener)
      }
    },
    () => status,
  )
}
