# Feature 23 — Load, tuning & demo (P23)

Status: planned on `phase-23-load-tuning-demo`, not implemented, 2026-10-28. Validation results and deviations are appended at the end when the phase runs.
Source plan: /root/.claude/plans/p23-plan-zippy-donut.md (approved, self-gate passed with two driver amendments: G0 SPEC-AMEND reading pre-resolved — no row owed for the GF-341 envelope field; SPEC-AMEND repeat aligned).

## Scope

P23 is the last roadmap phase. It delivers five k6 scenarios (not executed on this host), a tuning record for the `FLEETOPS_*` tunables (analysis only), a 10k-connection hub race test (executed here), a real `make seed-demo` (demo fleet, 50 vans, 50 paired demo drivers), GF-192 closed, and `docs/demo.md`.

Binding constitution: `specs/constitution/roadmap.md` §1 (R-1, R-2, R-6, R-7, R-8), §3 P23, §5 (P23 gate rows), `specs/constitution/tech-stack.md` §3, §4, §5, §11 (D-22, D-37), `specs/constitution/mission.md` §7 and §51, README §9, §10 phase-5 row, §14 line 271, `CLAUDE.md`, `specs/features/22-observability.md`.

### Baseline facts (verified read-only)

- Branch `phase-23-load-tuning-demo`, clean, based on `a933cd2`. Go pin 1.27.3. `make test` is `go test ./... -race -count=1` and CI runs it.
- `make seed-demo` is a stub (`Makefile:38-39`, exit 1, "lands in P23"). `CLAUDE.md` line 43 documents the stub.
- `internal/seed` has no demo drivers. `RunDemo` (`internal/seed/demo.go:62-100`) creates 50 vehicles `DEMO-%03d`, tracker devices, and pairings, then writes `deploy/.local/devices.json`. `DemoVehicleCount=50` is a constant (`demo.go:16`). `Run` (`seed.go:92-167`) creates the fleet, three alert rules, and one admin. Idempotence: `count(fleets)` check before the password prompt (`seed.go:101-107`), then a `LOCK TABLE fleets` re-check in the insert tx (`seed.go:128-137`). `--demo-devices` runs only on the `Created == true` branch (`seed.go:160-165`).
- CLI `tools/seed/main.go:34-37` flags: `--admin-email` (required), `--fleet-name`, `--timezone`, `--demo-devices`. Password: no-echo TTY prompt, or one stdin line. No flag or env (GF-23).
- `devicesfile.Write` is atomic (temp, fsync, rename), mode 0700 dir (`internal/devicesfile/devicesfile.go:101-140`).
- Hub `/stream` allows `admin`, `manager`, `dispatcher` **and `driver`** (`internal/hub/conn.go:23`). A driver token therefore reaches the GF-192 filter directly, even though the driver PWA has no `/stream` proxy.
- Hub filter: `jobFor` (`internal/hub/subscribe.go:174-177`) compares `conn.subject` (`users.id`, from the JWT, `internal/auth/jwt.go:53`) with envelope `driver_id` (`drivers.id`, `internal/jobs/publish.go:40`). GF-192 is confirmed open (`specs/features/11-driver-jobs.md:116`, `13…:189`, `16…:119`).
- Hub has no Postgres connection (CLAUDE.md, ws-hub entry). The fix must not add one.
- Hub scale test `internal/hub/scale_test.go`: one test `TestThousandSyntheticConnections` (:34), `n=1000` const (:35), real `httptest` server and real `coder/websocket` dials, dial semaphore 64 (:77) but **one goroutine per conn launched up front** (`dialAll`), 60 s total budget (:67), no skip gates. Default room cap 2000 (`hub.go:22`, config range 1–10000). Known flake policy: isolated re-run of `internal/hub` before failing anything (`specs/features/18-geofence-evaluator.md:18`).
- Tunables plumbing (`internal/config/config.go:21-40`, `:127-187`): `FLUSH_INTERVAL`, `FLUSH_MAX`, `ACCURACY_MAX_M` → `internal/ingest/buffer.go`; `PRESENCE_TTL` → `internal/presence`, `internal/mapview`; `WS_ROOM_CAP` → `hub.Options.RoomCap`. All server-side plumbed. Gaps: the map client hardcodes `CLIENT_STALE_MS = 120_000` (`apps/web/src/status/display.ts`, spec 09 line 210), so changing `PRESENCE_TTL` desyncs the UI. `MaxInFlight` (`internal/ingest/stream.go:15-17`) has a stale "tunable in P5" comment and stays a constant.
- Device auth: `POST /ingest/position`, `X-Device-Key` required (`internal/ingest/http.go:36-66`). Key format `^fo_dev_([1-9][0-9]*)_([A-Za-z0-9_-]{43})$` (`internal/devicekey/key.go:23`). Hash `HMAC-SHA256(DEVICE_KEY_SALT, secret)` in `devices.api_key_hash`. Body `lat,lng,speed,heading,accuracy,ts` (`internal/ingest/validate.go:55-61`). `device_ts` = body `ts`; `server_ts` = ingest clock (`stamp.go:42-54`). `deploy/.local/devices.json` (0600) has `version, fleet_id, generated_at, devices[]{device_id, vehicle_id, kind, key, waypoints}`.
- Assign: `POST /jobs/:id/assign` (`internal/jobs/http.go:275-305`), `Store.Assign` (`store.go:385-460`), one tx with `FOR UPDATE` and about six round trips, then synchronous `publishChange` (`publish.go:35-55`). Report: `GET /reports/daily` (`internal/reports/http.go:56-133`) reads one indexed `daily_driver_stats` row set. It never reads `positions`.
- `nearest` candidates require an open pairing to a driver (CLAUDE.md, P13). Demo drivers need pairings to be useful.
- k6 is not installed on this host.

## Plan

Build order. Each group names its tests. Commits are made only on request, so no commit or push is part of the plan.

**G0 — Preflight (no code change).**
- Run `make fmt-check`, `make lint`, `make test` on the branch. Record the baseline in the validation results. Run `npm --prefix apps/web run typecheck` and `npm --prefix apps/driver-pwa run typecheck` as baselines (both untouched).
- Confirm k6 is absent (`command -v k6` empty). Record the standing reason in the spec. Confirm `node` presence (`command -v node`) for the static k6 check (G4).
- Pin the field names the scripts depend on: `positions.batch` element keys (`device_ts`, `server_ts`) in `internal/hub` or `internal/ingest/stream.go`; `/metrics` heap gauge `go_memstats_heap_inuse_bytes` on ingest `:10008`; `/auth/login` request body; email validation rule for `.example` addresses (`internal/users`); re-verify the SPEC-AMEND reading: the constitution pins only the subject `jobs.events.<fleet_id>` (tech-stack §7:211), not the envelope body fields (GF-109 added `driver_id` in P7 with no mission §7 row), so the GF-341 body addition owes no row (see SPEC-AMEND repeat).
- Tests: none (read-only checks, output recorded in the spec's baseline section).

**G1 — `make seed-demo` and demo drivers.** Scope: `internal/seed/**`, `tools/seed/**`, `Makefile`.
- `internal/seed/demo.go`: add `DemoDriverCount = 50` (constant, GF-318). `RunDemo` also creates, for each `i` in 1..50, a `drivers` row `DRV-%03d`, a `users` row with role `driver` and `driver_id` set in the fleet, a random 24-character password that passes the existing policy check before any hash or write, and a pairing `DRV-%03d` to `DEMO-%03d`. Driver rows are created in the same tx as the vehicles, in the existing demo flow after the fleet commit (keeps the first-seed-only rule).
- Passwords and ids are written to `deploy/.local/demo-users.json` (0600, atomic write via the `devicesfile` pattern), fields `version, fleet_id, generated_at, drivers[]{driver_id, user_id, email, password, vehicle_id}`. Passwords are never printed to stdout or stderr and never taken as flags.
- `tools/seed/main.go`: `--demo-devices` now also creates demo drivers and writes `demo-users.json`. The flag keeps its name (no new flag). `DEVICE_KEY_SALT` requirement unchanged.
- `Makefile`: replace the stub at `:38-39` with `seed-demo: ## Seed fleet, admin, 50 demo vans, devices and demo drivers (first seed only)` running `$(GO) run ./tools/seed --demo-devices $(SEED_ARGS)`. Requires `SEED_ARGS="--admin-email <email>"` and `DEVICE_KEY_SALT`. Exit 0 on an already-seeded DB (GF-340).
- Tests (testcontainers via `testinfra.NewMigratedDB`, no mocks, D-37):
  - `TestDemoCreatesFiftyDriversLinkedToUsers`: 50 drivers, each user row has `driver_id` in the fleet, role `driver`.
  - `TestDemoDriversPairedOneToOne`: `DRV-n` paired to `DEMO-n`; `nearest` query returns candidates (pins P13 linkage).
  - `TestDemoUsersFileMode0600AndAtomic`: file mode 0600, fields complete, no password in stdout captured by the test.
  - `TestDemoPasswordsPassPolicy` and `TestDemoPasswordsNotLogged`.
  - `TestDemoRerunIsNoOpExitZero` (idempotence, exit 0, file unchanged, no prompt).
  - Extend `TestDemoDevicesCreatesFiftyPairedDevices`, `TestDemoDevicesRerunIdempotent`, `TestDemoKeysAuthenticate`, `TestDemoRequiresSalt` to still pass with drivers present.
  - `tools/seed/main_test.go`: `TestRunDemoDevicesNeedsSalt` and `TestRunUsageErrorsExitTwo` still pass. Add `TestSeedDemoTargetExitsZeroOnRerun` as a CLI-level run against the testcontainer DB.

**G2 — 10k hub race test.** Scope: `internal/hub/**` (test files only in this group).
- Add test flag `-scale.conns` (default 1000) in `scale_test.go`. Testing flags are not env vars, so no D-22 or SPEC-AMEND impact. Keep `TestThousandSyntheticConnections` as is.
- New `TestScaleConnections` (name pinned): splits N connections across `ceil(N/2000)` fleets, each filled exactly to the production room cap (`RoomCap` left at the default 2000). Dials run from a fixed worker pool of 64 instead of one goroutine per connection, so the live goroutine count stays bounded in the test. Assertions: all dial and auth succeed, one `position.batch` per fleet reaches every client byte-for-byte, total time bounded by the budget, goroutines return to baseline.
- Budget: per-op timeouts raised to 60 s; total budget `N/1000 × 60 s` capped at 20 min; `go test -timeout 30m`.
- Make target `hub-scale: ## Run 10k synthetic WS connections under -race` running `$(GO) test ./internal/hub -race -count=1 -run TestScaleConnections -args -scale.conns=10000 -timeout 30m`.
- Pre-declared fallback (decided now, not later): the Go race detector caps live goroutines at about 8128. A 10k run under `-race` needs roughly 50k server-side goroutines (five per connection, per `conn.go` comments) plus client goroutines, so the cap may abort the process. If it does, record the abort output as a deviation, run `TestScaleConnections` with `-race` at the largest passing N (binary-search in steps of 1000, recorded), and run the 10k case without `-race` for the full-count evidence. The gate row "10k with -race" then stays a recorded deviation, not a silent pass.
- Tests: `TestScaleConnections` itself, plus the existing `TestThousandSyntheticConnections` in `make test`. Flake policy: isolated re-run of `internal/hub` before failing anything.

**G3 — GF-192 closed (hub identity binding, publish-side).** Scope: `internal/jobs/**`, `internal/hub/**`.
- `internal/jobs/publish.go`: the `jobs.events.<fleet_id>` envelope gains `driver_user_id` (the `users.id` of the user linked to the envelope's `driver_id`, JSON `null` when unlinked). Computed after commit in `publishChange` with one fleet-scoped query `SELECT id FROM users WHERE driver_id=$1 AND fleet_id=$2 ORDER BY id LIMIT 1`. `driver_id` stays in the envelope (additive change).
- `internal/hub/subscribe.go` `jobFor`: a driver receives a `job.updated` only when `conn.subject == envelope.driver_user_id`. An absent or null `driver_user_id` delivers nothing to a driver. Non-driver roles unchanged.
- Hub stays Postgres-free. The mapping lives in the publisher.
- No migration (`users.driver_id` exists). No auth change (JWT claims unchanged). No role-matrix change (driver stays allowed on `/stream`; the filter narrows only).
- Tests:
  - `internal/jobs`: `TestPublishEnvelopeCarriesLinkedUserID` (testcontainers Postgres plus real NATS, linked and unlinked drivers).
  - `internal/hub`: `TestDriverReceivesOwnJobUpdate` (driver A gets A's update, GF-192 positive case).
  - `internal/hub`: `TestDriverDoesNotReceiveOtherDriverJobUpdate`, built so driver B's `users.id` equals driver A's `drivers.id` (the cross-id-space case the old filter leaked).
  - `internal/hub`: `TestUnlinkedDriverReceivesNoJobUpdate` (null mapping).
  - `internal/hub`: `TestDispatcherStillReceivesAllJobUpdates` (non-driver unchanged).
  - Existing `/stream` filter tests stay green.

**G4 — k6 scripts, Makefile entry, static validation.** Scope: `tools/loadtest/**`, `Makefile`.
- Layout: `tools/loadtest/k6/lib/config.js` (BASE_URL and WS_URL defaults, token and device loader), `tools/loadtest/k6/ws-fanout.js`, `assign.js`, `report.js`, `ingest-steady.js`, `ingest-burst.js`. Each script has its own `export const options` with a `thresholds` block. Scenario parameters use k6's own `-e NAME=value` (k6 parameters, not FleetOps env vars; see GF-337).
- Inputs: `TOKEN` (admin access JWT, `-e TOKEN=…`, minted by `POST /auth/login` as documented in `docs/demo.md`). Device keys come from `deploy/.local/devices.json` via k6 `open()` at `../../../deploy/.local/devices.json` relative to the script. Driver and vehicle ids come from `deploy/.local/demo-users.json`.
- Thresholds (encoded from README §9 and roadmap §5):
  - `assign.js`: `http_req_duration{name:assign}` `p(95)<300`, `http_req_failed` `rate<0.01`.
  - `report.js`: `http_req_duration{name:report}` `p(95)<300`, `http_req_failed` `rate<0.01`.
  - `ws-fanout.js`: custom Trend `freshness_ms` = `server_ts − device_ts` on each delivered position, `p(95)<2000`. Also `ws_connecting` `p(95)<1000` and `checks` `rate>0.99`.
  - `ingest-steady.js`: `constant-arrival-rate` at 100 iterations/s, `http_req_failed` `rate<0.001`, `http_req_duration` `p(95)<300`, `dropped_iterations` `count==0`.
  - `ingest-burst.js`: `ramping-arrival-rate` to 2000/s (10×), `http_req_failed` `rate<0.01`. Memory check: scrape ingest `/metrics` (`127.0.0.1:10008`) every 5 s for `go_memstats_heap_inuse_bytes`. Custom Trend `heap_inuse_bytes`, threshold: max over the post-warmup window ≤ 2× the first post-warmup sample.
- Results: stdout summary plus `--summary-export deploy/.local/loadtest/<script>.json` (gitignored).
- Makefile: `K6 ?= k6` (overridable in `deploy/.local/tools.mk`, the same pattern as `GO`). Targets, each `## ` annotated: `loadtest` (all five in order), `loadtest-ws`, `loadtest-assign`, `loadtest-report`, `loadtest-ingest-steady`, `loadtest-ingest-burst`, `loadtest-static`. Static is `$(GO) test ./tools/loadtest/... -count=1`.
- Static validation test `tools/loadtest/k6_static_test.go`:
  - Each `.js` file exists and contains `export const options`, a `thresholds` block, and the exact threshold expressions listed above for its scenario.
  - Brace, bracket and paren balance.
  - If `node` is on `PATH`: copy each script to a temp dir as `.mjs` and run `node --check`. Skips with a logged reason when `node` is absent. G0 records node presence.
  - Test name pins: `TestK6ScriptsHaveThresholds`, `TestK6ScriptsBalanced`, `TestK6ScriptsParseWithNode`.
- k6 is NOT RUN. No k6 binary exists on the host, so no k6 run is part of validation.

**G5 — GF-192 is closed in G3; no separate task.** The disposition is recorded in the spec with the decision and the test list.

**G6 — Tuning record, `docs/demo.md`, CLAUDE.md rows, full validation.**
- Tuning record (spec section, no code change). See Requirements, GF-342 table.
- `docs/demo.md`: 5-minute script in the order mission §51 / README §10 lists. Steps: login (`/auth/login` on 10010, web on 10012); 50 vans move (`make seed-demo`, then `go run ./tools/simulator --fleet=50 --rate=5s --api http://127.0.0.1:10010`); draw depot geofence on `/zones`; create and assign nearest on `/jobs/new`; driver accepts and advances in the driver PWA (`http://127.0.0.1:10013`, demo driver creds from `deploy/.local/demo-users.json`); photo appears (`proof_photo_url`); device-silent alert (stop the simulator for `threshold_s`, default 600, on the bell); daily report on `/reports`. Commands cite CLAUDE.md, never restate ports or env names.
- Nightly wiring is documentation only (GF-344): a crontab line in `docs/demo.md`.
- CLAUDE.md: replace line 43 (`make seed-demo` stub) with the real semantics. Add a compact "Load and demo (P23)" block with the `loadtest*` targets, `hub-scale`, `K6` override, and the `deploy/.local/demo-users.json` and `deploy/.local/loadtest/` paths. The file must stay under 200 lines (193 today), so the block is at most 5 lines and the Make list is condensed if needed.
- Full validation run (Validation section, checks a–j).

## Requirements

### Binding decisions carried verbatim

**D-22 (tunables):** Optional tunables (`FLEETOPS_*`) come from the environment. Unset or empty means the default. `config.Load` refuses an invalid value, and the error names the variable, never the value. Ranges: `FLEETOPS_FLUSH_INTERVAL` default `1s`, `100ms`–`10s`; `FLEETOPS_FLUSH_MAX` default `500`, `1`–`5000`; `FLEETOPS_ACCURACY_MAX_M` default `50`, finite and above `0` and at most `10000`; `FLEETOPS_WS_ROOM_CAP` default `2000`, `1`–`10000`; `FLEETOPS_PRESENCE_TTL` default `120s`, `30s`–`600s`. P23 is the tuning consumer.

**D-37 (test stack):** k6 for load, rapid property tests, testcontainers, no mocks of infra. k6 scripts pinned as separate JS in `tools/loadtest/k6/`.

**tech-stack §3/§4/§5:** k6 pinned as separate JS scripts in `tools/loadtest/k6/`. `FLEETOPS_*` variables as in D-22.

**Roadmap §1:** R-1/R-2 smallest MVP; R-6 role matrix; R-7 one migration per phase only if a schema element demands it (P23 demands none); R-8 README is never edited.

**Roadmap §3 P23 scope (verbatim):** k6 scripts in `tools/loadtest/k6/`; tuning pass with `FLEETOPS_*` tunables; 10k WS race run; `make seed-demo`; `docs/demo.md`. Each is covered by a G-group above.

**Roadmap §5 gate rows naming P23:** ingest steady 100 msg/s; ingest burst 2,000 msg/s with no unbounded memory; freshness p95 ≤ 2 s from simulator timestamps; API latency assign and report p95 ≤ 300 ms; WS memory and conns 10k with `-race`, room cap 2,000; testing depth k6 only.

**README §9 targets:** API p95 ≤ 300 ms; freshness p95 ≤ 2 s. **README §10 phase-5:** k6 at 10× target, tune batching and flush, demo dataset plus 5-minute demo script. **README §14 line 271:** the 2,000 msg/s burst stays a local k6 exercise (no CI).

**Mission §7:** SPEC-AMEND ledger, see SPEC-AMEND repeat. **Mission §51:** demo script bullet, completed in P23, `docs/demo.md` (D-39).

**GF-16:** tool pins (k6 and Go pinned in Makefile or CI as the constitution requires; k6 pin recorded as `K6` with the version pin noted in the spec, not installed). **GF-208:** MinIO root override precedent. Not used: seed-demo tests do not need MinIO (no proof objects are created). **GF-247:** e2e stack pattern (`e2e/scripts/stack.sh`), reference only. `e2e/` is untouched. **GF-318:** constants are not env vars (`DemoDriverCount`, room budget, thresholds stay in code or script). **GF-332:** Prometheus and Grafana on host networking; read during tuning, no config change.

**CLAUDE.md current state:** 193 lines, line 43 is the `make seed-demo` stub, the file must stay under 200 lines.

**Reused GF decisions (cite, do not restate):** GF-135, GF-136, GF-138 (demo devices and their file), GF-23 (password from TTY or stdin only), GF-116 and GF-109 (hub `notDriver` and `jobFor` filters), GF-335 (assign span), GF-301 (registry conventions), GF-30 (salt validation before DB work), GF-58 (fleet and device from key, not body).

### New decisions (GF-337 onward)

**GF-337 — k6 script shape.** One file per scenario (`ws-fanout.js`, `assign.js`, `report.js`, `ingest-steady.js`, `ingest-burst.js`), each with its own `options` and `thresholds`. Shared helpers in `lib/`. Rationale: a single multi-scenario file hides which threshold belongs to which gate row, and per-file scripts let the nightly run one scenario. WS fanout: connect `WS /stream?fleet=<id>`, send `{"type":"auth","token":TOKEN}` as the first frame (per CLAUDE.md), subscribe, and measure freshness per delivered position. Ingest scripts: device key from `devices.json`, `X-Device-Key` header only, never the body or `Authorization` (GF-52, GF-64). Parameters use k6's own `-e NAME=value`. Rationale: these are k6 run parameters, not FleetOps environment variables, so no D-22 row or SPEC-AMEND row is owed. Results go to stdout and `deploy/.local/loadtest/` (gitignored).

**GF-338 — Loadtest entry point and seed-demo semantics.** Targets: `loadtest` and five per-script targets, plus `loadtest-static` and `hub-scale`, all `## `-annotated. `K6 ?= k6` is overridable in `deploy/.local/tools.mk`. Rationale: same override pattern as `GO` and `MIGRATE`. `make seed-demo` = `make seed` with `--demo-devices` (which now includes demo drivers). It requires `SEED_ARGS="--admin-email …"` and `DEVICE_KEY_SALT`. Demo data is first-seed only (the existing `Created == true` rule is kept, GF-338a). On an already-seeded DB it exits 0 with "already seeded; no changes" and does not touch devices or demo-users files. Rationale: device keys are stored only as HMAC, so existing devices cannot be re-emitted. Re-demo means a fresh database.

**GF-339 — Demo drivers and linkage.** 50 drivers `DRV-001`…`DRV-050`, each a `drivers` row, a `users` row with role `driver` and `driver_id` set, and a pairing to `DEMO-001`…`DEMO-050` (one van each). Emails `demo-driver-001@example.com` style (reserved domain; G0 confirms validator). Passwords are random 24-character values that pass policy, written only to `deploy/.local/demo-users.json` (0600). Never printed, never a flag (GF-23). Rationale: `nearest` candidates need an open pairing to a driver (P13), and the PWA walkthrough needs a driver login. One pairing per van matches the 50-van demo. Using one shared driver password was rejected: it needs a second prompt and makes the demo driver login a shared secret.

**GF-340 — `make seed-demo` idempotence.** Exit 0 on re-run against any already-seeded DB (existing `seed.go:101-107` path). Exit 2 on usage errors (missing `SEED_ARGS`, missing or short salt). Rationale: matches `make seed` contract in CLAUDE.md and the roadmap idempotence rule.

**GF-341 — GF-192 closed.** Mechanism: the publisher resolves the linked `users.id` and adds `driver_user_id` to the `jobs.events` envelope. The hub compares `conn.subject` to `driver_user_id`. Unlinked means nothing delivered to a driver. Rationale: the hub has no Postgres connection (CLAUDE.md), so it cannot map ids itself, and a JWT `driver_id` claim would need an auth change plus token reissue for existing sessions. Publish-side mapping needs no migration, no auth change, and no role change. Driver tokens already reach `/stream` (`conn.go:23`), so the leak is reachable and worth closing even without the PWA proxy. The envelope change is additive. G0 checks whether any consumer rejects unknown fields.

**GF-342 — Tuning record (analysis only, no env change).** Analysis-only because k6 does not run on this host. No default changes; `internal/config` is not touched. Per tunable:

| Tunable | Default | Tuned value | Rationale |
|---|---|---|---|
| `FLEETOPS_FLUSH_INTERVAL` | `1s` | `1s` (kept) | Freshness p95 ≤ 2 s. A 1 s flush leaves about 1 s for ingest, network and client. Going to `100ms` raises write rate 10× at 100 msg/s for no gain in the gate. |
| `FLEETOPS_FLUSH_MAX` | `500` | `500` (kept) | At 100 msg/s, a 1 s interval gives about 100 rows per flush, so the interval triggers. At 2,000 msg/s, the max triggers about 4 times a second, so each flush is 500 rows, about 2,000 rows per second. Raising to `5000` lengthens each flush and increases memory per flush, which the burst gate forbids. |
| `FLEETOPS_ACCURACY_MAX_M` | `50` | `50` (kept) | Simulator accuracy is `5.0` (`internal/simulator/run.go:27-28`), well below 50. Simulator never reads the variable, so setting it below 5 would silently drop simulated fixes. Documented as a known risk. |
| `FLEETOPS_WS_ROOM_CAP` | `2000` | `2000` (kept) | 10k connections across 5 rooms of exactly 2,000 (G2). Cap bounds per-room memory. |
| `FLEETOPS_PRESENCE_TTL` | `120s` | `120s` (kept) | The UI hardcodes `CLIENT_STALE_MS = 120_000` (`apps/web/src/status/display.ts`). Changing the server TTL without a web change desyncs the map. Accepted limitation, recorded here, fixed in no phase (apps untouched). |

Without a runnable k6 the tuning is analysis-only. Tuned values are not claimed as measured. Re-tuning needs a k6 run on a host with k6 installed.

**GF-343 — 10k race test mechanics.** Test name `TestScaleConnections`, test flag `-scale.conns`, targets `hub-scale`. Flags and not env vars, so no D-22 row. Rooms filled to the production cap of 2000. Worker-pool dials (64). Pre-declared fallback for the TSan goroutine ceiling, as in G2. Rationale: the ceiling is a runtime fact, so the fallback is decided in advance rather than improvised after an abort.

**GF-344 — Nightly is documentation, not CI.** `docs/demo.md` carries one crontab line running `make loadtest` into `deploy/.local/loadtest/nightly.log` on a host with k6 and a running stack. No `.github/` change. Rationale: roadmap says "on demand and nightly, not per PR", and `.github/` is out of scope.

**GF-345 — Seed-demo tests are testcontainers-only.** No mocks (D-37). Idempotence and exit codes are asserted against a real DB. Rationale: the demo path writes to Postgres and a file, and mock tests would not catch the constraint behavior.

**GF-346 — Static k6 validation is Go-only plus optional node.** The Go test checks thresholds, balance and, when `node` exists, syntax (`node --check` on a `.mjs` copy). Rationale: no JS toolchain is pinned for the repo, and k6 is the execution authority. Limitation: without node, syntax is not checked; G0 records node presence so the spec states which case ran.

## Validation

Runnable checks on this host with pass conditions:

- **(a)** `make fmt-check`, `make lint`, `make test` green on the phase branch, including `internal/seed`, `internal/hub`, `internal/jobs`, `tools/loadtest`. Web and driver-pwa typecheck baselines recorded and unchanged.
- **(b)** Scope fence: `git diff --name-only main...HEAD` touches only `tools/loadtest/**`, `internal/seed/**`, `tools/seed/**`, `internal/hub/**`, `internal/jobs/**`, `Makefile`, `docs/demo.md`, `CLAUDE.md`, `specs/features/23-load-tuning-demo.md`, `prompts/**`, `.gitignore` (only if `deploy/.local/loadtest/` needs ignoring, which it does not, since `deploy/.local/` is already ignored). Must NOT touch `README.md`, `e2e/**`, `apps/**`, `db/migrations/**`, `specs/constitution/**`, `.github/**`, `deploy/docker-compose.yml`, `internal/config/**`, `internal/auth/**`.
- **(c)** No secrets in the diff: `git diff main...HEAD` grep for `fo_dev_`, `password`, `JWT_SECRET` values, and `TOKEN=` literals returns nothing except doc placeholders.
- **(d)** k6 NOT RUN: standing reason is the k6 binary is absent on this host (G0 output). Scripts validated statically via `make loadtest-static` (Go test, plus node check if node is present).
- **(e)** `wc -l CLAUDE.md` < 200.
- **(f)** `make seed-demo` exits 0 on a seeded-with-demo DB and is idempotent on second run. Proven by testcontainer tests (`TestDemoRerunIsNoOpExitZero`, `TestSeedDemoTargetExitsZeroOnRerun`), not against a live stack.
- **(g)** 10k race run recorded with output and runtime (`make hub-scale`). If the fallback in G2 triggers, the abort output and the N-search results are recorded instead.
- **(h)** `docs/demo.md` commands cross-checked by grep against CLAUDE.md ports (10010, 10011, 10012, 10013, 10008), make targets, and env names. Every target named in the doc exists in the Makefile.
- **(i)** Role matrix: no new authenticated route. GF-341 narrows the driver filter only. Driver still allowed on `/stream`.
- **(j)** Every k6 script contains a `thresholds` block matching its §5 row (`TestK6ScriptsHaveThresholds`).

## Testing depth

| Layer | What | Where | Executed here |
|---|---|---|---|
| Unit | seed-demo pure helpers (policy, file writer, mode 0600), threshold static tests | `internal/seed`, `tools/loadtest` | yes |
| Integration (testcontainers, no mocks) | seed-demo DB linkage, pairing, idempotence; envelope `driver_user_id` against real Postgres and NATS | `internal/seed`, `tools/seed`, `internal/jobs` | yes |
| Race | hub 10k (`TestScaleConnections`) plus existing 1k, with `-race`; GF-341 filter tests | `internal/hub` | yes (fallback per GF-343) |
| Static | k6 script thresholds, balance, optional node parse | `tools/loadtest` | yes |
| Load | k6 scenarios (WS fanout, assign, report, ingest steady, ingest burst) | `tools/loadtest/k6/` | **NO** — k6 binary absent (deliverables only) |
| Existing gates | Go suite; web and driver-pwa typecheck unchanged | repo-wide | yes (baselines in G0) |

Flake policy: an `internal/hub` failure is re-run in isolation before it fails the gate (the known `TestHubRelaysNATSJobAndAlert` flake, `specs/features/17-geofence-registry-drawing.md:269`).

## SPEC-AMEND repeat

- **New SPEC-AMEND row: none expected.** D-22 already registers the tunables. Tech-stack §3 and §4 already name k6 and `tools/loadtest/k6/`. No new service env var (k6 `-e` parameters are k6 run arguments; test flags are Go test arguments). No schema element (no migration).
- **Conditional row:** the `jobs.events` envelope gains `driver_user_id` (GF-341). Resolved in the plan: the constitution pins only the subject `jobs.events.<fleet_id>` (tech-stack §7:211), never the envelope body fields (GF-109 added `driver_id` in P7 with no mission §7 row), so **no row is owed**; G0 re-verifies this reading and the spec records the result.
- **If the GF-343 fallback triggers:** no SPEC-AMEND row (it is a test-runtime fact, recorded as a deviation).
- **GF-192 state as decided:** **closed in P23** by GF-341 (publish-side mapping, tests in G3).

## Out of scope

- Real k6 execution on this host (k6 absent; scripts are deliverables).
- Cloud deployment profile (D-41).
- README edits (R-8).
- `.github/` changes, including any nightly CI workflow (GF-344 documentation only).
- Compose changes (`deploy/docker-compose.yml`).
- Frontend changes (`apps/web`, `apps/driver-pwa`) and `e2e/`, including the `CLIENT_STALE_MS` desync (recorded as an accepted limitation in GF-342).
- New migrations, new env vars, auth or JWT claim changes, role-matrix changes.
- Changes to `internal/config` (no tuning change, GF-342).
- Push and commit (done only on request).
- Any content beyond roadmap P23.
