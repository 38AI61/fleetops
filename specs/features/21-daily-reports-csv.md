# Feature 21 — Daily reports & CSV (P21)

Status: planned on `phase-21-daily-reports-csv`, not implemented, 2026-10-28. Validation results and deviations are appended at the end when the phase runs.
Source plan: /root/.claude/plans/p21-plan-calm-chipmunk.md (approved).

## Scope

Roadmap P21 (`specs/constitution/roadmap.md` §3) delivers:

- Migration `012`: the `daily_driver_stats` rollup table (tech-stack §6 row, D-32).
- A rollup worker in `services/ingest` that recomputes today every 15 min and yesterday once after midnight, per fleet timezone (D-32). Idempotent.
- The D-32 metric formulas: distance, idle, on-time, jobs completed, with driver attribution through `vehicle_pairings` active at sample time.
- `GET /reports/daily?date=&driver=` as JSON, and `?format=csv` as a streamed export. JWT, role matrix per README §8.
- One report page in `apps/web` (`/reports`) with a Vitest component test. This lifts the P20 "web untouched" default for this page only.

Out of scope: dashboards and `/metrics` export (P22), k6 and demo seed (P23), multi-day or range aggregation, driver scoring (backlog), email changes, simulator (D-12), driver-pwa, README (R-8), compose, CI, any schema beyond `012`.

### Baseline facts (verified read-only)

- Branch `phase-21-daily-reports-csv`, base `main` (HEAD `2f7f31d`). P20 merged (`6b61c08`). Working tree clean.
- Highest decision ID in `specs/` is GF-318. Next free is GF-319.
- Highest migration is `011_alerts_core`. P21 adds `012`.
- `positions` (`db/migrations/007_positions_vehicles_latest.up.sql:1-12`): `msg_id`, `fleet_id`, `vehicle_id`, `geom geography(Point,4326)`, `speed_kmh REAL NOT NULL`, `heading_deg`, `accuracy_m`, `device_ts`, `server_ts`. PK `(server_ts, msg_id)`, `PARTITION BY RANGE (server_ts)`. Monthly UTC child partitions are created at runtime (`internal/ingest/partitions.go:41-43, 106`).
- `vehicles_latest` (`007`, lines 14-24): last position per vehicle. Not used by the rollup.
- `vehicle_pairings` (`db/migrations/006_vehicle_pairings.up.sql:1-13`): `fleet_id, vehicle_id, driver_id NULL, device_id, started_at, ended_at NULL`. Partial unique on open rows only. No time-based "active at T" query exists today. The existing queries use `ended_at IS NULL` (`internal/dispatch/store.go:24`, `internal/registry/pairing.go:217, 229`).
- `jobs` (`db/migrations/009_jobs_core.up.sql:1-27`): `state` CHECK vocabulary includes `'delivered'` (line 20). `delivered_at TIMESTAMPTZ NULL`, `window_start`, `window_end`, `driver_id`, `fleet_id`. Delivery write at `internal/jobs/driver.go:201-202`. `jobs_window_order` CHECK (line 22). Indexes `(fleet_id, state)` and `(fleet_id, window_start)`.
- `fleets.timezone TEXT NOT NULL DEFAULT 'UTC'` (`db/migrations/004_fleets_timezone.up.sql:1`). Validated with `time.LoadLocation` at seed (`internal/seed/seed.go:67-68`). This is the GF-14 column.
- `drivers` (`db/migrations/005_fleet_registry.up.sql:12-18`): `id BIGSERIAL`, `fleet_id`, `name TEXT NOT NULL`.
- Retention: `retainMonths = 12` (`internal/ingest/partitions.go:21`). Rollup reads today and yesterday only, so it never touches a dropped partition.
- Ingest loops in `services/ingest/main.go`: presence sweeper (`:93-103`, 15 s), alert evaluator (`:106-116`, 30 s, GF-291), ETA loop (`:119-129`, 30 s, GF-306), maintenance (`:132-141`, 24 h). Each uses its own `ctx`/`done` pair and is joined LIFO on shutdown.
- Advisory lock keys in use: `0x464c5450` "FLTP" (partition ensure, blocking, `partitions.go:18`), `0x46_41_4c_45` "FALE" (alerts, `internal/alerts/evaluator.go:18`), `0x45_54_41_4c` "ETAL" (ETA, `internal/eta/loop.go:20`). The new key `0x52_4f_4c_4c` "ROLL" is unused.
- Loop shape to copy: `internal/alerts/evaluator.go:45-58` (`Run`, ticker, `select` on ctx) and `:79` (`Pass`, tx, `pg_try_advisory_xact_lock`, early return when not locked). The ETA loop is the same shape (`internal/eta/loop.go:39-52, 75, 82-89`).
- Metrics: `NewMetrics(reg)` with pre-seeded label sets, registered in a private registry and not exposed until P22 (`internal/ingest/metrics.go:27, 49, 118-140`; `internal/eta/metrics.go:29`).
- API: `Register(e, authn)` per package (`internal/alerts/http.go:40-48`). `auth.RequireRole` (`internal/auth/middleware.go:56-71`). Error envelope `{"error":"<code>"}` via `internal/apierr/apierr.go:32-34`. Codes at lines 10-27. Driver self-resolution through `users.driver_id` in `internal/jobs/driver.go:13-28`.
- No CSV or streaming response exists anywhere in the repo. The reports endpoint is the first.
- Web: react-router in `apps/web/src/App.tsx` (routes `/zones` at `:56`). Data fetching is react-query (`alerts/AlertBell.tsx:17-35`, `geofences/api.ts:45`). API client `apps/web/src/api/client.ts:24-43` (`apiFetch`, `getJson`, `ApiError`). Tests are vitest with `@testing-library/react`, co-located (`geofences/ZonesPage.test.tsx`, `alerts/AlertBell.test.tsx`). Vite dev proxy in `apps/web/vite.config.ts`.
- README §8 role row (line 176): Reports = Admin ✅, Manager ✅, Dispatcher "own fleet", Driver "own stats". See GF-324 for the dispatcher reading.

## Plan

Build order. Each group names its tests.

**G0 — Preflight.** No code.
- Run baseline gates on the phase branch before any edit: `make fmt-check`, `make lint`, `make test` (Go suite; if `internal/hub` flakes, re-run that package in isolation before failing anything, per policy). `npm --prefix apps/web ci --include=dev`, then `typecheck`, `test`, `build`. Record counts. `apps/driver-pwa` baseline: `test` and `build`, recorded only (the app is not touched).
- Confirm the pins above (migration `011` highest, GF-318 highest, columns, `retainMonths = 12`). Confirm the `apierr` codes include a 404 code for "not found" and a 400 code for `validation_failed`. Confirm the migration round-trip test harness used by `001`–`011`.
- Test: none. Output: baseline numbers appended to Validation results.

**G1 — Migration `012` `daily_driver_stats`.** Files `db/migrations/012_daily_driver_stats.up.sql` and `.down.sql`. Shape per GF-319.
- Tests: migration round-trip (up, down, up) on the testinfra PostGIS image (GF-12). Assert the column list, PK, CHECK constraints, and index exist after `up`, and the table is gone after `down`.

**G2 — Rollup worker (`internal/reports`, rollup + pass loop, ingest wiring).**
- `internal/reports/rollup.go`: `RollupDay(ctx, tx, fleetID, tz, day)`. Computes one fleet-day in SQL and writes it with the idempotent write described in GF-323.
- `internal/reports/pass.go`: `Run(ctx, interval)` with a 15-min ticker, the `ROLL` lock, the boot pass, and the post-midnight window (GF-320).
- `internal/reports/metrics.go`: two series, pre-seeded, private registry (GF-325).
- `services/ingest/main.go`: one goroutine beside the ETA loop, using the `reportsCtx`/`reportsDone` pattern. Teardown LIFO like the others.
- Tests:
  - Golden fixture (integration, testinfra PostGIS): distance, idle, on-time %, jobs completed, exact per the Testing depth table. Includes a DST day in `America/New_York` (23 h local day), a gap over 5 min, a stationary leg, a driverless pairing, and a jobs-only driver.
  - Attribution: a mid-day pairing change (vehicle moves from D1 to D2 at 10:30). Legs before the change go to D1 and legs after go to D2, with the leg rule in GF-321.
  - Idempotence: run `RollupDay` twice. Assert the second run affects 0 rows and the table is identical (`RowsAffected` on the insert, delete and update, plus a row snapshot compare).
  - Lock: hold `ROLL` from a second connection (the `alerts` test pattern, `internal/alerts/evaluator_test.go:337`). The pass is skipped and `passes_skipped` increments.
  - Property test (D-37, bounded): seeded generator, 25 fleets-days, 6 vehicles, random sample gaps and speeds, random pairing changes. SQL output compared with a Go reference model of the GF-321 rules.
  - Post-midnight: fixture with fleet time 00:10 local. Pass recomputes yesterday. Pass at 00:45 local does not.

**G3 — Reports API (`internal/reports` HTTP and store, `services/api` wiring).**
- `internal/reports/http.go`: `Register(e, authn)`, `GET /reports/daily`. Handler follows `internal/alerts/http.go:57-83`. Store reads the rollup only (one indexed SELECT, joined to `drivers` for the name). The store is `internal/reports/store.go`.
- `internal/reports/csv.go`: streamed CSV writer (GF-327).
- `services/api/server.go`: one line to register.
- Tests: role matrix (see GF-324). CSV header and content-type. Round-trip (CSV parse back to the JSON rows). Quoting of a driver name containing a comma and a double quote. Bad `date`, `driver` or `format` returns 400. Anonymous returns 401.

**G4 — Report page in `apps/web`.**
- `apps/web/src/reports/api.ts` (react-query `useQuery(['reports', date])`), `apps/web/src/reports/ReportsPage.tsx`, route `/reports` in `apps/web/src/App.tsx`, and a link in the existing topbar nav if one lists routes (G0 checks).
- `apps/web/vite.config.ts`: proxy key `/reports/daily` only (not `/reports`, which is the SPA route). No bypass needed.
- Tests: `apps/web/src/reports/ReportsPage.test.tsx` (Vitest component test). Renders a row, a null on-time percentage as "—", an empty day, a 403 "not available for your role" state, and a CSV link with the selected date. No new dependencies.

**G5 — CLAUDE.md rows.** A "Reports (P21)" block of at most 12 lines. Content: route, roles, rollup cadence and lock, CSV header, and the formula rule summary. No env var (GF-328). `CLAUDE.md` is 197 lines today, so adding 12 would reach 209. Trim at least 10 lines of existing content with no loss of facts, as P20 did (`23a651d`). The result must stay under 200.

**G6 — Full validation and results.** Run the Validation section. Append results and deviations to this file.

## Requirements

### Binding decisions carried verbatim

- **D-32** (`specs/constitution/tech-stack.md:291`): "Reports: `daily_driver_stats(fleet_id, driver_id, day, jobs_completed, on_time_count, on_time_denom, distance_m, idle_s)` refreshed every 15 min for today and once after midnight for yesterday (fleet timezone). Distance = sum of haversine between consecutive samples with gap ≤ 5 min. Idle = time between consecutive samples with speed < 1 km/h. On-time = delivered_at ≤ window_end over jobs that have a window. Driver attribution through `vehicle_pairings` active at sample time. **SPEC-AMEND** (table). §9 API p95 ≤ 300 ms rules out on-demand scans over 8.6M rows/day."
- **tech-stack §6 row** (`tech-stack.md:153`): the same nine columns as D-32.
- **Roadmap §3 P21** (`roadmap.md:141`): scope and validation as quoted in the pointer prompt.
- **Roadmap §1 (R-1/R-2)**: smallest MVP. No extra endpoints beyond the two forms of `/reports/daily`.

### Roadmap and README

- README §2.1.8 (line 33): per-day and per-driver jobs completed, on-time %, distance, idle time, CSV export.
- README §8 (line 176): Reports = Admin ✅, Manager ✅, Dispatcher "own fleet", Driver "own stats". GF-324 pins the dispatcher reading.
- README §9 (line 190): API p95 ≤ 300 ms for report endpoints. The rollup exists to meet this.
- README §10 (line 200): phase-4 checklist "daily reports + CSV".
- README is never edited (R-8).

### Reused GF decisions (cite, do not restate)

- **GF-291 / GF-306**: worker placement in `services/ingest`, tick and advisory-lock convention, pass-per-tx.
- **GF-294**: one transaction per pass.
- **GF-297**: load per pass, no cache. The fleet list and timezone are read each pass.
- **GF-298** (`specs/features/19-alerts-core.md:137-151`): API envelope and role matrix convention. Amended only where GF-324 says so.
- **GF-14** (`01-toolchain-ci-migrations.md:152`) and migration `004`: fleet timezone column.
- **GF-93 / GF-301**: metric naming and private registry until P22.
- **GF-318**: constants (15 min, 5 min gap, 1 km/h, post-midnight window) are not env vars.
- **GF-12**: testinfra PostGIS image.
- **GF-4**: core NATS. No event is published by P21.

### New decisions

**GF-319 — Migration `012` shape.** One table, no partitions.
- Columns, exactly the tech-stack §6 set plus the keys: `fleet_id BIGINT NOT NULL REFERENCES fleets(id)`, `driver_id BIGINT NOT NULL REFERENCES drivers(id)`, `day DATE NOT NULL` (the fleet-local calendar date), `jobs_completed INTEGER NOT NULL`, `on_time_count INTEGER NOT NULL`, `on_time_denom INTEGER NOT NULL`, `distance_m DOUBLE PRECISION NOT NULL`, `idle_s INTEGER NOT NULL`.
- `PRIMARY KEY (fleet_id, driver_id, day)`. The natural key is the upsert target.
- `CHECK (on_time_count >= 0 AND on_time_count <= on_time_denom AND on_time_denom >= 0 AND jobs_completed >= 0 AND distance_m >= 0 AND idle_s >= 0)`.
- `CREATE INDEX daily_driver_stats_fleet_day_idx ON daily_driver_stats (fleet_id, day)`. This serves `GET /reports/daily?date=` without a driver, which the PK prefix cannot. It is part of `012`, not a new schema element beyond D-32.
- Down file drops the table and index.
- Rationale: a surrogate key adds nothing, and the PK is the upsert target for GF-323. `DATE` in fleet-local time follows the existing `fleets.timezone` day convention (`internal/jobs/store.go:289`, `CLAUDE.md` day definition).

**GF-320 — Worker placement and cadence.**
- `internal/reports.Run(ctx, 15*time.Minute)` is a goroutine in `services/ingest/main.go`, started beside the ETA loop. It has the `reportsCtx`/`reportsDone` pair.
- A pass runs at boot, then every 15 min. The boot pass recomputes yesterday and today for every fleet. This is the catch-up after downtime for the most recent two days. Older days are not backfilled.
- Each pass, for every fleet: recompute today (fleet-local). Recompute yesterday only when the fleet-local time is in `[00:00, 00:30)`. Two ticks at 15 min land inside that 30 min window, so "once after midnight" holds as at-least-once. Repeats are free because of GF-323.
- Fleet timezone: loaded each pass (GF-297). `time.LoadLocation` failure skips that fleet, logs once, and increments `reports_rollup_passes_skipped` for the fleet, not the pass.
- Lock: `pg_try_advisory_xact_lock(0x52_4f_4c_4c)` ("ROLL") around the whole pass in one transaction (GF-294). Not locked → the pass is skipped and counted. This is separate from `FLTP` (`partitions.go:18`), so maintenance is not blocked.
- Known residual: a late sample for yesterday that arrives after 00:30 is not reflected until the next boot. Stated, not fixed.
- Rationale: 15 min and 30 min constants match D-32 and GF-318. One pass per tick keeps the ETA and alert loops unchanged.

**GF-321 — Metric formulas and attribution (D-32 pinned).**
- Sample set for a fleet-day: `positions` rows with `server_ts` in `[local midnight, next local midnight)` in the fleet timezone, computed with `time.LoadLocation` in Go and passed as `timestamptz`. Partition pruning on `server_ts` applies. Order `(vehicle_id, server_ts, msg_id)`.
- Leg = consecutive samples of one vehicle, both inside the window. Pairs crossing midnight are dropped. Stated loss, at most a few seconds at the 5 min cap.
- Gap = `cur.server_ts − prev.server_ts`. Leg counts only when gap ≤ 300 s. Gap > 300 s contributes to neither distance nor idle.
- Distance: `ST_DistanceSphere(prev.geom::geometry, cur.geom::geometry)` (haversine, PostGIS sphere). Sum per driver, then `ROUND(sum, 1)` to `distance_m`.
- Idle: a leg is idle when `prev.speed_kmh < 1.0` (strict), using the opening sample. Idle contribution = gap in seconds. Sum, then `ROUND(sum)::int` to `idle_s`.
- Attribution: the driver of the pairing active at `prev.server_ts`. Query: `vehicle_pairings` where `vehicle_id = prev.vehicle_id AND started_at <= prev.server_ts AND (ended_at IS NULL OR prev.server_ts < ended_at) AND driver_id IS NOT NULL`, `ORDER BY started_at DESC, id DESC LIMIT 1` (LATERAL). Precedence: opening sample's pairing, latest `started_at`, then `id`. A leg is wholly attributed to the opening sample's driver, even if a pairing change falls inside the leg. A driverless or unpaired leg contributes nothing.
- Jobs: `jobs_completed` = count of `state = 'delivered'` with `jobs.driver_id = d` and `(delivered_at AT TIME ZONE tz)::date = day`. Jobs use the job-level `driver_id`, not pairings, because a delivered job's assignment does not change (assignment changes only in `assigned`).
- On-time: among those delivered jobs, `on_time_denom` = jobs with `window_start IS NOT NULL AND window_end IS NOT NULL`. `on_time_count` = subset with `delivered_at <= window_end`. Jobs without a full window are in neither count.
- Day bucket for jobs: `delivered_at` in fleet timezone. Not `window_start`, so a late delivery counts on the day it happened. Stated, and tested.
- Row set: union of drivers with at least one attributed leg or one delivered job that day. Missing side is 0. A driver with neither gets no row.
- Rationale: rules are the D-32 text plus explicit tie-breaks. Opening-sample attribution avoids double-counting a leg across a pairing change.

**GF-322 — Computation placement.** Aggregation in SQL, one statement per fleet-day, using `LAG` over the partition-pruned window. Go only orchestrates and computes the window bounds. No sample rows are moved into Go. Rationale: 8.6M rows/day cannot be read into the process every 15 min.

**GF-323 — Idempotent write.** In the same transaction per pass, for each `(fleet_id, day)`:
1. `DELETE FROM daily_driver_stats WHERE fleet_id = $1 AND day = $2 AND driver_id NOT IN (<computed set>)`.
2. `INSERT ... SELECT ... ON CONFLICT (fleet_id, driver_id, day) DO UPDATE SET ... WHERE (jobs_completed, on_time_count, on_time_denom, distance_m, idle_s) IS DISTINCT FROM (EXCLUDED.jobs_completed, ...)`.

A second run with unchanged input writes 0 rows. The test asserts this. Rationale: "re-running changes nothing" is provable by `RowsAffected = 0`, not by a snapshot alone.

**GF-324 — Reports API shapes and roles.**
- Routes: `GET /reports/daily?date=YYYY-MM-DD&driver=<id>&format=json|csv`. JWT, `RequireAuth`.
- Defaults: `date` missing → fleet-local today. `driver` missing → all drivers for admin, manager, dispatcher; own driver only for the driver role. `format` missing → `json`.
- Roles: admin, manager, dispatcher read the whole fleet. The dispatcher reading of README §8 "own fleet" is taken as fleet-wide, the same as the alerts precedent (GF-298). **Flagged for user confirmation** (see Out of scope). Driver: own row only, via `users.driver_id` (`internal/jobs/driver.go:13-28`). Driver with no `users.driver_id` → empty rows, 200.
- `driver` param for admin, manager, dispatcher: must be a driver in the principal's fleet. Unknown, cross-fleet or malformed id → 404 `not_found` (same as the jobs precedent). Driver role: any `driver` other than their own → 404.
- Bad `date`, bad `driver` integer, or `format` not in {json, csv} → 400 `validation_failed`.
- JSON 200: `{"date":"2026-10-27","rows":[{"driver_id":7,"driver_name":"…","jobs_completed":3,"on_time_count":2,"on_time_denom":3,"on_time_pct":66.7,"distance_m":12345.6,"idle_s":1800}]}`. `on_time_pct` = `ROUND(100.0 * count / denom, 1)`, `null` when denom is 0. Rows sorted by `driver_id` ASC. No `computed_at` (not in the tech-stack columns).
- Rationale: GF-298 envelope with a named list. Rows only, no aggregates, because the admin page sums per day in the UI if needed. The response is a single indexed SELECT on `(fleet_id, day)`, meeting §9.

**GF-325 — Worker metrics.** `fleetops_reports_rollup_pass_duration_seconds` (histogram), `fleetops_reports_rollup_passes_skipped_total{reason}` with `reason` in `lock_held`, `fleet_tz`. Both pre-seeded. Private registry until P22. Rationale: GF-93 and GF-301. Two series, no more.

**GF-326 — Day and timezone boundary.** Fleet-local day bounds computed in Go (`time.LoadLocation`, `time.Date` in the zone), so DST days of 23 h and 25 h come out right. Passed to SQL as `timestamptz`, not `AT TIME ZONE` on a column, so partition pruning works. Rationale: pruning on `server_ts` is required for the p95 target.

**GF-327 — CSV shape.**
- `Content-Type: text/csv; charset=utf-8`, `Content-Disposition: attachment; filename="daily-<date>.csv"`.
- Header row, exact: `date,driver_id,driver_name,jobs_completed,on_time_count,on_time_denom,on_time_pct,distance_m,idle_s`.
- `encoding/csv` with `UseCRLF = true` (RFC 4180). Quoting by the stdlib writer.
- `on_time_pct` is empty when null. Numbers in the same precision as JSON.
- Streamed: rows read from the store's cursor and written as read, with a flush at the end. No full-body buffering.
- Rationale: stdlib-safe quoting. The round-trip test reads it back with `encoding/csv`.

**GF-328 — No env var, no new SPEC-AMEND.** No env var. Constants (15 min, 30 min window, 5 min gap, 1 km/h, 100-row flush) are not env vars (GF-318). No `jobs` column or new table beyond `012`.

## Validation

Runnable on this host. Each check has a pass condition.

- **(a) Gates.** `make fmt-check` exit 0. `make lint` exit 0, 0 issues. `make test` exit 0 with no FAIL, including the new `internal/reports` package. Any `internal/hub` flake gets an isolated re-run before it counts as a failure. `npm --prefix apps/web run typecheck`, `npm --prefix apps/web test` (baseline + the new `ReportsPage.test.tsx`, counts recorded), `npm --prefix apps/web run build` all exit 0.
- **(b) Scope fence.** `git diff --name-only main...HEAD` touches only: `db/migrations/012_*`, `internal/reports/**`, `services/api/server.go` (one line), `services/ingest/main.go` (worker wiring only), `apps/web/src/reports/**`, `apps/web/src/App.tsx` (route), `apps/web/vite.config.ts` (proxy key), the topbar nav component only if it lists routes, `CLAUDE.md`, `specs/features/21-daily-reports-csv.md`, `prompts/**`. It must NOT touch `README.md`, `e2e/`, `apps/driver-pwa/`, `internal/jobs/**` (read-only use), `internal/eta/**`, `internal/alerts/**`, `internal/geofence/**`, `internal/simulator/**`, `specs/constitution/`, `deploy/docker-compose.yml`, `.github/`. Output of the command is attached to Validation results.
- **(c) No secrets.** `git diff main...HEAD | grep -iE 'secret|password|token|key='` returns no added value lines.
- **(d) e2e not run.** `git diff --name-only main...HEAD -- e2e/` is empty. `make e2e` is NOT RUN, because its stack script runs `docker compose down -v`, which is the driver standing rule. Recorded as a deviation with reason.
- **(e) CLAUDE.md.** `wc -l CLAUDE.md` < 200.
- **(f) P22 boundary.** `grep -rniE 'prometheus|grafana|otel' internal/reports services/ apps/web/src/reports` returns nothing. (The existing `internal/ingest` and `internal/eta` registries are untouched; only the new package is checked.)
- **(g) driver-pwa untouched.** `git diff --name-only main...HEAD -- apps/driver-pwa/` is empty.
- **(h) CSV correctness.** Round-trip test passes, and the CSV header row is asserted byte-for-byte.
- **(i) Idempotence.** Second rollup writes 0 rows (`RowsAffected`) and the table snapshot is identical. Test passes.
- **(j) Attribution.** The mid-day pairing change test passes, and the pairing rule (GF-321) is covered.
- **(k) Role matrix.** Test covers 4 roles × {default, own driver, other driver, cross-fleet driver, CSV} plus anonymous 401.

## Testing depth

| Layer | Covers | Where |
|---|---|---|
| Unit | Haversine via `ST_DistanceSphere` vs a Go helper (R = 6371008.8 m, epsilon 0.5 m). Idle threshold boundary (`speed_kmh` 0.99 idle, 1.00 not idle). Gap boundary (300 s counted, 301 s not). Pairing-precedence rule. Fleet-local day bounds on a 23 h DST day. | `internal/reports/*_test.go` |
| Integration golden | Distance, idle, on-time %, jobs completed from a hand-computed fixture in testinfra PostGIS: a 1-minute stationary leg, a moving leg, a >5-min gap, a driverless pairing, a delivered job with and without a window, a delivery after `window_end`, a delivery on a DST day, a jobs-only driver. Expected values hand-computed in test comments and asserted exactly (counts, ints) or within stated epsilon (distance). | `internal/reports/golden_integration_test.go` |
| Integration attribution | Mid-day pairing change (D1 → D2 at 10:30). Each leg attributed to the opening sample's driver. | same file |
| Integration idempotence | Two runs, `RowsAffected = 0` on second, identical rows. | `internal/reports/rollup_idempotent_integration_test.go` |
| Integration lock | Pass skipped when `ROLL` is held from a second connection. | `internal/reports/pass_integration_test.go` |
| Integration property (D-37) | 25 seeded fleet-days, 6 vehicles, random gaps, speeds, pairing changes; SQL output equals Go reference model. Fixed seed, bounded runtime. Warranted because attribution and gap rules have many combinatorial edges. | `internal/reports/property_integration_test.go` |
| Integration migration | `012` up → down → up round-trip, schema assertions. | existing migration round-trip harness (G0) |
| API integration | Role matrix (GF-324), CSV header and content-type, CSV round-trip, quoting, 400s, 401. | `internal/reports/http_integration_test.go` |
| Component | `ReportsPage` renders rows, null → "—", empty day, 403 state, CSV link with date. | `apps/web/src/reports/ReportsPage.test.tsx` |
| Existing gates | Go suite, `apps/web` baseline, `apps/driver-pwa` baseline (untouched, recorded) all still green. The `apps/web` count changes by the new file. | G0, G6 |

Testing policy: `testinfra.PostgisImage` for every DB test (GF-12). No mock DB. No new JS dependency.

## SPEC-AMEND repeat

- **New SPEC-AMEND row: none.** D-32 is already registered (`mission.md:76`) as the owning SPEC-AMEND for `daily_driver_stats`, and tech-stack §6 names the table. The `(fleet_id, day)` index (GF-319) is an implementation detail of the owned table, not a new schema element. No env var is added (GF-328). The `/reports/daily` route is already named in tech-stack §3 (P21).
- **Record explicitly:** if the dispatcher reading in GF-324 is rejected by the user, the change is a role-matrix edit to README §8 behaviour, which is a new SPEC-AMEND row. The default plan does not add one.
- **GF-192 hub identity binding remains open and belongs to P23.** Repeated here as the standing reminder. P21 does not touch the hub.

## Out of scope

- Dashboards and the `/metrics` export (P22). The worker metrics in GF-325 are registered, not exposed.
- OpenTelemetry and Grafana (P22).
- k6 load test and demo seed (P23).
- Multi-day and range aggregation. Only single-day queries.
- Backfilling days older than yesterday.
- Driver scoring (backlog).
- Email delivery changes.
- Simulator changes (D-12).
- driver-pwa changes.
- README edits (R-8).
- Compose changes and CI changes.
- Any schema beyond `db/migrations/012`.
- Merge to `main`, push, and any P22+ content.
- A fix for the known gap in `services/ingest/main.go` where `maintDone` is not joined on the `EnsureConsumer` error path (observed in G0 research, not a P21 concern). Recorded for later.
- GF-192 hub identity binding (P23).

**Decision for the user to confirm:** GF-324 reads README §8 "Dispatcher: own fleet" as fleet-wide read access. If the user wants dispatchers excluded, the change is a role-matrix edit (see SPEC-AMEND repeat), and the role tests change accordingly.

## Validation results and deviations

Run 2026-10-28 on `phase-21-daily-reports-csv`, commits `b49ed44` (G1) through `c507102` in build order, then the G6 commit that holds this section. Host: Go 1.27.3 (`/root/.local/go/bin`), golangci-lint v2.15.0, Docker with `postgis/postgis:16-3.4` through testcontainers, Node for the web apps. No compose stack was started.

### G0 baseline (before any edit)

- `make lint`: `0 issues.`, exit 0. `make test`: exit 0, 27 packages `ok`, no FAIL.
- `apps/web`: `typecheck` exit 0; `test` 16 files, 88 tests passed; `build` exit 0.
- `apps/driver-pwa` (not touched, recorded only): `npm ci` exit 0; `test` 19 files, 129 tests passed; `build` exit 0 (`check-pwa-build: ok`).
- Pins confirmed: highest migration `011_alerts_core` (P21 adds `012`); `fleets.timezone` (`004`); `drivers` and `vehicle_pairings` shapes as in the Baseline facts; `apierr` has `not_found` (404) and `validation_failed` (400); the migration round-trip harness is `db/roundtrip_test.go`, and the step-down pattern is `internal/alerts/seed_test.go`. `retainMonths` was not re-read.
- The constitution file `specs/constitution/constitution.md` does not exist. The constitution is `mission.md`, `roadmap.md` (R-1 to R-9, §1) and `tech-stack.md`. R-rules were checked against the diff (see Deviations).

### Files per task group

- **G1** (`b49ed44`): `db/migrations/012_daily_driver_stats.{up,down}.sql`, `internal/reports/migration_integration_test.go`.
- **G2** (`97dfde6`, `c507102`): `internal/reports/{rollup,pass,metrics}.go`; tests `helpers_test.go`, `golden_integration_test.go`, `attribution_integration_test.go`, `rollup_idempotent_integration_test.go`, `pass_integration_test.go`, `rollup_unit_test.go`, `property_integration_test.go`; `services/ingest/main.go` (worker wiring).
- **G3** (`7ec2274`): `internal/reports/{store,csv,http}.go`, `http_integration_test.go`; `services/api/server.go`.
- **G4** (`d45a6cc`): `apps/web/src/reports/{api.ts,ReportsPage.tsx,ReportsPage.test.tsx}`; `apps/web/src/App.tsx`; `apps/web/vite.config.ts`; Reports links in the topbars of `map/MapPage.tsx`, `geofences/ZonesPage.tsx`, `jobs/JobsPage.tsx`.
- **G5** (`4331024`): `CLAUDE.md` (197 to 193 lines).
- **G6 follow-ups**: `c4591a7` moves the migration head pin in `db/roundtrip_test.go` to 12; `c507102` moves the golden 600 s gap to 301 s so the 300/301 s boundary is covered.

### Validation

**(a) Gates.** Final tree:
- `make fmt-check`: exit 0.
- `make lint`: `0 issues.`, exit 0.
- `make test`: exit 0, 28 packages `ok`, no FAIL. `internal/hub` did not flake, so no isolated re-run was needed. `internal/reports` ran `ok`.
- `npm --prefix apps/web run typecheck`: exit 0.
- `npm --prefix apps/web test`: 17 files, 92 tests passed (baseline 16 and 88: +1 file, +4 tests in `ReportsPage.test.tsx`).
- `npm --prefix apps/web run build`: exit 0.

**(b) Scope fence.** `git diff --name-only main...HEAD` (33 files, +2769 / −20) includes none of `README.md`, `e2e/`, `apps/driver-pwa/`, `internal/jobs|eta|alerts|geofence|simulator/`, `specs/constitution/`, `deploy/docker-compose.yml` or `.github/`. It includes `db/roundtrip_test.go`, the topbar pages `MapPage.tsx`, `ZonesPage.tsx` and `JobsPage.tsx`, and the server wiring beyond one line. Each is listed under Deviations.

**(c) No secrets.** `git diff main...HEAD | grep -iE 'secret|password|token|key='` matches added lines, but none carries a secret value. They are: the CLAUDE.md variable names (no values); test placeholders (`'x'` as a password hash, the 32-byte test key `strings.Repeat("r", 32)`, `accessToken: 'token'` in the web test); and the words "token" and "secret" in comments. The literal pass condition ("no added value lines") is therefore not met by a plain grep. Manual review found no secret values.

**(d) e2e not run.** `git diff --name-only main...HEAD -- e2e/` is empty. `make e2e` is **NOT RUN**: its `e2e-stack-up` step calls `e2e/scripts/stack.sh up`, and `stack.sh:127` runs `docker compose down -v`. That is the driver standing rule against running the compose teardown here.

**(e) CLAUDE.md.** `wc -l CLAUDE.md` → `193` (< 200).

**(f) P22 boundary.** Literal `grep -rniE 'prometheus|grafana|otel' internal/reports services/ apps/web/src/reports` **does not return nothing**. The matches are: `internal/reports/metrics.go` (the GF-325 series import and types, which GF-325 requires); `internal/reports/helpers_test.go` (the same registry in fixtures); `services/ingest/main.go:16,83` and `services/ws-hub/e2e_test.go:17,163` (existing P6/P8 registry lines, not added by P21). The intent holds: `grep -rniE 'promhttp|/metrics|grafana|otel|opentelemetry' internal/reports services/ apps/web/src/reports` returns only the existing comment `services/ingest/main.go:2` ("/metrics is P22"). Nothing is exposed and there is no OTel or Grafana.

**(g) driver-pwa untouched.** `git diff --name-only main...HEAD -- apps/driver-pwa/` is empty.

**(h) CSV correctness.** `TestReportsCSVHeaderAndRoundTrip` PASS: the header row is asserted byte for byte (`date,...,idle_s\r\n` as a prefix), `Content-Type: text/csv; charset=utf-8` and the `Content-Disposition` filename are asserted, and `encoding/csv` parses the body back to the JSON rows. The driver name `Cy, "the" jobs-only` round-trips. `TestReportsCSVEmptyDay` PASS (header only; JSON `rows: []`).

**(i) Idempotence.** `TestRollupIdempotent` PASS: first run `{Deleted:1, Upserted:2}` (a stale row for a driver with no activity is removed); second run `{0, 0}` with the table snapshot identical; a changed input writes `{Upserted:1}` for the changed driver only.

**(j) Attribution.** `TestAttributionPairingChange` PASS: D1 → D2 at 10:30 UTC. The leg opened at 10:26 and spanning the change goes wholly to D1. The leg opened at 10:30 goes to D2 (`started_at` inclusive, `ended_at` exclusive). The property test checks the multi-pairing precedence against the Go GF-321 model.

**(k) Role matrix.** `TestReportsRoleMatrix` PASS: admin, manager and dispatcher each cover default, own-driver filter, other-driver filter, cross-fleet driver (404), unknown driver (404) and CSV. The driver role covers own row by default, own filter, other driver (404), cross-fleet (404), CSV, and an unlinked driver (empty list; filter 404). Anonymous is 401. `TestReportsBadParams` PASS (400 for bad date, date format, driver, format).

**Reports package, verbose** (`go test ./internal/reports -race -count=1 -v`): all PASS, including `TestMigration012RoundTrip`, `TestGoldenDay` (23 h NY day: 4 jobs, on-time 1/2, idle 180 s, distance within 0.5 m of the Go haversine; D3 jobs-only; driverless vehicle counts nothing), `TestPassLockSkipsAndCounts`, `TestPassPostMidnightWindow`, `TestPassSkipsBadFleetTZ`, `TestPropertyRollupMatchesReference` (25 seeded fleet-days, 6 vehicles each, seed `20260308/21`, SQL equals the Go reference), `TestDayBoundsLength` (23 h, 25 h, 24 h), `TestLocalDayUsesFleetZone`, `TestDistanceSphereMatchesHaversine`, `TestInYesterdayWindow`.

### Deviations

1. **`db/roundtrip_test.go`** is not in the fence's list. Migration 012 moves the head, and the existing `TestMigrationRoundTrip` pins head 11. The first `make test` failed with `version: want 11, got 12`. I changed the pin to 12 (three lines, one comment) in `c4591a7`.
2. **Topbar links in `MapPage.tsx`, `ZonesPage.tsx`, `JobsPage.tsx`.** The topbars are per page and list routes, so G4's "if one lists routes" applies. The fence names only "the topbar nav component", so this goes beyond it. One link line each.
3. **`services/api/server.go`** is more than one line: an import, a two-line handler construction, and a blank line. The route needs both a handler and a store.
4. **Validation (f)** cannot pass literally: GF-325 requires the Prometheus registry in `internal/reports`. See (f).
5. **Validation (c)** does not return "no added lines" by plain grep. See (c).
6. **GF-324 status codes for `driver`.** The spec says both "malformed id → 404" and "bad driver integer → 400". I chose: a non-integer `driver` is 400 `validation_failed`; an integer that is unknown, cross-fleet, or not the caller's (driver role) is 404 `not_found`.
7. **Row set.** A driver gets a row when at least one leg counts (gap ≤ 300 s, attributed) or a delivered job exists. A leg with gap > 300 s does not create a row by itself.
8. **Pairing lookup (GF-321).** The query is implemented as written, with `driver_id IS NOT NULL` in the WHERE clause. So if the latest active pairing at the opening sample is driverless, an earlier active pairing with a driver can be picked. Open pairings are unique per vehicle, so this needs overlapping history.
9. **CSV download in the web app.** The link keeps the dated `href` (`/reports/daily?date=…&format=csv`). The click goes through `apiFetch` and saves a blob, because a plain link cannot send the bearer token and would get 401.
10. **Default date in the UI** is the browser's local date. The UI has no fleet timezone. The API default (no `date`) is fleet-local.
11. **Bad fleet timezone** is logged on every pass that hits it. GF-320 says "logs once"; the counter still goes up per fleet per pass.
12. **Spec status line** (line 3, "planned … not implemented") is left as written. The append-only rule applies to existing sections.
13. **Dispatcher reading** (GF-324) is implemented as stated, fleet-wide, per the run instruction. The README §8 row is untouched. The user still needs to confirm it.

### Residuals (stated in the spec, not fixed)

- GF-320: a late sample for yesterday that arrives after 00:30 local is not reflected until the next boot.
- The `maintDone` join gap in `services/ingest/main.go` is untouched (out of scope).
- `make e2e` has not run (see (d)). The browser flow of the CSV download and the report page has not been exercised outside the component test.
