import { markBooted, setSession } from '../session/session'

// A JWT-shaped string with a base64url payload. decodeClaims reads only the payload.
export function token(claims: Record<string, unknown>): string {
  const b64 = (s: string) => btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
  return `${b64('{"alg":"none"}')}.${b64(JSON.stringify(claims))}.sig`
}

// Sets an in-memory session for the given role and marks the boot as done.
export function signInAs(role: string, fleetId = 1): string {
  const accessToken = token({ sub: '7', fleet_id: fleetId, role, exp: 4102444800 })
  setSession({
    accessToken,
    expiresAt: Date.now() + 900_000,
    claims: { sub: '7', fleet_id: fleetId, role, exp: 4102444800 },
  })
  markBooted()
  return accessToken
}
