import { useEffect, useState } from 'react'
import type { ActionRow } from './db'
import { isSyncing } from './flush'
import { listQueue, subscribeQueue } from './queue'

export interface QueueView {
  rows: ActionRow[]
  syncing: boolean
}

// Live view of one user's queue. Every queue write re-reads it (GF-241).
export function useQueue(userId: string): QueueView {
  const [view, setView] = useState<QueueView>({ rows: [], syncing: false })
  useEffect(() => {
    let live = true
    const refresh = () => {
      listQueue(userId).then(
        (rows) => {
          if (live) setView({ rows, syncing: isSyncing() })
        },
        () => undefined,
      )
    }
    refresh()
    const unsubscribe = subscribeQueue(refresh)
    return () => {
      live = false
      unsubscribe()
    }
  }, [userId])
  return view
}
