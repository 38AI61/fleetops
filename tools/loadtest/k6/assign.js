// Assign latency (GF-337, README §9): POST /jobs/:id/assign p95 <= 300 ms.
// Each iteration creates a job, then assigns it to a demo van and driver.
import http from 'k6/http';
import { check } from 'k6';
import { BASE_URL, token, jsonAuth, parseDemoUsers } from './lib/config.js';

const users = parseDemoUsers(open('../../../deploy/.local/demo-users.json'));

export const options = {
  scenarios: {
    assign: {
      executor: 'constant-arrival-rate',
      rate: Number(__ENV.RATE || 5),
      timeUnit: '1s',
      duration: __ENV.DURATION || '60s',
      preAllocatedVUs: 10,
      maxVUs: 50,
    },
  },
  thresholds: {
    'http_req_duration{name:assign}': ['p(95)<300'],
    'http_req_failed': ['rate<0.01'],
  },
};

export default function () {
  const tok = token();
  const created = http.post(
    BASE_URL + '/jobs',
    JSON.stringify({ pickup: { lat: 52.52, lng: 13.40 }, dropoff: { lat: 52.50, lng: 13.35 } }),
    Object.assign(jsonAuth(tok), { tags: { name: 'create' } }),
  );
  if (!check(created, { 'job created': (r) => r.status === 201 })) {
    return;
  }
  const d = users.drivers[__ITER % users.drivers.length];
  const res = http.post(
    BASE_URL + '/jobs/' + created.json('id') + '/assign',
    JSON.stringify({ vehicle_id: d.vehicle_id, driver_id: d.driver_id }),
    Object.assign(jsonAuth(tok), { tags: { name: 'assign' } }),
  );
  check(res, { 'assigned': (r) => r.status === 200 });
}
