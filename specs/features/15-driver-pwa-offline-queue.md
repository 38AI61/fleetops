# P15 — Driver PWA offline queue · feature spec plan

Status: approved plan, persisted 2026-10-27. Branch `phase-15-driver-pwa-offline-queue`.

Source plan: `/root/.claude/plans/p15-plan-hazy-finch.md` (approved).

Target file: `specs/features/15-driver-pwa-offline-queue.md` (to be persisted verbatim on branch `phase-15-driver-pwa-offline-queue`). Decisions GF-236…GF-246. Highest existing decision is GF-235.

## Scope

- **Queue for transitions and photos, `idb`.** Driver taps accept, start, pickup and deliver-with-photo while offline are captured to IndexedDB at the tap, FIFO per job, and replayed on reconnect (GF-236, GF-237, GF-238).
- **Cached snapshot for `GET /driver/jobs/today`.** The last successful payload per user is stored; when offline the today list renders from it, labelled as a snapshot (GF-239).
- **409 conflicts surfaced, not dropped.** A rejected replay stays in the queue as a visible conflict with Retry and Discard, and the today list refetches (GF-240).
- Replay is idempotent on the server through P11 same-state 200 and P12 delivered-replay 200, so a reload or double flush never produces a second event (GF-237).
- Phase stays inside `apps/driver-pwa/`. No backend change (GF-236, GF-244).

## Plan

Build order. Each group ends with its tests green.

**G0 — Preflight.**
- Baseline gates green on the phase branch: `make fmt-check`, `make lint`, `make test`; `apps/web` `npm run typecheck`, `npm test`, `npm run build`; `apps/driver-pwa` `npm ci --include=dev`, `npm run typecheck`, `npm test`, `npm run build`.
- Re-confirm the highest decision id is GF-235 (`grep -o 'GF-[0-9]*' specs/features/*.md | sort -t- -k2 -n | tail -1`).
- Install `idb@8.0.4` (runtime) and `fake-indexeddb@6.2.6` (dev), pinned exact, in `apps/driver-pwa/package.json`. Vitest stays 4.1.12, jsdom 30.1.3.
- Confirm the target-state map (see G1) against `internal/jobs/transitions.go` before coding it.
- Tests: none new; the baseline run is the test.

**G1 — Queue core (`src/offline/`).**
- `src/offline/db.ts`: `openQueueDb()` via `openDB('fleetops-driver', 1, …)`. Stores:
  - `actions` — `keyPath: 'seq'`, `autoIncrement: true`. Indexes: `by_job` (`jobId`), `by_key` (`key`), `by_user` (`userId`). A row is `{seq, userId, jobId, action, key: "${jobId}:${action}", status: 'pending'|'in_flight'|'conflict', enqueuedAt, attempts, conflict?: {code, message}, photo?: {bytes: ArrayBuffer, type: string, name: string, lat: number, lng: number}}`.
  - `snapshots` — `keyPath: 'userId'`, value `{payload, fetchedAt}` (GF-239).
- `src/offline/queue.ts` (pure logic over `idb`, no React):
  - `TARGET_STATE = {accept: 'accepted', start: 'en_route', pickup: 'picked_up', deliver: 'delivered'}` (GF-236).
  - `enqueueTransition(userId, job, action)`: runs in one `readwrite` transaction. If a row with the same `key` exists in any status, it returns that row and writes nothing (dedupe, GF-237). Otherwise it adds a `pending` row with `seq` auto-assigned.
  - `enqueueDeliver(userId, job, photo, lat, lng)`: same dedupe on `key` (`${jobId}:deliver`). The photo is stored as `bytes` (ArrayBuffer), `type` and `name`, never as a `Blob` or `File` (GF-236).
  - `nextRunnable(userId)`: reads `actions` by `seq` for that user. A row is runnable when it is `pending` and no earlier row for the same `jobId` is `pending`, `in_flight` or `conflict`. This is FIFO per job, and jobs do not block each other (GF-238).
  - `markInFlight`, `markDone` (delete row), `markConflict(seq, {code, message})`, `retry(seq)` (conflict → pending), `discardJob(userId, jobId)` (deletes every row for that job, GF-240).
  - `resetInFlight(userId)`: on boot, `in_flight` rows go back to `pending`. Safe because the server treats a same-state replay as 200 (GF-237).
- `src/offline/flush.ts`: `flushQueue(userId, send)` takes a lock (`navigator.locks.request('fleetops-driver-queue', …)` when present, else an in-memory mutex), loops `nextRunnable`, calls `send(row)`, and maps the result (GF-238, GF-240):
  - 2xx → `markDone`.
  - 409, 404, 400, 413 → `markConflict` with `code` and a driver message. Flush continues for other jobs.
  - 503 → leave `pending`, stop the pass, retry on the next trigger.
  - 401 or network `TypeError` → leave `pending`, stop the pass.
- Tests (Vitest, `fake-indexeddb`, `src/offline/queue.test.ts`, `src/offline/flush.test.ts`):
  - **FIFO across jobs** (binding): enqueue accept(A), accept(B), start(A) while offline; flush against a fake server; assert calls are `[accept A, accept B, start A]` with per-job order preserved and a blocked job not stalling others.
  - **Replay dedupe** (binding): the fake server counts an event only when state changes. Enqueue the same transition twice (dedupe no-op, one row). Flush, then flush again concurrently and sequentially; assert one server event. Also: a row replayed after `resetInFlight` yields no second event.
  - **Conflict surfacing** (binding): a fake 409 on replay leaves the row as `conflict` with `code`, does not delete it, does not send later rows for that job, and `retry`/`discardJob` behave as specified.
  - Unit: `enqueueDeliver` stores photo bytes and re-creates a `File` with the same bytes, type and name; dedupe on `:deliver`; per-user isolation (`by_user`).
  - Unit: `nextRunnable` ordering across users and jobs; `resetInFlight` only touches `in_flight`.
  - Unit: 503 and 401 leave rows `pending` and stop the pass.

**G2 — Snapshot for today (`src/offline/snapshot.ts`, `src/jobs/queries.ts`).**
- `saveSnapshot(userId, payload)`, `loadSnapshot(userId)` → `{payload, fetchedAt} | null`.
- `useTodayJobs` (`queries.ts`):
  - `networkMode: 'always'` so the query function runs offline and can fall back. This overrides the TanStack default that pauses queries offline (GF-239).
  - `queryFn` fetches today; on success saves the snapshot and returns `{jobs, stale: false, fetchedAt: now}`.
  - On a network `TypeError` it returns the snapshot as `{jobs, stale: true, fetchedAt}`, or rethrows if no snapshot exists.
  - `refetchInterval: 30_000` and refetch-on-focus stay (GF-230).
- Hand-rolled snapshot, not TanStack `persistQueryClient`: that would persist the whole cache, add a dependency outside D-38, and cannot scope to a user cleanly (GF-239).
- Tests:
  - Unit `src/offline/snapshot.test.ts`: save then load; `fetchedAt` set; per-user isolation; load returns null for an unknown user.
  - Unit `src/jobs/queries.test.ts`: online success writes snapshot and returns `stale: false`; `TypeError` with snapshot returns `stale: true`; `TypeError` without snapshot throws; `ApiError` 401 is not swallowed by the fallback.

**G3 — Integration: capture at the tap, replay on reconnect (`src/jobs/queries.ts`, `src/pages/TodayPage.tsx`, `src/pages/DeliverPage.tsx`, `src/offline/replay.ts`).**
- `useJobTransition`: no longer calls the API directly. It calls `enqueueTransition` first (awaiting the IDB transaction, so the tap is captured before any network), then `flushQueue` if online. The button is disabled while the job has a queued row (GF-241).
- `actionsFor` (GF-229) stays the gate: enqueue only if `actionsFor(job.state) === action` at tap time.
- `DeliverPage`: submit enqueues `enqueueDeliver` when `navigator.onLine` is false or when the fetch throws a network `TypeError`; otherwise it sends as today. A queued deliver navigates to `/today` with a "Queued" notice (GF-241).
- `src/offline/replay.ts`: `startReplay(userId, send)` returns a teardown. Triggers: app boot (after `resetInFlight`), `online` event, `visibilitychange` to visible, and a 30 s interval while the queue is non-empty (GF-238). Each pass invalidates the today query on completion.
- Boot: `navigator.storage?.persist?.()` is called best-effort (GF-243).
- Tests:
  - Component `src/pages/TodayPage.test.tsx` (extend): offline tap writes a queue row and shows "Queued"; the button is disabled while queued.
  - Component `src/pages/DeliverPage.test.tsx` (extend): offline submit queues the photo and does not call the API; an online submit still calls the API unchanged.
  - Unit `src/offline/replay.test.ts`: `online` event triggers a flush; the interval is armed only while non-empty; teardown clears listeners and timer.

**G4 — Conflict surfacing (`src/offline/ConflictList.tsx`, `src/pages/TodayPage.tsx`).**
- Header shows "N queued" while pending, "Syncing…" during a pass, and "Offline" when offline.
- Each conflict renders a card: job reference, action, and the message from GF-240 (e.g. "Job changed on the server; not applied." for 409, "Job no longer available." for 404, "Proof was rejected." for 400/413). Actions: **Retry** (`retry(seq)` then flush) and **Discard** (`discardJob` then refetch). Nothing is removed without a driver action.
- Any conflict triggers a today refetch so the list shows server state (GF-240).
- Tests:
  - Component: a conflict row renders its message; Retry sets it pending and calls the replay; Discard removes the job's rows and invalidates the today query.
  - Component: the "N need attention" count matches the conflict rows.

**G5 — SW / reload interaction (GF-226 limitation, GF-242).**
- Keep `registerType: 'autoUpdate'` in `vite.config.ts`. Queued work survives a reload because it lives in IDB and `resetInFlight` runs at boot (GF-242).
- The residual risk is an in-progress deliver form that is not yet queued. It is recorded, not fixed.
- The build artifact check (GF-234) is unchanged.
- Tests: `src/offline/db.test.ts` — opening the DB after a simulated reload with an `in_flight` row resets it to `pending` and it is replayed once; no `vite.config.ts` change is asserted by `npm run build`.

**G6 — CLAUDE.md and full validation.**
- Add a `## Driver PWA offline queue (P15)` block to `CLAUDE.md` (GF-245): the store names, the replay triggers, the conflict states, the snapshot behavior, the `localStorage` device key caveat (unchanged), and the manual follow-up. No env table change.
- Run every gate in Validation. Record the output.

## Requirements

### Carried from existing decisions (cite, do not restate)
- **D-37** testing tools: Vitest, Testing Library, `fake-indexeddb` (tech-stack §9 Offline row).
- **D-38** frontend libraries: `idb`, `vite-plugin-pwa` (Workbox). The React Router, MapLibre and dev-proxy items are unchanged.
- **D-21** and **D-23**: the job state edges and the fail edges. P15 uses only the accept, start, pickup and deliver edges (`internal/jobs/transitions.go`).
- **R-6**: no route is added. Driver routes already exist in the role matrix (P11, P12). Confirmed.
- **R-8**: `README.md` is not edited.
- **Mission §2**: the driver works with bad signal. P15 satisfies this for transitions and deliveries.
- **GF-224…GF-235** (P14): cited, not restated. GF-229 `actionsFor` is the reuse point. GF-226 is the reload limitation. GF-228 is device key storage (unchanged). GF-230 is polling and freshness. GF-231 is the deliver submit path. GF-233 is the testing approach.
- **P11 transition contract**: `POST /driver/jobs/:id/accept|start|pickup` with `{}` or empty body. Same-state replay → 200 with the job object and no event (no UPDATE). A move from a state with no edge, or from cancelled → 409 `{"error":"conflict"}`. Not owned, unlinked, or cross-fleet → 404.
- **P12 deliver contract**: multipart `photo`, `lat`, `lng`. Gate order: role → id → proofs → driver → gate (404 / 409 for any state other than `picked_up` or `delivered`) → body (400 / 413) → upload → tx. Replay of `delivered` → 200 with the job object, no upload, no event, no publish.
- **GF-196** job object shape with `proof_photo_url`.

### New decisions (GF-236 onward)

**GF-236 — Queue storage and schema.** DB `fleetops-driver`, version 1, `idb` 8.0.4. Store `actions` (`keyPath: 'seq'`, autoIncrement) with indexes `by_job`, `by_key` (`${jobId}:${action}`) and `by_user`. Store `snapshots` (`keyPath: 'userId'`). Photos are stored as `ArrayBuffer` plus `type` and `name`, and a `File` is rebuilt at replay. `Blob` is not stored because Blob structured-clone under `fake-indexeddb` in jsdom is unverified. Target-state map `TARGET_STATE` (above) mirrors the D-23 edges. *Why:* `seq` gives a global FIFO, `by_job` gives FIFO per job, and bytes avoid an unverified Blob round-trip.

**GF-237 — Dedupe and idempotency.** Two layers. (a) Client: `enqueue` looks up `by_key` in the same transaction and returns the existing row, so a double tap or a repeated enqueue writes one row. (b) Server: P11 and P12 already treat a replay at the target state as 200 with no event, so a replayed row produces no second event. The server event count is the test oracle. An `in_flight` row reset at boot is safe for the same reason: a row that already applied returns 200 on replay. Because FIFO per job guarantees at most one in-flight row per job, the reset cannot skip an earlier action. No client request id or `Idempotency-Key` is added: the server has none (research: none exist) and same-state 200 is sufficient. *Why:* adding a server idempotency header would be a backend change, which P15 excludes.

**GF-238 — Replay trigger, ordering, concurrency.** Triggers: boot, `online`, `visibilitychange` to visible, and a 30 s interval while the queue is non-empty. Order: global `seq`. A job with any non-done row is blocked for later rows of that job (FIFO per job), but other jobs proceed. A pass runs under a lock: `navigator.locks` when present, else an in-memory mutex. Multiple tabs are covered by the server's idempotency (GF-237), not by the lock. *Why:* `online` alone misses flaky signal; the interval catches a failed pass; FIFO per job keeps the server state machine in order.

**GF-239 — Offline snapshot for today.** The last successful `GET /driver/jobs/today` payload is saved per user in `snapshots` (hand-rolled, not TanStack persist). `useTodayJobs` uses `networkMode: 'always'` so the query runs offline and falls back on network `TypeError` only; an `ApiError` is never masked by the fallback. The UI labels snapshot data as "Offline — as of HH:MM". No time-based staleness threshold is applied: the label is always shown for snapshot data. *Why:* hand-rolled storage is per user and small; the label keeps the driver informed without a threshold the roadmap does not define.

**GF-240 — Conflict surfacing.** Server results on replay: 2xx → done and row deleted. 409, 404, 400 and 413 → `conflict` row kept with `code` and a driver message; later rows for that job are held. 503 and 401 or network failure → `pending`, pass stops (no conflict shown). Driver actions: **Retry** (`pending`) or **Discard** (deletes every row for that job). Any conflict triggers a today refetch. Nothing is dropped without a driver action. *Why:* the roadmap requires conflicts to be visible, and a silent drop would hide a job the driver still owes.

**GF-241 — What is queued, what is not.** Queued: accept, start, pickup, deliver with photo. Captured at the tap (the IDB write is awaited before any network call). `actionsFor` gates the enqueue. Not queued: `fail` (not exposed, GF-229). Login and session refresh need the network to authenticate and are not queued. `POST /ingest/position` is not queued: positions are a stream, a stale fix is useless, and GF-232 already drops it on network error. *Why:* queueing login would store credentials; queueing ingest would replay stale positions.

**GF-242 — SW / reload interaction (GF-226).** Keep `registerType: 'autoUpdate'`. Queued transitions and deliveries survive a reload because they are in IDB, and `resetInFlight` plus GF-237 make the replay safe. The residual risk is an in-progress deliver form that has not yet been queued, which a reload still discards. A `prompt` registration would add a UI that P15 does not otherwise need. *Why:* the queue removes the risk for work already committed; the form is a pre-tap state, so it is recorded as residual, not fixed.

**GF-243 — Storage durability.** `navigator.storage.persist()` is called best-effort at boot. A refusal is not an error. No eviction logic, no size cap beyond the existing 5 MiB photo check. *Why:* a queued photo is the only driver data that exists only on the device.

**GF-244 — Backend scope.** None. The replay uses P11 and P12 endpoints unchanged. The driver principal needs no change (GF-227). If research or implementation finds a need, it is scope-fenced in Validation before any Go or `db/` change.

**GF-245 — CLAUDE.md.** One block `## Driver PWA offline queue (P15)`, mechanical only: store names, triggers, conflict states, snapshot label, the device-key `localStorage` caveat (unchanged from P14), and the manual follow-up. The env table is unchanged. File stays under 200 lines.

**GF-246 — Logout and shared devices.** Logout does not clear the queue; rows are keyed by `userId` and are replayed only for the same user after login. A 401 with a failed refresh still calls `logout()` through the unchanged `apiFetch` (`client.ts`); the pending rows survive for that user. *Why:* changing `apiFetch` would touch P14's auth path. Residual risk: a shared device with two drivers keeps each driver's rows separate by `userId`.

## Validation

Run from the repo root on this host, on branch `phase-15-driver-pwa-offline-queue`. Each item has a pass condition.

1. **Go gates unchanged.** `make fmt-check`, `make lint`, `make test` (needs Docker for PostGIS; if Docker is unavailable, say so and do not claim a pass). Pass: exit 0, zero Go diff.
2. **`apps/web` gates unchanged.** `npm --prefix apps/web ci --include=dev`, `npm --prefix apps/web run typecheck`, `npm --prefix apps/web test`, `npm --prefix apps/web run build`. Pass: all exit 0.
3. **`apps/driver-pwa` gates.** `npm --prefix apps/driver-pwa ci --include=dev`, `npm --prefix apps/driver-pwa run typecheck`, `npm --prefix apps/driver-pwa test`, `npm --prefix apps/driver-pwa run build`. Pass: all exit 0. The build runs the GF-234 artifact check: `dist/manifest.webmanifest`, `dist/sw.js` and `dist/registerSW.js` exist and the index links them.
4. **Binding test names appear and pass.** `npm --prefix apps/driver-pwa test -- --reporter=verbose` shows these names as passed: "FIFO across jobs", "replaying the same queued transition twice produces one server event", "a 409 on replay is surfaced, not dropped". Pass: each name present with ✓ and zero failures.
5. **Scope fence.** `git diff --name-only main...HEAD` contains only paths under `apps/driver-pwa/`, `CLAUDE.md`, `specs/features/15-driver-pwa-offline-queue.md`, `prompts/`, and lockfiles. Pass: the intersection with `README.md`, `deploy/PORTS.md`, `deploy/docker-compose.yml`, `specs/constitution/`, `db/`, `internal/`, `services/`, `tools/`, `apps/web/` is empty.
6. **CLAUDE.md size.** `wc -l CLAUDE.md` < 200.
7. **No secrets.** `git diff main...HEAD | grep -c 'fo_dev_'` is 0 for added lines. Fixtures use no device-key literal. Pass: count is 0.
8. **No new dependency outside D-37/D-38.** `git diff main...HEAD -- apps/driver-pwa/package.json` adds only `idb` and `fake-indexeddb`, exact-pinned.
9. **Manual follow-up (not automated; recorded).** Airplane-mode run against compose plus local API at 10013; reconnect replays in order; conflicts visible. This needs a real browser and network stack. P15 does not start compose. Record the result in the execution record when run.

## Testing depth

| Layer | Tool | Cases | Binding |
|---|---|---|---|
| Queue FIFO | Vitest + `fake-indexeddb` | FIFO across jobs; per-job blocking; global `seq` order | **Yes** (FIFO across jobs) |
| Replay dedupe | Vitest + `fake-indexeddb` | enqueue dedupe; replay twice → one server event; `resetInFlight` replay → no second event | **Yes** (replay twice → one event) |
| Conflict surfacing | Vitest + `fake-indexeddb` | 409 on replay → `conflict` row kept with code; later rows held; retry and discard | **Yes** (409 surfaced) |
| Unit | Vitest | snapshot save and load, staleness flag, per-user isolation; `enqueueDeliver` photo bytes round-trip; `nextRunnable` ordering; `resetInFlight`; 503 and 401 hold | No |
| Component | Testing Library + jsdom | offline tap queues, "Queued" disabled button; snapshot-backed today list with "Offline" label; conflict card, Retry and Discard; replay indicator; DeliverPage offline queue | No |
| Build | `npm run build` | GF-234 artifact check unchanged | No |

## SPEC-AMEND repeat

P15 adds **no new SPEC-AMEND row**. Every endpoint it calls already exists: `POST /driver/jobs/:id/accept|start|pickup` (P11), `POST /driver/jobs/:id/deliver` (P12), `GET /driver/jobs/today` (P11). `idb` and `fake-indexeddb` are pinned by D-38 and D-37. Research confirmed this.

Observation recorded, no amendment: `specs/features/11-driver-jobs.md` and roadmap P11 say repeated transitions are "idempotent" in general. The code is idempotent only for same-state replays; a move from a later state returns 409. P15's dedupe depends on same-state replay only, which matches the code. No spec change is needed for P15.

The GF-192 hub identity binding remains open and belongs to P23.

## Out of scope

- Playwright end-to-end (P16).
- Push notifications (GF-1).
- Geofences (P17 onward).
- Reports (P21).
- Fail UI (GF-229). The endpoint stays unexposed.
- Live `job.updated` over WebSocket (polling stays, GF-230).
- Background Sync and Periodic Background Sync APIs.
- Any backend Go or `db/` change (GF-244). None is planned.
- README, compose, `deploy/PORTS.md` and constitution edits.
- Compose start.
- Merge to `main`, push.
- `apps/web` changes.
- Any P16+ content.
- Maskable icon (GF-226).
