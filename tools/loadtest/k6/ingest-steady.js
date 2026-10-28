// Ingest steady state (GF-337, roadmap §5): 100 messages/s, no failures, no
// dropped iterations. The device key goes in X-Device-Key only (GF-52, GF-64).
import http from 'k6/http';
import { check } from 'k6';
import { INGEST_URL, parseDevices, positionBody, deviceHeaders } from './lib/config.js';

const devices = parseDevices(open('../../../deploy/.local/devices.json'));

export const options = {
  scenarios: {
    steady: {
      executor: 'constant-arrival-rate',
      rate: 100,
      timeUnit: '1s',
      duration: __ENV.DURATION || '60s',
      preAllocatedVUs: 50,
      maxVUs: 200,
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.001'],
    http_req_duration: ['p(95)<300'],
    dropped_iterations: ['count==0'],
  },
};

export default function () {
  const dev = devices[__ITER % devices.length];
  const res = http.post(
    INGEST_URL + '/ingest/position',
    JSON.stringify(positionBody(dev, __ITER)),
    deviceHeaders(dev),
  );
  check(res, { 'accepted 204': (r) => r.status === 204 });
}
