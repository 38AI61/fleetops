// Daily report latency (GF-337, README §9): GET /reports/daily p95 <= 300 ms.
// Reads daily_driver_stats only (P21), so it measures the indexed read path.
import http from 'k6/http';
import { check } from 'k6';
import { BASE_URL, token, jsonAuth } from './lib/config.js';

const today = new Date().toISOString().slice(0, 10);

export const options = {
  scenarios: {
    report: {
      executor: 'constant-arrival-rate',
      rate: Number(__ENV.RATE || 5),
      timeUnit: '1s',
      duration: __ENV.DURATION || '60s',
      preAllocatedVUs: 10,
      maxVUs: 50,
    },
  },
  thresholds: {
    'http_req_duration{name:report}': ['p(95)<300'],
    'http_req_failed': ['rate<0.01'],
  },
};

export default function () {
  const date = __ENV.DATE || today;
  const res = http.get(
    BASE_URL + '/reports/daily?date=' + date + '&format=json',
    Object.assign(jsonAuth(token()), { tags: { name: 'report' } }),
  );
  check(res, { 'report 200': (r) => r.status === 200 });
}
