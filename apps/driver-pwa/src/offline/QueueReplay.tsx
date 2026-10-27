import { useEffect } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useAuthState } from '../session/session'
import { startReplay } from './replay'

// Runs the replay triggers for the signed-in driver and stops them on logout (GF-238).
// Renders nothing.
export function QueueReplay() {
  const { session } = useAuthState()
  const userId = session?.claims.sub
  const client = useQueryClient()
  useEffect(() => {
    if (!userId) return
    return startReplay(userId, client)
  }, [userId, client])
  return null
}
