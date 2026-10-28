// Ingest burst (GF-337, roadmap §5): ramp to 2,000 messages/s (10x target) with
// no unbounded memory. A single sampler scrapes ingest /metrics every 5 s and
// checks heap in use against 2x the first post-warmup sample. The 2x comparison
// needs the first sample at runtime, which k6 options cannot express, so it is a
// rate check (heap_within_2x) with a >= 1 threshold. Local only (README §14).
import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Trend } from 'k6/metrics';
import { INGEST_URL, METRICS_URL, parseDevices, positionBody, deviceHeaders } from './lib/config.js';

const devices = parseDevices(open('../../../deploy/.local/devices.json'));
const heap = new Trend('heap_inuse_bytes');
const heapWithin2x = new Rate('heap_within_2x');

const WARMUP_MS = 30000;
const sampleStart = Date.now();
let baseline = null;

export const options = {
  scenarios: {
    burst: {
      executor: 'ramping-arrival-rate',
      startRate: 100,
      timeUnit: '1s',
      preAllocatedVUs: 200,
      maxVUs: 1000,
      stages: [
        { target: 2000, duration: '30s' },
        { target: 2000, duration: '60s' },
        { target: 0, duration: '10s' },
      ],
      exec: 'burst',
    },
    heap: {
      executor: 'constant-vus',
      vus: 1,
      duration: '100s',
      exec: 'sampleHeap',
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
    heap_inuse_bytes: ['max>0'],
    heap_within_2x: ['rate>=1'],
  },
};

export function burst() {
  const dev = devices[__ITER % devices.length];
  const res = http.post(
    INGEST_URL + '/ingest/position',
    JSON.stringify(positionBody(dev, __ITER)),
    deviceHeaders(dev),
  );
  check(res, { 'accepted 204': (r) => r.status === 204 });
}

// sampleHeap reads go_memstats_heap_inuse_bytes from ingest /metrics (GF-329, P22).
export function sampleHeap() {
  if (Date.now() - sampleStart >= WARMUP_MS) {
    const res = http.get(METRICS_URL);
    const line = res.body.split('\n').find((l) => l.startsWith('go_memstats_heap_inuse_bytes '));
    const value = line ? Number(line.split(' ')[1]) : NaN;
    if (baseline === null && !Number.isNaN(value)) {
      baseline = value;
    }
    if (!Number.isNaN(value)) {
      heap.add(value);
      heapWithin2x.add(baseline !== null && value <= 2 * baseline);
    }
  }
  sleep(5);
}
