import { queryClient } from '../api/queryClient'
import { cancelProactive } from './refresh'
import { setSession } from './session'

// Local state clears even when the request fails (GF-154). The route guard
// sends the user to /login once the session is gone.
export async function logout(): Promise<void> {
  try {
    await fetch('/auth/logout', { method: 'POST', credentials: 'same-origin' })
  } catch {
    // The server cannot be reached. Local state still clears below.
  } finally {
    cancelProactive()
    setSession(null)
    queryClient.clear()
  }
}
