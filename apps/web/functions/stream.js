// WebSocket pass-through for the dispatcher (P24, GF-352). Origin is forwarded
// unchanged, so WS_ORIGIN_PATTERNS on the api host must allow the Pages origin.
export async function onRequest(context) {
  const { request, env } = context
  if (request.headers.get('Upgrade') !== 'websocket') {
    return new Response('expected a WebSocket upgrade', { status: 426, headers: { Upgrade: 'websocket' } })
  }
  if (!env.UPSTREAM_ORIGIN) {
    return new Response('UPSTREAM_ORIGIN is not set', { status: 500 })
  }
  const url = new URL(request.url)
  return fetch(new Request(env.UPSTREAM_ORIGIN + '/stream' + url.search, request))
}
