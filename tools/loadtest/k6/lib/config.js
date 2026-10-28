// Shared k6 helpers (GF-337). Scenario knobs are k6 run parameters passed with
// `-e NAME=value`, not FleetOps environment variables, so no D-22 row applies.
// Secrets come from the environment: TOKEN is the admin access JWT (GF-52, GF-64).

export const BASE_URL = __ENV.BASE_URL || 'http://127.0.0.1:10010';
export const WS_URL = __ENV.WS_URL || 'ws://127.0.0.1:10011';
export const METRICS_URL = __ENV.METRICS_URL || 'http://127.0.0.1:10008/metrics';

// token returns the admin access JWT. It fails the run when TOKEN is absent,
// so no scenario sends unauthenticated traffic by accident.
export function token() {
  const t = __ENV.TOKEN;
  if (!t) {
    throw new Error('TOKEN is required: pass -e TOKEN=<admin access JWT>');
  }
  return t;
}

// jsonAuth is the header set for an authenticated JSON request.
export function jsonAuth(tok) {
  return { headers: { Authorization: 'Bearer ' + tok, 'Content-Type': 'application/json' } };
}

// parseDevices reads deploy/.local/devices.json text (GF-136, GF-137).
export function parseDevices(text) {
  return JSON.parse(text).devices;
}

// parseDemoUsers reads deploy/.local/demo-users.json text (GF-339).
export function parseDemoUsers(text) {
  return JSON.parse(text);
}

// positionBody is one ingest body for a device. The key goes in X-Device-Key,
// never in this body (GF-52).
export function positionBody(dev, i) {
  const wp = dev.waypoints[i % dev.waypoints.length];
  return { lat: wp.lat, lng: wp.lng, speed: 30, heading: 90, accuracy: 5.0, ts: new Date().toISOString() };
}

// deviceHeaders is the ingest header set for one device.
export function deviceHeaders(dev) {
  return { headers: { 'X-Device-Key': dev.key, 'Content-Type': 'application/json' } };
}
