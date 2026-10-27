import { singleFlightRefresh } from './refresh'
import { markBooted } from './session'

let boot: Promise<boolean> | null = null

// Runs once per page load. The access token is memory-only, so the refresh cookie
// restores the session. A second call would rotate an already-rotated refresh
// token (D-02), so the promise is memoized.
export function bootSession(): Promise<boolean> {
  boot ??= singleFlightRefresh().then((ok) => {
    markBooted()
    return ok
  })
  return boot
}
