// Pages Function for the web app (P24, GF-352). REST stays same-origin: the
// allowlisted API paths proxy to UPSTREAM_ORIGIN, document navigations get the SPA
// shell, and anything else falls through to the static assets.
const ALLOWED = ['/auth', '/map', '/vehicles', '/jobs', '/geofences', '/alerts', '/alert-rules', '/reports/daily']

function allowed(pathname) {
  return ALLOWED.some((p) => pathname === p || pathname.startsWith(p + '/'))
}

export async function onRequest(context) {
  const { request, env } = context
  const url = new URL(request.url)

  // UI routes such as /jobs/new share a prefix with the API. A navigation is a
  // page load, so it always gets the shell (GF-352).
  if (request.headers.get('Sec-Fetch-Mode') === 'navigate') {
    return env.ASSETS.fetch(new Request(new URL('/index.html', request.url)))
  }

  if (!allowed(url.pathname)) {
    return context.next()
  }
  if (!env.UPSTREAM_ORIGIN) {
    return new Response('UPSTREAM_ORIGIN is not set', { status: 500 })
  }
  return fetch(new Request(env.UPSTREAM_ORIGIN + url.pathname + url.search, request))
}
