// Cloud smoke (P24, GF-355 G8). Run by `make cloud-smoke` against a running
// cloud container. It checks /healthz, login, /map/vehicles, then runs the
// simulator through the container's ingest and reads position frames back over
// WS /stream. It prints one result line and never prints a token or password.
import { spawn } from 'node:child_process';
import { readFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { setTimeout as sleep } from 'node:timers/promises';

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const BASE = (process.env.SMOKE_BASE || 'http://127.0.0.1:10019').replace(/\/+$/, '');
const EMAIL = process.env.SMOKE_ADMIN_EMAIL || '';
const PASSWORD = process.env.SMOKE_PASSWORD || '';
const GO = process.env.GO || 'go';
const HEALTHZ_WAIT_MS = 120_000;
const SIM_RUN_MS = 90_000;

const result = { healthz: 'fail', login: 'fail', map: 'fail', frames: 0, vehicles: 0 };

function note(msg) {
  console.error(`smoke: ${msg}`);
}

async function waitHealthz() {
  const deadline = Date.now() + HEALTHZ_WAIT_MS;
  while (Date.now() < deadline) {
    try {
      const res = await fetch(`${BASE}/healthz`);
      if (res.status === 200) return true;
    } catch {
      // the container is still booting
    }
    await sleep(2000);
  }
  return false;
}

async function login() {
  const res = await fetch(`${BASE}/auth/login`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ email: EMAIL, password: PASSWORD }),
  });
  if (res.status !== 200) return null;
  const body = await res.json();
  return typeof body.access_token === 'string' ? body.access_token : null;
}

async function mapVehicles(token) {
  const res = await fetch(`${BASE}/map/vehicles?bbox=-180,-90,180,90`, {
    headers: { authorization: `Bearer ${token}` },
  });
  if (res.status !== 200) return null;
  const body = await res.json();
  return Array.isArray(body.vehicles) ? body.vehicles : null;
}

// fleetId reads the fleet from the demo devices file the seed wrote (GF-136).
async function fleetId() {
  const file = JSON.parse(await readFile(path.join(ROOT, 'deploy/.local/devices.json'), 'utf8'));
  return file.fleet_id;
}

// runSimulator starts the simulator against the smoke base and returns a stop
// function. The child's output is discarded so no device key is printed.
function runSimulator() {
  const sim = spawn(GO, ['run', './tools/simulator', '--fleet=50', '--rate=5s', '--api', BASE], {
    cwd: ROOT,
    stdio: 'ignore',
  });
  sim.on('error', () => {});
  return () => sim.kill('SIGTERM');
}

// streamFrames opens WS /stream for the fleet, authenticates, and counts
// position.batch frames until the returned stop function is called.
async function streamFrames(token, fleet) {
  const ws = new WebSocket(`${BASE.replace(/^http/, 'ws')}/stream?fleet=${fleet}`);
  let frames = 0;
  ws.onmessage = (ev) => {
    try {
      if (JSON.parse(String(ev.data)).type === 'position.batch') frames++;
    } catch {
      // non-JSON frames are not counted
    }
  };
  await new Promise((resolve, reject) => {
    ws.onopen = () => {
      ws.send(JSON.stringify({ type: 'auth', token }));
      resolve();
    };
    ws.onerror = () => reject(new Error('websocket connect failed'));
  });
  return () => {
    ws.close();
    return frames;
  };
}

async function main() {
  if (!EMAIL || !PASSWORD) {
    note('SMOKE_ADMIN_EMAIL and SMOKE_PASSWORD must be set');
    return;
  }

  result.healthz = (await waitHealthz()) ? 'ok' : 'fail';
  if (result.healthz !== 'ok') {
    note('/healthz did not return 200 within 120 s');
    return;
  }

  const token = await login();
  result.login = token ? 'ok' : 'fail';
  if (!token) {
    note('login did not return an access token');
    return;
  }

  const before = await mapVehicles(token);
  result.map = before ? 'ok' : 'fail';
  if (!before) {
    note('/map/vehicles did not return 200 with a vehicles array');
    return;
  }

  const fleet = await fleetId();
  const stopSim = runSimulator();
  let stopWS = null;
  try {
    stopWS = await streamFrames(token, fleet);
    await sleep(SIM_RUN_MS);
  } catch (err) {
    note(err.message);
  } finally {
    stopSim();
    if (stopWS) result.frames = stopWS();
  }

  const after = await mapVehicles(token);
  result.vehicles = after ? after.length : 0;
}

try {
  await main();
} catch (err) {
  note(`unexpected error: ${err.name}`);
}

const pass = result.healthz === 'ok' && result.login === 'ok' && result.map === 'ok' &&
  result.frames >= 1 && result.vehicles >= 1;
console.log(
  `healthz=${result.healthz} login=${result.login} map=${result.map} ` +
  `ws_position_frames=${result.frames} vehicles=${result.vehicles} result=${pass ? 'PASS' : 'FAIL'}`,
);
process.exitCode = pass ? 0 : 1;
