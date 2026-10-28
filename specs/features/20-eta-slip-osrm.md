# Feature 20 — ETA slip & OSRM (P20)

Status: planned on `phase-20-eta-slip-osrm`, not implemented, 2026-10-28. Validation results and deviations are appended at the end when the phase runs.
Source plan: /root/.claude/plans/p20-plan-eager-puzzle.md (approved).

## Scope

Roadmap P20 (`specs/constitution/roadmap.md` §3) delivers:

- `deploy/osrm/prepare.sh`: one-shot extract, partition and customize of an operator-supplied PBF into `osrm_data` as `region.osrm` (D-10). Not run in CI, not part of `make e2e`.
- An OSRM `/route` client: `OSRM_URL`, request shape, `duration` → minutes, error taxonomy.
- ETA recompute for each active job at most once per 5 min, and on assign (D-35), with write-back to `jobs.eta_minutes`.
- The `eta_slip` rule (D-34): projected arrival > `window_end` + 15 min → warning alert on the existing `internal/alerts` kind.
- Degradation: OSRM unreachable → rule skipped, metric incremented, no alert (D-10, D-34).

Out of scope: reports (P21), dashboards and `/metrics` export (P22), k6 (P23), `/trip` and matrix APIs, simulator changes (D-12), driver-pwa and web changes, README edits, compose changes, CI changes, any `jobs` schema change.

### Baseline facts (verified read-only)

- Branch `phase-20-eta-slip-osrm`, base `main` at `11a0136`.
- Migrations `001`–`011`. Highest is `011_alerts_core`. `jobs` (migration `009_jobs_core.up.sql`) has `window_start`, `window_end`, `eta_minutes INT`, and `CONSTRAINT jobs_eta_range CHECK (eta_minutes IS NULL OR eta_minutes BETWEEN 0 AND 1440)` at line 23. No later migration touches `eta_minutes`.
- Highest decision ID in `specs/features/19-alerts-core.md` is GF-302. This plan starts at GF-303.
- `services/ingest/main.go:91-115` starts the presence sweeper and the alert evaluator as goroutines with cancel and done channels. Maintenance runs at `:117-127`. `consumer.Run` blocks at `:135`.
- `internal/alerts/evaluator.go:18` holds `passLockKey` (`0x46_41_4c_45`, "FALE"), taken with `pg_try_advisory_xact_lock`. `Interval = 30s` is at `:15`.
- `internal/alerts/evaluator.go:123` is a no-op `case` for `KindETASlip`.
- `internal/alerts/store.go:220`: `evaluated` is computed as `r.Kind != KindETASlip`.
- `internal/alerts/rules.go:50-54`: the `eta_slip` default is `{}` with `Enabled: false`.
- `internal/alerts/evaluator.go:332`: `insertAlert(ctx, tx, fleetID, kind, severity, payload, at) (Raised, error)`.
- `internal/alerts/publish.go:30,35,53`: `Subject`, `Encode`, and `publishRaised` (unexported).
- `internal/alerts/notify.go:72`: `notifyCritical` (critical only).
- `db/migrations/011_alerts_core.up.sql:26-33` backfills `eta_slip` `{}` `false` per fleet. `internal/alerts/seed.go:12-23` seeds new fleets.
- `internal/jobs/publish.go:30`: subject `jobs.events.<fleet_id>`, core NATS, body `job.updated`. Ingest's JetStream stream `GPS_RAW` covers only `gps.raw.>` (`internal/ingest/stream.go:12,19`). `job.updated` is therefore not replayable into ingest.
- `db/migrations/007_positions_vehicles_latest.up.sql:14-24`: `vehicles_latest(vehicle_id PK, fleet_id, geom geography(Point,4326), server_ts, ...)`.
- `deploy/docker-compose.yml:50-64`: the `osrm` service uses `osrm/osrm-backend`, command `osrm-routed --algorithm mld /data/region.osrm`, port `127.0.0.1:10005:5000`, volume `osrm_data:/data`, and a TCP-connect healthcheck.
- `OSRM_URL` is not in `internal/config` (no field, no read). It is named in the env list at `specs/constitution/tech-stack.md:93` (§5), and the routing row is at `tech-stack.md:14` (§1).
- `docs/` does not exist. No `shellcheck` or `sh -n` target exists in the Makefile.
- Test patterns: `internal/simulator/client_test.go:48,80` (`httptest.NewServer` as a fake peer). `internal/alerts/silence_property_test.go` (property-test precedent). `internal/testinfra` (PostGIS container).

## Plan

Build order is G0 → G6. Each group lists its tests. Task groups are ordered so that each one can be verified before the next starts.

### G0 — Preflight

- Run the baseline gates on `phase-20-eta-slip-osrm` before any edit: `make fmt-check`, `make lint`, `make test`. Record the output.
- If `internal/hub` fails, re-run it in isolation before treating the failure as real (known flake policy).
- Record baselines for `apps/web` (`npm --prefix apps/web run typecheck`, `npm --prefix apps/web test`) and `apps/driver-pwa` (`npm --prefix apps/driver-pwa run typecheck`, `npm --prefix apps/driver-pwa test`). Both are untouched this phase.
- Pin the column names this plan depends on by reading `db/migrations/009_jobs_core.up.sql` and later job migrations: `jobs.vehicle_id`, `jobs.driver_id`, pickup geometry, dropoff geometry. If any of these is missing, stop and report. The plan does not add them.
- Read the `jobs` trigger that guards `app.actor_id`. Confirm it fires only on state or assignment change, not on `eta_minutes`.
- Confirm the migration count is 11 and the highest decision ID is GF-302.
- Tests: none new. The output is the baseline record.

### G1 — `deploy/osrm/prepare.sh` and `docs/osrm.md`

- Create `deploy/osrm/prepare.sh` as POSIX `sh`. Invocation: `sh deploy/osrm/prepare.sh <pbf-file-name>`. The PBF is read from `deploy/osrm/data/` (gitignored, D-10). No Makefile target.
- The script runs one-shot containers using the same image reference as compose (`OSRM_IMAGE`, defaulting to compose's `osrm/osrm-backend`):
  1. Copy the PBF into `osrm_data` as `region.osrm.pbf`. Mount `deploy/osrm/data` read-only.
  2. `osrm-extract -p /opt/car.lua /data/region.osrm.pbf`. Use the car profile.
  3. `osrm-partition /data/region.osrm`.
  4. `osrm-customize /data/region.osrm`. This matches the `mld` algorithm in compose. No `--uploaded` flag.
- Idempotence: if `region.osrm.mldgr` already exists in `osrm_data`, the script exits 0 and does nothing. `--force` re-runs the pipeline.
- The script asserts the expected output files exist after customize. Otherwise it exits non-zero with a message.
- `docs/osrm.md` is the operator prep note (D-39). It covers: where to download the Geofabrik region PBF, the output target of 200–500 MB, the commands, the image digest to record, and the cloud degradation note.
- The real run is operator-manual. Record it as NOT RUN with the reason: no PBF is in the repo, and the run needs Docker and a multi-hundred-MB download.
- Tests and checks:
  - `sh -n deploy/osrm/prepare.sh` passes.
  - A grep check confirms the step-4 command contains `osrm-customize` and not `--algorithm`, and that the `mld` pin matches compose.
  - Manual check on the operator host: run the script against a small extract such as Monaco, if one is present. Otherwise NOT RUN.

### G2 — OSRM client (`internal/eta`, client file)

- Package `internal/eta`. Files: `osrm.go` (client), `limiter.go`, `slip.go`, `loop.go`, `metrics.go`.
- `Router` interface with `Route(ctx, from, to Point) (minutes int, err error)`. The HTTP implementation is `OSRMRouter{BaseURL, HTTP *http.Client}`. The client timeout is 2 s. No retries.
- Request: `GET {BaseURL}/route/v1/driving/{lng},{lat};{lng},{lat}?overview=false`. Origin is the vehicle's `vehicles_latest` point. Target is pickup if state ∈ {assigned, accepted}, else dropoff. Coordinates are in lng,lat order.
- Parsing: require HTTP 200, a JSON body with `code == "Ok"`, at least one entry in `routes`, and `routes[0].duration` finite and ≥ 0. `minutes = ceil(duration / 60)`. Clamp the result to `[0, 1440]` at write time.
- Error taxonomy (also the metric outcome label): `ok`, `unreachable` (connection refused or DNS), `timeout`, `http_error` (non-200), `malformed` (bad JSON, missing fields, non-finite duration), `no_route` (`code != "Ok"` or empty `routes`).
- Config: `OSRM_URL` is optional. Default `http://127.0.0.1:10005` when unset or empty, matching the compose port. The value must parse as an `http` or `https` URL. A bad value is refused at boot, and the error names the variable, never the value. Add `OSRM_URL` to `internal/config` (`load`, with the injected lookup) and to CLAUDE.md's env list.
- Tests:
  - Contract tests with recorded OSRM JSON via `httptest`, in `internal/eta/testdata/*.json`. Cases: happy path (`duration` 754.3 → 13 min), exact multiple of 60 s, `duration` 0 → 0, `code` NoRoute → `no_route`, empty `routes` → `no_route`, HTTP 500 → `http_error`, bad JSON → `malformed`, `duration` missing → `malformed`, server sleep past the 2 s timeout → `timeout`, closed listener → `unreachable`.
  - Config test: `OSRM_URL` unset → default; `ftp://…` → refused naming the variable.
  - The fixtures are checked in. The tests make no network calls.

### G3 — ETA recompute loop, rate limiter, and write-back

- `loop.go`: `Loop{DB, Router, Limiter, Clock, Metrics, Publisher, Notifier, Logf}` with `Run(ctx)` on a 30 s ticker (`eta.Interval`). Started in `services/ingest/main.go` beside the alert evaluator. Same cancel and done pattern as the evaluator at `:104-115`. Stopped before the consumer returns.
- Each tick runs one transaction under `pg_try_advisory_xact_lock(0x45_54_41_4c)` ("ETAL"). If the lock is not acquired, the pass is skipped and counted.
- Candidate SQL: jobs with state ∈ {assigned, accepted, en_route, picked_up} and a non-null `vehicle_id`. Join `vehicles_latest` for the origin. A missing position is skipped with reason `no_position` and no OSRM call.
- Rate limiter (`limiter.go`): in-memory `map[jobID]time.Time` of the last attempt. A job is allowed one attempt per 5 min. An attempt counts toward the window regardless of outcome. Entries for jobs no longer in the active set are pruned each pass. A restart resets the map, which allows at most one extra call per job per process start.
- Assign trigger: the loop scans the active set every tick. A newly assigned job with no prior attempt is computed on the next tick, within 30 s. This is the polling variant, not NATS (see GF-308).
- Write-back: `UPDATE jobs SET eta_minutes = $2 WHERE id = $1 AND eta_minutes IS DISTINCT FROM $2`. The value is clamped to `[0, 1440]`. No `job_events` row, no `job.updated` publish, and no `app.actor_id` (the trigger guards state and assignment only; G0 verifies this).
- Tests:
  - Unit, rate limiter: a fake clock drives a sequence of ticks across several jobs. Assert at most one attempt per job per 5 min window. Add a bounded property test with a fixed seed and 1000 iterations (D-37 precedent: `silence_property_test.go`).
  - Unit, ETA math: `ceil` boundaries; clamp below 0 and above 1440.
  - Integration (PostGIS testinfra): an assigned job with a fixture vehicle position and an `httptest` OSRM returning 754 s → `jobs.eta_minutes` = 13 within one pass. A second pass inside 5 min makes no OSRM call. After the window, one more call.
  - Integration, assign: a job flips to assigned, and its ETA appears within one tick.
  - Integration, CHECK clamp: a fixture that would produce 1500 writes 1440.

### G4 — `eta_slip` rule firing and alert raise

- `slip.go`: `Slip(now time.Time, etaMinutes int, windowEnd *time.Time) bool`. Projected arrival is `now + eta_minutes`. Slip is true only if `windowEnd != nil` and `projected > *windowEnd + 15 min` (strict). A windowless job (`window_end IS NULL`) never slips.
- The check runs in the loop only after a successful fresh computation in the same pass (GF-310). It runs only if the fleet's `eta_slip` rule has `enabled = true`. The loop reads `alert_rules` per fleet per pass, with no cache (GF-297 precedent).
- Dedup: before insert, `NOT EXISTS` an `alerts` row with `kind = 'eta_slip'`, `payload->>'job_id' = <job>`, and `payload->>'window_end' = <window_end>`. Run under the ETA lock (GF-294 precedent). One alert per job per window (GF-311).
- Raise path: add two exports to `internal/alerts`: `RaiseETASlip(ctx, tx, fleetID, payload) (Raised, error)` as a thin wrapper over `insertAlert`, and `PublishRaised` as an export of `publishRaised`. The loop commits, then publishes and notifies through the same `Publisher` and `Notifier` as the evaluator.
- Payload: `{job_id, vehicle_id, eta_minutes, projected_at (RFC3339), window_end (RFC3339), threshold_min: 15}`. Severity `warning`.
- Tests:
  - Unit, slip math: boundary at exactly `window_end + 15 min` (no slip), one second past (slip), windowless (never), and an ETA of 0.
  - Integration, the required slip test: a delayed fixture job (window_end in the past, OSRM stub returning 3000 s) and the rule enabled → exactly one `alerts` row, kind `eta_slip`, severity `warning`, and the payload fields above.
  - Dedup: a second pass with the same window → no new row.
  - Windowless job with a huge ETA → no row.
  - Rule disabled → no row, and the ETA is still written (ETA is independent of the rule, GF-313).
  - Unit, `internal/alerts`: `KindETASlip` `evaluated` is `true` in the list response (see G5).

### G5 — Rule activation

- `internal/alerts/store.go:220`: change `evaluated` to `true` for every kind. The P19 `evaluated: false` placeholder is retired (GF-317).
- Seeds and existing rows keep `enabled = false` for `eta_slip`. No migration. A manager or admin enables it with the existing `PATCH /alert-rules`, which already accepts `eta_slip` `{}` (GF-290).
- Tests:
  - `internal/alerts` HTTP test: `GET /alert-rules` shows `evaluated: true` for `eta_slip`. `PATCH` `enabled: true` is accepted and reflected.
  - Existing P19 alert tests stay green.

### G6 — Degradation

- When the router returns `unreachable`, `timeout`, `http_error`, `malformed`, or `no_route`, the job is skipped. `eta_minutes` is not written. No slip is evaluated. `fleetops_eta_rule_skipped_total{reason="osrm_unavailable"}` is incremented once per skipped job. There is one log line per pass, not per job. No alert is raised.
- A missing position increments `reason="no_position"`. A job with no target increments `reason="no_target"`.
- Cloud: `OSRM_URL` is set to the public demo server by the operator. This is config only. No code path is special-cased. Demo-server availability is not guaranteed, and the degradation path above covers it (D-10). The repo does not implement a fallback.
- Metrics (GF-93, GF-301): `fleetops_eta_calls_total{outcome}`, `fleetops_eta_rule_skipped_total{reason}`, `fleetops_eta_pass_duration_seconds` (histogram), `fleetops_eta_passes_skipped_total`. Every label set is pre-seeded to zero. The registry stays private to ingest until P22 (GF-315).
- Tests:
  - Integration, degradation: an `httptest` OSRM that is closed → no alert row, no `eta_minutes` write, `fleetops_eta_calls_total{outcome="unreachable"}` increments, and `fleetops_eta_rule_skipped_total{reason="osrm_unavailable"}` increments.
  - Unit, metric pre-seeding: all label values exist at zero on registration.
  - Timeout variant: a stalled server → `outcome="timeout"`, same no-alert result.

### G7 — CLAUDE.md rows and full validation

- Add a "## ETA (P20)" block of at most 12 lines to CLAUDE.md: the OSRM client location, `OSRM_URL` in the env list, the `prepare.sh` invocation pointer to `docs/osrm.md`, the 5-min and 30 s cadence, and the eta_slip rule activation via PATCH. No ports, no make targets, no decision prose.
- Run the full validation section below.

## Requirements

### Binding decisions carried verbatim

- **D-10** (OSRM provenance): the operator downloads a regional OSM PBF (Geofabrik extract, 200–500 MB output target) into `deploy/osrm/data/` (gitignored). `deploy/osrm/prepare.sh` runs extract, partition and customize in a one-shot `osrm/osrm-backend` container into `osrm_data` as `region.osrm`. It is not run in CI. Cloud uses the public demo server, where ETA rules degrade.
- **D-34** (`eta_slip`): projected arrival > `window_end` + 15 min, warning severity, evaluated every 5 min per active job and on assign.
- **D-35** (ETA): OSRM `/route` from the vehicle's latest position to the job's next target. The target is pickup if state ∈ {assigned, accepted}, else dropoff. The result goes to `jobs.eta_minutes`. At most one call per job per 5 min.
- **D-12**: the simulator stays OSRM-free.
- **D-39**: operator-facing notes live in `docs/`.
- **D-37**: property tests are used where they add signal. Here, the rate limiter.
- **GF-247**: CI `e2e` does not start `osrm`.

### Roadmap and README

- Roadmap §1 (R-1/R-2): smallest MVP. No extra endpoints, no optimisation.
- Roadmap §13: OSRM RAM row. Region extract ~200–500 MB, no continent extract.
- README §2.1.7 (ETA row), §3 (evaluation mechanics), §4 (routing), §13 (operator prep), §8 (audit). README is never edited (R-8). Where README and D-35 differ, D-35 wins. The README wording is not restated.

### Reused GF decisions (cite, do not restate)

- **GF-291**: evaluator placement in `services/ingest`, 30 s tick, advisory lock.
- **GF-292**: silence episode precedent for dedup.
- **GF-294**: one transaction per pass, dedup via `NOT EXISTS` under lock, no unique index.
- **GF-297**: rules loaded per pass, no cache, `enabled = false` skips the kind.
- **GF-298**: API shapes and roles. Amended by GF-317 for `evaluated`.
- **GF-290**: `eta_slip` seeded disabled.
- **GF-93**, **GF-301**: metric naming and registry conventions.
- **GF-4**: core NATS for `jobs.events`. Not consumed here.
- **GF-208**: container conventions for `prepare.sh`.

### New decisions

**GF-303 — OSRM client package and config.** Package `internal/eta`. A `Router` interface with an `OSRMRouter` HTTP implementation. Client timeout 2 s, no retries. `OSRM_URL` is optional, defaults to `http://127.0.0.1:10005`, and must be http or https. A bad value is refused at boot, naming the variable. Rationale: one package keeps the client, limiter, loop, and slip math together, and the interface makes the loop testable against `httptest`. Retries are excluded because the limiter already bounds calls to one per 5 min per job.

**GF-304 — Parse rules and error taxonomy.** Accept only HTTP 200 with `code == "Ok"`, at least one route, and a finite `duration ≥ 0`. Minutes are `ceil(duration / 60)`, clamped to `[0, 1440]` at write. Outcome labels: `ok`, `unreachable`, `timeout`, `http_error`, `malformed`, `no_route`. Rationale: `ceil` is conservative for slip detection (an ETA is never understated). A zero-length route is valid and yields 0. An empty route set is `no_route`, not an error to retry.

**GF-305 — `prepare.sh` shape.** POSIX `sh`, run as `sh deploy/osrm/prepare.sh <pbf-name>`. Four steps in one-shot containers: copy PBF to `osrm_data`, `osrm-extract -p /opt/car.lua`, `osrm-partition`, `osrm-customize`. The `mld` pipeline matches compose's `--algorithm mld`. Idempotent: skip when `region.osrm.mldgr` exists; `--force` re-runs. Output is asserted. No Makefile target, which keeps the Makefile out of scope and the operator path explicit. Rationale: the operator runs this rarely and by hand.

**GF-306 — Loop placement and cadence.** A goroutine in `services/ingest/main.go`, started beside the alert evaluator, on a 30 s tick. Each pass runs under `pg_try_advisory_xact_lock(0x45_54_41_4c)`. Rationale: GF-291 precedent. The 30 s tick is the assign-latency bound. The 5-min rate limit is enforced by GF-307, not by the tick.

**GF-307 — Per-job rate limiter.** In-memory map of job ID to last attempt time. One attempt per job per 5 min. Attempts count on any outcome. Pruned to the active set each pass. Restart resets the map (at most one extra call per job per process start). Rationale: a DB-backed limiter would need a schema change, which D-35 does not justify. Single writer per GF-306 makes the in-memory map safe.

**GF-308 — Assign trigger.** Polling the active-job scan each tick. NATS `job.updated` is rejected: `jobs.events` is core NATS (GF-4), not replayable, so a restart would drop assigns, and ingest would need its own subscription. A newly assigned job is computed within one tick, subject to GF-307. Rationale: no new subscription and no lost events.

**GF-309 — Write-back.** Single `UPDATE jobs SET eta_minutes` guarded by `IS DISTINCT FROM`, clamped to `[0, 1440]`. Silent: no `job_events` row, no `job.updated` publish. No `app.actor_id`, because the trigger guards state and assignment only (verified in G0). Stale ETA is not cleaned on terminal states. The value stays on the row; consumers show ETA only for active states. Rationale: ETA is not a state transition, and re-publishing it would flood the hub. A terminal-state write would add a second writer for no user-visible gain.

**GF-310 — `eta_slip` semantics.** Projected arrival `now + eta_minutes`. Slip iff `window_end` is not NULL and projected > `window_end + 15 min` (strict). Severity `warning`. The check runs only on a fresh computation in the same pass, so no staleness column is needed. Windowless jobs never slip. Rationale: D-34 verbatim. Fresh-only evaluation avoids a `jobs.eta_updated_at` migration.

**GF-311 — Dedup and episode.** One `eta_slip` alert per `(job_id, window_end)`, enforced by `NOT EXISTS` under the ETA lock. No re-arm. A job that recovers and slips again inside the same window stays silent. Rationale: the append-only `alerts` table and the GF-292 episode philosophy. A new window is a new key, so a re-planned job alerts again.

**GF-312 — Alert payload.** `{job_id, vehicle_id, eta_minutes, projected_at, window_end, threshold_min: 15}`, RFC3339 times, severity `warning`. Rationale: enough for the dispatcher to act without a join.

**GF-313 — Rule activation.** `enabled` stays `false` for `eta_slip` in seeds and existing rows. No migration. A manager or admin enables it with `PATCH /alert-rules`. ETA computation runs regardless of the rule, so the UI always has ETA, and the slip check is gated on `enabled`. Rationale: OSRM is not in the e2e stack (GF-247), and an enabled-by-default slip alert would fire in environments without OSRM. Enabling is an explicit operator choice.

**GF-314 — Degradation.** On any failure outcome: skip the job, no ETA write, no slip, increment `fleetops_eta_rule_skipped_total{reason="osrm_unavailable"}`, one log line per pass, no alert. Cloud uses the public demo server through `OSRM_URL` with no code fallback. Rationale: D-10 and D-34. The rule degrades without raising a false alert.

**GF-315 — Metrics.** `fleetops_eta_calls_total{outcome}`, `fleetops_eta_rule_skipped_total{reason}`, `fleetops_eta_pass_duration_seconds`, `fleetops_eta_passes_skipped_total`. Label sets pre-seeded to zero. Registered in ingest's private registry until P22. Rationale: GF-93 and GF-301.

**GF-316 — Route request shape.** `GET {base}/route/v1/driving/{lng},{lat};{lng},{lat}?overview=false`. Origin is `vehicles_latest.geom`. Target is pickup for assigned or accepted, else dropoff. No steps, no alternatives. Rationale: minimum payload. `overview=false` avoids geometry overhead, and steps are not needed.

**GF-317 — `evaluated` flag.** `evaluated` is `true` for every kind, including `eta_slip`. This amends GF-298: P19's `evaluated: false` for `eta_slip` is retired. Rationale: the rule now evaluates. `store.go:220` is the one-line change that makes the API tell the truth.

**GF-318 — No schema change, no new env var beyond `OSRM_URL`.** No migration `012`. No `jobs` column change. The constants (5-min limit, 30 s tick, 15-min threshold, 1440 clamp) are not env vars. Rationale: GF-310 and GF-313 show no migration is needed. Keeping `OSRM_URL` as the only new knob keeps SPEC-AMEND clean.

## Validation

Each check has a pass condition.

- **(a) Gates green.** `make fmt-check`, `make lint`, and `make test` pass. The new `internal/eta` package and the `internal/alerts` changes are covered. `make test` includes the PostGIS integration tests. Pass: zero failures. `internal/hub` follows the isolated re-run policy.
- **(b) Scope fence.** `git diff --name-only main...HEAD` touches only:
  - `deploy/osrm/**`
  - `docs/osrm.md`
  - `internal/eta/**`
  - `internal/config/**` (`OSRM_URL` only)
  - `internal/alerts/store.go` (one line, `evaluated`)
  - `internal/alerts/**` exports only (`RaiseETASlip`, `PublishRaised`) and their tests
  - `services/ingest/**` (wiring only)
  - `CLAUDE.md`
  - `specs/features/20-eta-slip-osrm.md`
  - `prompts/`

  It must NOT touch `README.md`, `e2e/`, `apps/driver-pwa/`, `apps/web/**`, `internal/jobs/**`, `internal/geofence/**`, `specs/constitution/`, `db/migrations/`, `internal/simulator/**`, or `deploy/docker-compose.yml`. Pass: no unlisted paths.
- **(c) No secrets.** `git diff main...HEAD | grep -iE 'secret|password|key=|token'` shows no real values. Fixtures and tests use placeholder strings only.
- **(d) e2e untouched.** `git diff --name-only main...HEAD -- e2e/` is empty. `make e2e` is NOT RUN. Reason: `e2e/scripts/stack.sh` runs `docker compose down -v`, and the driver standing rule forbids it in this phase without explicit approval.
- **(e) CLAUDE.md size.** `wc -l CLAUDE.md` is below 200. The new block is at most 12 lines.
- **(f) Deterministic contract tests.** `internal/eta` tests use only recorded JSON fixtures and `httptest`. Pass: the tests pass with the network disabled.
- **(g) P21 boundary.** `grep -rniE 'daily_driver_stats|/reports|csv' internal/eta services/ internal/alerts` is empty.
- **(h) CI never starts osrm.** `git diff main...HEAD -- .github/ e2e/` is empty.
- **(i) Rate-limit proof.** The limiter test shows at most one OSRM call per job per 5 min, including the property test. Pass: the assertion holds across all iterations.
- **(j) Frontend untouched.** `git diff --name-only main...HEAD -- apps/` is empty. Baselines from G0 are recorded for context.
- **(k) prepare.sh.** `sh -n deploy/osrm/prepare.sh` is clean. The `osrm-customize` step matches compose's `mld`. The real prepare run is NOT RUN, because no PBF is present and the run is operator-manual (D-10).
- **(l) Write-back and alert integration.** The integration tests in G3, G4, and G6 pass against PostGIS.

## Testing depth

| Layer | What | Where | Source |
|---|---|---|---|
| Contract | Recorded OSRM JSON via `httptest`: parse, duration rounding, error codes, timeout, unreachable | `internal/eta/osrm_test.go`, `testdata/*.json` | Roadmap P20 |
| Unit | Rate limiter (fake clock, ≤ 1 per 5 min) and bounded property test (1000 iterations) | `internal/eta/limiter_test.go` | Roadmap P20, D-37 |
| Unit | ETA math, clamp, slip boundary, windowless | `internal/eta/slip_test.go` | D-34 |
| Unit | Metric pre-seeding; config validation for `OSRM_URL` | `internal/eta`, `internal/config` | GF-315 |
| Integration | Write-back updates `jobs.eta_minutes` within the rate limit; CHECK clamp | `internal/eta/loop_integration_test.go` (PostGIS testinfra) | Roadmap P20 |
| Integration | Slip alert fires on a delayed fixture job; dedup on second pass; windowless silent; disabled rule silent | same | Roadmap P20, D-34 |
| Integration | Degradation: OSRM down → no alert, no write, metric incremented; timeout variant | same | Roadmap P20, D-10 |
| API | `GET /alert-rules` shows `evaluated: true` for `eta_slip`; PATCH enables | `internal/alerts/http_test.go` | GF-317 |
| Existing gates | Go suite green (hub flake policy); `apps/web` and `apps/driver-pwa` baselines recorded, untouched | `make test`, npm scripts | Roadmap P20 |

No e2e test is added. GF-247 keeps osrm out of the CI stack.

## SPEC-AMEND repeat

- **New SPEC-AMEND row: none expected.** D-10, D-12, D-34, D-35, and D-39 are already registered. `OSRM_URL` is already in the env list at `tech-stack.md:93` (§5). No migration, no `jobs` column, and no tunable env var is added (GF-318).
- If implementation adds a schema element, a new env var beyond `OSRM_URL`, or a change to the D-34/D-35 semantics, that is a new SPEC-AMEND row. It must be recorded explicitly before merge.
- The GF-317 change to `evaluated` is a GF-298 amendment, not a SPEC-AMEND row, because it changes an API flag and not a constitution decision.
- **GF-192** (hub identity binding) remains open. It belongs to P23.

## Out of scope

- Reports (P21).
- Dashboards and the `/metrics` export (P22).
- k6 (P23).
- OSRM `/trip` and matrix APIs (backlog).
- Simulator changes (D-12).
- Real rerouting or dispatch optimisation.
- README edits (R-8).
- Compose changes (the `osrm` service already exists).
- CI changes (osrm stays out of e2e, GF-247).
- `apps/web` and `apps/driver-pwa` changes.
- Any `jobs` schema change, and any `db/migrations/012`.
- Merge to `main`, push, and any P21+ content.

## Validation results and deviations

Run 2026-10-28 on `phase-20-eta-slip-osrm`, commits `763a56d` (G1) through `3c39135` (G6) and the G7 commit that holds this section, in build order. Host: Go 1.27.3 from `deploy/.local/tools.mk`, golangci-lint v2.15.0, Docker with `postgis/postgis:16-3.4`, Node for the frontend baselines. No compose stack was started.

**G0 baseline (before any edit).** `make fmt-check` exit 0. `make lint` exit 0, 0 issues. `make test` exit 0, 27 `ok` packages, no FAIL. The hub test passed on its first run, so no flake occurred and no isolated re-run was needed. Web: `npm --prefix apps/web run typecheck` exit 0; `npm --prefix apps/web test` 16 files, 88 tests pass. Driver-pwa: `typecheck` exit 0; `npm --prefix apps/driver-pwa test` 19 files, 129 tests pass. Pins: 11 `up` migrations (`001`–`011`); `jobs` has `vehicle_id`, `driver_id`, `pickup`, `dropoff` (`009`) and the `eta_minutes` CHECK 0..1440. `jobs_transition_guard` is `BEFORE UPDATE` and returns early unless state or assignment changes. An ETA-only update therefore needs no `app.actor_id` and writes no `job_events` row (verified by the G3 `events` count test). Decision IDs before P20 end at GF-302 (spec 19); P20 uses GF-303 to GF-318.

**Validation checks.**

- **(a) Gates green. PASS.** `make fmt-check` exit 0. `make lint` exit 0, 0 issues. `make test` exit 0, 27 `ok` packages, including `internal/eta` (28 top-level tests) and `internal/alerts`. PostGIS integration tests ran in that run.
- **(b) Scope fence. PASS.** `git diff --name-only main...HEAD` lists 34 files, all inside the allowed list: `deploy/osrm/{prepare.sh,.gitignore}`, `docs/osrm.md`, `internal/eta/**`, `internal/config/{config.go,config_test.go}` (OSRM_URL only), `internal/alerts/{store.go (one line), eta.go, eta_test.go, publish.go, evaluator.go (call rename), http_test.go}`, `services/ingest/main.go` (wiring), `CLAUDE.md`, `specs/features/20-eta-slip-osrm.md`, `prompts/65`–`67`. `git diff --name-only main...HEAD -- README.md e2e/ apps/ internal/jobs internal/geofence internal/simulator specs/constitution db/migrations deploy/docker-compose.yml .github/` is empty.
- **(c) No secrets. PASS.** `git diff main...HEAD | grep -iE 'secret|password|key=|token'` matches only the existing comment that names `JWT_SECRET`, a test insert with the placeholder `password_hash` `'x'`, and spec or prompt text that quotes this check. No real value is in the diff.
- **(d) e2e untouched. PASS for the diff; `make e2e` NOT RUN.** `git diff --name-only main...HEAD -- e2e/` is empty. `make e2e` was not run because `e2e/scripts/stack.sh` runs `docker compose down -v`, which this run forbids. No compose stack was started.
- **(e) CLAUDE.md size. FAIL in the run, then fixed.** The implement run left `wc -l CLAUDE.md` at 204 (baseline 197) against the `< 200` cap; its `## ETA (P20)` block is 7 lines, under the 12-line cap, and the run correctly refused to delete existing reference lines. A follow-up commit on this branch condensed four pre-existing bullet groups (the two seed lines, the JWT/DEVICE_KEY_SALT lines, the Migration rules group, the Deliver route/Success lines) into fewer lines with no content loss, bringing the file to 197. Under 200. PASS.
- **(f) Deterministic contract tests. PASS.** The pure tests pass inside `unshare -rn` (an empty network namespace with `lo` up), exit 0: `TestRoute*`, `TestMinutesCeilingAndClamp`, `TestLimiter*`, `TestSlipBoundary`, `TestSlipWindowlessNever`, `TestSlipZeroETA`, `TestMetricsPreSeededToZero`. They use recorded fixtures and `httptest` on loopback. The DB-backed tests need the Docker daemon, which an empty namespace cannot reach, so they were not part of the offline run.
- **(g) P21 boundary. PASS.** `grep -rniE 'daily_driver_stats|/reports|csv' internal/eta services/ internal/alerts` has no matches (exit 1).
- **(h) CI never starts osrm. PASS.** `git diff main...HEAD -- .github/ e2e/` is empty.
- **(i) Rate-limit proof. PASS.** `TestLimiterFakeClockTicks`: 3 jobs over 660 s get attempts at exactly 0, 300 and 600 s. `TestLimiterBoundaryIsInclusive`: denied at window − 1 ns, allowed at the window. `TestLimiterPropertyAtMostOneCallPerWindow`: fixed PCG seed, 1000 iterations, 1–8 jobs, up to 120 ticks, jobs may leave the active set. Every gap between attempts is at least 5 min, and the count stays within `span/5min + 1`. Integration `TestLoopWritesETAAndHoldsWindow`: 1 call at t0, no call at +4 min, 1 call at +5 min.
- **(j) Frontend untouched. PASS.** `git diff --name-only main...HEAD -- apps/` is empty. The G0 baselines are recorded above.
- **(k) prepare.sh. PASS for the runnable part; real run NOT RUN.** `sh -n deploy/osrm/prepare.sh` exit 0. The step-4 command is `osrm-customize /data/region.osrm` with no `--algorithm` flag; compose pins `--algorithm mld` on `osrm-routed`. The script's comment states the match. Usage: no argument exits 2 with the usage line; `a;b.pbf` exits 2; a missing file exits 1 and names the path. **Real prepare run: NOT RUN.** `deploy/osrm/data/` holds no PBF, and the run needs Docker and a multi-hundred-MB download (D-10, operator-manual). The `osrm/osrm-backend` image was not pulled on this host. The manual Monaco check in G1 is NOT RUN for the same reason.
- **(l) Write-back and alert integration. PASS.** G3 (PostGIS): `TestLoopWritesETAAndHoldsWindow`, `TestLoopTargetFollowsState`, `TestLoopAssignedJobComputedWithinOneTick`, `TestLoopClampsAtWrite` (90000 s through the router, and a raw 1500 through the loop guard, both written as 1440), `TestLoopMissingPositionSkipsWithoutCall`, `TestLoopLockContentionSkipsPass`. G4 (PostGIS): `TestSlipAlertFiresOnDelayedJob`, `TestSlipDedupOnePerWindow`, `TestSlipNewWindowAlertsAgain`, `TestSlipWindowlessJobIsSilent`, `TestSlipRuleDisabledIsSilentButETAWritten`. G6 (PostGIS and httptest): `TestDegradationSkipsWithoutWriteOrAlert` (unreachable, timeout, http_error, malformed, no_route) and `TestDegradationOneLogLinePerPass`.

**Test groups.** G1: `sh -n` and grep checks. G2: `TestRouteRecordedFixtures` (7 fixtures), `TestRouteRequestShape`, `TestRouteHTTPErrorIsHTTPError`, `TestRouteStalledServerIsTimeout`, `TestRouteClosedListenerIsUnreachable`, `TestRouteOutcomeLabelsAreStable`, `TestMinutesCeilingAndClamp`, and the config `TestLoadOSRMURL*` and `TestConfigErrorsNeverContainValues` cases. G3: limiter and loop tests. G4: slip tests, `TestRaiseETASlipWritesWarningRow`, `TestPublishRaisedEnvelopeForETASlip`. G5: `TestAlertRulesPatch` with the new GET check. G6: `TestMetricsPreSeededToZero`, degradation tests.

**Deviations and interpretations.**

1. **Constitution path.** The prompt names `specs/constitution/constitution.md`, which does not exist. The R-1…R-9 rules are in `specs/constitution/roadmap.md` §1, and that file was used.
2. **File placement.** `Minutes` and the clamp are in `osrm.go`, and their test is in `osrm_test.go`, not in `slip.go` and `slip_test.go` as the file list suggests. No behaviour changed.
3. **`metrics.go` in G3.** The loop calls the counters, so the file is needed with G3. G6 holds the pre-seed test and the degradation tests.
4. **Fixtures are not recorded from a live server.** The `testdata/*.json` files are written in OSRM's response shape. No OSRM image was pulled on this host, so they are unverified against a real `osrm-routed`. Recording them from a real server is an operator follow-up.
5. **`deploy/osrm/.gitignore`.** The root `.gitignore` does not cover `deploy/osrm/data/`, so the new file adds `data/` inside the `deploy/osrm/**` scope.
6. **Volume name.** `prepare.sh` uses `docker run -v <project>_osrm_data:/data`, not `docker compose run`, so it can take `OSRM_IMAGE`. The project defaults to `deploy`. `COMPOSE_PROJECT_NAME` overrides it. `docs/osrm.md` documents this.
7. **`OSRM_URL` in CLAUDE.md.** The env list already names `OSRM_URL` (line 80), so G2 needed no edit there.
8. **`RaiseETASlip` timestamp.** The spec's signature has no time argument, so the wrapper stamps `raised_at` with `time.Now().UTC()`. The evaluator uses the pass time. The loop's `Clock` sets `projected_at`.
9. **No Notifier in the ETA loop.** The notifier acts only on critical alerts (GF-300), and `eta_slip` is a warning. The loop publishes after commit (GF-299) and has no Notifier field.
10. **Publish metrics.** The loop calls `alerts.PublishRaised` with a nil `*alerts.Metrics`. A failed ETA publish is logged but not counted in `fleetops_alert_evaluation_failures_total{stage="publish"}`.
11. **Rename.** `publishRaised` became `PublishRaised` (`publish.go`, and its one call site in `evaluator.go`), as the spec asks for the export.
12. **P19 assertions amended (GF-317).** `TestAlertRulesPatch` asserted `evaluated: false` for `eta_slip`. It now asserts `true`, and it also checks `GET /alert-rules`. The edit is in `internal/alerts/http_test.go`, within the listed test scope.
13. **Slip dedup precision.** The dedup key is `payload->>'window_end'` in RFC3339 (second precision). Two windows that differ only in sub-second parts count as one window. Such data is not expected.
14. **`no_target` is not reachable.** `pickup` and `dropoff` are `NOT NULL`, and the candidate query requires an active state, so the `no_target` branch and its label exist but no test drives them.
15. **Pass lock spans OSRM calls.** A pass holds one transaction and its advisory lock across every OSRM call (up to 2 s each). With many active jobs a pass runs long. This is acceptable for the MVP, and the next tick runs the next pass. Noted for review; not changed.
16. **Spec status line.** Line 3 still says "not implemented". The instruction is to append, not rewrite, so it was left as is.

**Result.** Checks (a), (b), (c), (e), (f), (g), (h), (i), (j), (l), and the runnable part of (k) PASS. Check (d) passes on the diff, and `make e2e` is NOT RUN. The real `prepare.sh` run is NOT RUN. Check (e) initially FAILED (204 lines) and was fixed by the condensation commit described in (e) above (197 lines).
