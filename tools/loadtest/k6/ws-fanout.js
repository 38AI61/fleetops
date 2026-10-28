// WS fanout freshness (GF-337, README §9): freshness = server_ts - device_ts on
// each delivered position, p95 <= 2 s (roadmap §5). Needs the simulator running.
import ws from 'k6/ws';
import { check } from 'k6';
import { Trend } from 'k6/metrics';
import { WS_URL, token, parseDemoUsers } from './lib/config.js';

const users = parseDemoUsers(open('../../../deploy/.local/demo-users.json'));
const freshness = new Trend('freshness_ms');

export const options = {
  scenarios: {
    fanout: {
      executor: 'constant-vus',
      vus: Number(__ENV.VUS || 10),
      duration: __ENV.DURATION || '60s',
    },
  },
  thresholds: {
    freshness_ms: ['p(95)<2000'],
    ws_connecting: ['p(95)<1000'],
    checks: ['rate>0.99'],
  },
};

export default function () {
  const url = WS_URL + '/stream?fleet=' + users.fleet_id;
  const res = ws.connect(url, {}, function (socket) {
    socket.on('open', function () {
      socket.send(JSON.stringify({ type: 'auth', token: token() }));
    });
    socket.on('message', function (msg) {
      const body = JSON.parse(msg);
      if (body.type !== 'position.batch') {
        return;
      }
      for (const p of body.points) {
        freshness.add(Date.parse(p.server_ts) - Date.parse(p.device_ts));
      }
    });
    socket.setTimeout(function () {
      socket.close();
    }, 30000);
  });
  check(res, { 'ws upgrade 101': (r) => r && r.status === 101 });
}
