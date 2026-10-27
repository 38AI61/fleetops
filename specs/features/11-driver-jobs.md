# Feature 11 — Driver job API (P11)

Status: approved plan, persisted 2026-10-27. Branch `phase-11-driver-jobs`.

Source plan: `/root/.claude/plans/p11-plan-snoopy-knuth.md` (approved, self-gate passed with GF-192 accepted). Binding constitution: `specs/constitution/roadmap.md` §1 (R-1…R-9, esp. R-6), §3 P11, `specs/constitution/tech-stack.md` §7.1–§7.2, §8.1, §9, §11 (D-21, D-23, D-24, GF-4), `specs/constitution/mission.md` §7 (the D-23 row owning `POST /driver/jobs/:id/fail`), `specs/features/10-job-core.md` (the GF-173/174/178/179/180 decisions your routes reuse), `AGENTS.md`, `CLAUDE.md`.

This spec is written before any P11 code (R-1, SDD). Implementation starts at Plan task group G0.

## Scope

Roadmap P11 (driver job API), server side only.

- `GET /driver/jobs/today` — the driver's own jobs for today (fleet timezone). Offline cache is P13.
- `POST /driver/jobs/:id/accept`, `/start`, `/pickup`, `/fail` (`fail` requires `reason`). `deliver` is P12.
- Driver is scoped to own jobs only; cross-driver and cross-fleet access returns 404.
- Repeated accept (and every other driver transition) is idempotent (D-24 replay).
- Every driver transition publishes with non-null `driver_id`, so the existing P7 hub driver filter applies. No hub change.
- No migration. Migrations 001–009 already provide everything (confirmed in G0).

## Plan

**G0 — Preflight.**
- `make fmt-check lint test` green on the phase branch before any change.
- `ls db/migrations` ends at `009_jobs_core`.
- GF-183 is the highest decision id in the repo.
- Tests: none new. Pass condition: baseline green, no migration needed.

**G1 — Driver store: identity and today list.** (`internal/jobs/store.go`, new `internal/jobs/driver.go`)
- `resolveDriver(ctx, principal)`: `SELECT d.id FROM users u JOIN drivers d ON d.id = u.driver_id AND d.fleet_id = $2 WHERE u.id = $1`. No row → driver is unlinked.
- `Store.DriverJobs(ctx, fleetID, driverID int64, now time.Time) ([]Job, error)`:
  - Filter: `j.fleet_id = $1 AND j.driver_id = $2 AND j.state NOT IN ('delivered','failed','cancelled') AND (j.window_start IS NULL OR (j.window_start AT TIME ZONE f.timezone)::date <= ($3::timestamptz AT TIME ZONE f.timezone)::date)`, joined to `fleets f`.
  - Order: `j.window_start ASC NULLS LAST, j.id ASC`.
  - Store remains Clock-free: the handler passes `Clock()`; SQL converts to the fleet timezone.
- Tests: `TestDriverStoreTransition` covers the store identity and list paths (G2); the list cases are asserted through HTTP in `TestDriverToday` (G5).

**G2 — Driver store: transition.** (`internal/jobs/driver.go`)
- `Store.DriverTransition(ctx, actor, fleetID, jobID, driverID int64, to State, reason *string) (Change, Outcome, error)`.
- One tx, in order:
  1. `begin(ctx, actor)` (sets `app.actor_id`, GF-180).
  2. `selectJob(..., lock=true)`.
  3. Ownership guard: `job.FleetID == fleetID && job.DriverID != nil && *job.DriverID == driverID`, else `ErrNotFound`. This runs before the replay check.
  4. Replay check: `current.State == to` → `Replayed`, commit, no event. For `fail`, the replay does not write the reason.
  5. `CanTransition(current.State, to)` else `ErrConflict`.
  6. `UPDATE jobs SET state=$1 [, failure_reason=$2] WHERE id AND fleet_id AND state=$from`. Zero rows → re-read (D-24 branch, same as `Transition`).
  7. `appliedChange`, commit.
- `to` must be one of `accepted`, `en_route`, `picked_up`, `failed`. Anything else → `ErrValidation`.
- `Transition` and `Assign` are not modified.
- Tests (`TestDriverStoreTransition`, testcontainers PostGIS):
  - happy path for each edge: `assigned→accepted`, `accepted→en_route`, `en_route→picked_up`, `{accepted,en_route,picked_up}→failed`.
  - ownership: another driver's job, an unlinked driver, a cross-fleet job → `ErrNotFound`, no event written.
  - `fail` writes `failure_reason`.
  - replay → `Replayed`, no event; a second `fail` with a different reason keeps the first.
  - illegal edge (e.g. `assigned→failed`, `accepted→en_route` from `created`) → `ErrConflict`.
  - `to = delivered` or `assigned` → `ErrValidation`.
  - event count per job matches the applied transitions.

**G3 — HTTP routes, wiring, validation.** (`internal/jobs/http.go`, `services/api/server.go`)
- Add `Now func() time.Time` to `jobs.Handler`. Nil means `time.Now`.
- `services/api/server.go` passes `d.Clock` into the jobs handler, so the harness fake clock reaches `today`.
- Register under the jobs group with `auth.RequireAuth` then `auth.RequireRole("driver")`:
  - `GET /driver/jobs/today` → `{"jobs": [...]}`.
  - `POST /driver/jobs/:id/accept | start | pickup` → `bindEmptyObject`.
  - `POST /driver/jobs/:id/fail` → `bindStrict` with `{"reason": string}`.
- Handler flow: principal → resolve driver (unlinked → `404` on transitions, `200 {"jobs":[]}` on today) → `idParam` → bind → store → `writeErr`.
- Responses: `200` with the job object (GF-173 element shape) on applied and replayed.
- Tests: `TestDriverTransitionValidation` (see Testing depth).

**G4 — Role matrix (R-6).** (`services/api/rolematrix_test.go`)
- Five new rows, `roleCols` order (admin, manager, dispatcher, driver, unauthenticated):
  - `GET /driver/jobs/today` → `[403,403,403,200,401]`.
  - `POST /driver/jobs/:id/accept`, `/start`, `/pickup`, `/fail` → `[403,403,403,2xx,401]` with per-row `jobRowSetup` fixtures.
- Transition fixtures: a fresh job in `assigned` (or pre-advanced for start/pickup/fail) assigned via `Store.Assign` to a vehicle and to the driver linked to the matrix's driver user. The `driver` user needs `users.driver_id` set in the fixture.
- Test: `TestRoleMatrix` (extended).

**G5 — Isolation, replay, list, and validation HTTP tests.** (`services/api`)
- `TestDriverCrossDriverIsolation`, `TestDriverReplay` tests as named below.

**G6 — Publish.** (`internal/jobs/publish.go` reused, call sites in `driver.go`/`http.go`)
- Each applied driver transition calls `publishChange` after commit. Same subject and body as GF-179; `driver_id` is always non-null.
- Replay, 409, and 404 publish nothing.
- Test: `TestDriverJobsPublish` (NATS subscription, as in P10's `TestJobsPublishOnePerTransition`).

**G7 — Docs.** (`CLAUDE.md`, `specs/features/11-driver-jobs.md`)
- Extend the Jobs (P10) block in `CLAUDE.md` with the five routes, the driver-identity rule, the today semantics, and the fail-reason rule. File stays under 200 lines.
- No env vars, make targets, or migrations.

**G8 — Full validation.** Run every check in the Validation section.

## Requirements

### Binding (carried from the constitution and P10)

- **D-23**: transition edges come from `internal/jobs/transitions.go`; `job_transitions` mirrors them. Driver edges: `assigned→accepted`, `accepted→en_route`, `en_route→picked_up`, `{accepted,en_route,picked_up}→failed`. `deliver` is not in P11.
- **D-24**: replay of the current state returns `Replayed` with no event; a non-edge returns `ErrConflict`.
- **D-21**: the hub filters `job.updated` by the envelope `driver_id`; P11 relies on this and does not change it.
- **GF-4**: `jobs.events.<fleet_id>` on core NATS, no JetStream stream.
- **R-6**: role-matrix rows for every new route.
- **Mission §7**: the D-23 SPEC-AMEND row already owns `POST /driver/jobs/:id/fail`.
- **GF-180**: actor is set via `set_config('app.actor_id', …)` as the first statement of the tx; the actor is the user id.
- **GF-174**: `day` semantics are `(window_start AT TIME ZONE fleets.timezone)::date`.
- **GF-179**: publish after commit, one message per applied transition, body with `driver_id`.
- **GF-178**: error mapping (`400 validation_failed`, `404 not_found`, `409 conflict`).
- **GF-173**: job object shape.
- **GF-28**: role checks run before body read.

### New decisions

- **GF-184 — Driver identity.** The caller's driver row is `users.driver_id` joined with `drivers.fleet_id = principal.FleetID`. Unlinked driver: `today` → `200 {"jobs":[]}`; transitions → `404`. The actor recorded in `job_events` is the user id (GF-180). *Why:* a driver user without a driver row has no jobs, so it sees nothing and cannot mutate anything.
- **GF-185 — Today semantics.** The handler passes `Clock()` to `DriverJobs`; SQL converts to `fleets.timezone`. Included: non-terminal jobs whose `window_start` day is on or before today, plus non-terminal jobs with NULL `window_start`. Excluded: `delivered`, `failed`, `cancelled`, and future-dated jobs. Ordering `window_start ASC NULLS LAST, id ASC`. Response `{"jobs": [...]}`. No query parameters are defined; unknown ones are ignored (as `filterFrom` does). *Why:* a carried-over accepted or en_route job from yesterday is still the driver's active work and must not vanish; undated active work must not vanish either. Completed work belongs to reports (P21).
- **GF-186 — Route roles.** All five routes are driver-only: `RequireRole("driver")`. Admin, manager, and dispatcher get `403`; unauthenticated gets `401`; the role check runs before body read. *Why:* tech-stack §7.1 marks the routes "JWT driver"; the README role matrix gives drivers only their own jobs.
- **GF-187 — Ownership guard.** A driver transition requires, under the row lock: `job.fleet_id = principal.FleetID` and `job.driver_id = resolved driver id`. Anything else → `ErrNotFound`. The guard runs before the replay check, so a non-owner cannot observe state through a replay `200`. *Why:* the roadmap's "driver cannot touch another driver's job" is exactly this guard, and it leaks no existence across drivers or fleets.
- **GF-188 — Endpoint contracts.** `accept`, `start`, `pickup`: empty body or `{}`; any field → `400`. `fail`: `{"reason": string}` required, trimmed, non-empty, at most 1000 runes (the GF-173 notes cap); unknown fields → `400`. Malformed `:id` → `404` (`idParam` idiom). Success → `200` with the job object for applied and replayed calls.
- **GF-189 — Fail persistence.** `failure_reason` is written by the same `UPDATE` as the state change. The trigger guards only state and assignment, so the reason write is unrestricted, and it is inside the same tx as the state change. `fail` is valid only from `accepted`, `en_route`, `picked_up`; from `assigned` it is `409`. Replay is state-only (D-24): a repeated `fail` returns `200` with no event, and the first reason stays durable even if the second differs. The reason is not written to `job_events` or the NATS body (the trigger records only the state change). *Why:* one UPDATE keeps reason and state atomic; the event log schema is not extended in P11 (no migration).
- **GF-190 — Store shape.** New methods `DriverJobs` and `DriverTransition` only. `Transition`, `Assign`, `Change`, `Outcome`, and `begin` are reused unchanged. `DriverTransition` repeats the D-24 replay block (about 15 lines) rather than refactoring `Transition`, which keeps the P10 path untouched. The shared behaviour is locked by `TestDriverStoreTransition` replay cases. `Handler` gains a `Now` field; `NewServer` wires `d.Clock` in. *Why:* the P10 path stays green without edits; the clock seam is the one existing harness hook.
- **GF-191 — Publish.** Same subject and body as GF-179; `driver_id` non-null on every driver transition (a job reaches `assigned` only via `Assign` with a driver). One message per applied transition after commit. No message on replay, `409`, or `404`.
- **GF-192 — Hub `/stream` consequence (no code change).** The hub matches the token subject (`users.id`) against the published `driver_id` (`drivers.id`). These are different id spaces, so the P7 driver filter will not deliver a driver's own `job.updated` unless the ids are bound. P11 makes no hub change and records this as a known integration gap, closed by the identity binding in P13/P23. `/stream` filter tests stay as they are in `internal/hub`.
- **GF-193 — Role-matrix rows and CLAUDE.md.** Five rows (GF-186 cells, GF-188 fixtures); CLAUDE.md gets mechanical rows only (routes, identity rule, today semantics, fail-reason rule), file under 200 lines, no env vars or make targets.

## Validation

Every check runs on this host. Docker is available; compose is not started.

1. `make fmt-check` — exits 0.
2. `make lint` — exits 0.
3. `make test` — exits 0 with `-race -count=1`; PostGIS and NATS testcontainers run.
4. Named tests pass with `-race -count=1`:
   - `go test ./services/api -run 'TestDriver|TestRoleMatrix' -race -count=1` — exits 0.
   - `go test ./internal/jobs -run 'TestDriverStoreTransition' -race -count=1` — exits 0.
5. Scope fence: `git diff --name-only main...HEAD` lists only paths under `internal/jobs/`, `services/api/`, `CLAUDE.md`, `specs/features/11-driver-jobs.md`, `prompts/`. Must not touch `README.md`, `deploy/`, `specs/constitution/`, `db/`, `internal/hub/`, `internal/mapview/`, `apps/`, `go.mod`, `go.sum`.
6. `ls db/migrations` still ends at `009_jobs_core`.
7. `wc -l CLAUDE.md` < 200.
8. No secret values in the diff (`git diff main...HEAD | grep -iE 'secret=|password=|BEGIN [A-Z ]*PRIVATE'` returns nothing).

## Testing depth

| Layer | Test | Scope |
|---|---|---|
| Unit | none new (P11 adds no property test) | — |
| Integration (testcontainers PostGIS, `internal/jobs`) | `TestDriverStoreTransition` | each driver edge; ownership guard (other driver, unlinked, cross-fleet → `ErrNotFound`); `fail` writes reason; replay → `Replayed`, no event; illegal → `ErrConflict`; `to` = `delivered`/`assigned` → `ErrValidation`; event count per job |
| Services/API harness (`services/api`) | `TestDriverToday` | own jobs scheduled today in a non-UTC fleet timezone (fake `Deps.Clock`); undated active job included; carried-over active job included; future-dated excluded; `delivered`/`failed`/`cancelled` excluded; other driver's jobs excluded; unlinked driver → `200 {"jobs":[]}`; envelope and ordering |
| Services/API harness | `TestDriverCrossDriverIsolation` | A's accept/start/pickup/fail on B's job → `404`, state and event count unchanged; A's `today` excludes B's and includes A's; cross-fleet job → `404` |
| Services/API harness | `TestDriverReplay` | accept twice → two `200`, one `assigned→accepted` event; start/pickup replayed → `200`, no event; fail twice → two `200`, one event, reason unchanged by second call |
| Services/API harness | `TestDriverTransitionValidation` | `fail` with no body, empty reason, whitespace reason, 1001 runes, unknown field → `400`; accept on `created` → `409`; malformed id → `404`; non-driver roles → `403` before body read |
| Services/API + NATS | `TestDriverJobsPublish` | each applied driver transition → exactly one message on `jobs.events.<fleet_id>` with non-null `driver_id`; replay, `409`, `404` → none |
| Role matrix (R-6) | `TestRoleMatrix` (extended) | five new rows, driver-only cells |
| Race | `make test` with `-race` | all packages |
| Migration round trip | `db/roundtrip_test.go` | unchanged, stays green |

## SPEC-AMEND repeat

`POST /driver/jobs/:id/fail` (additive endpoint, reason required) is the D-23 SPEC-AMEND that mission §7 already records for P11. This phase adds no new SPEC-AMEND row.

## Out of scope

- `POST /driver/jobs/:id/deliver`, proof uploads, MinIO, `delivery_geom` writers (P12).
- Driver PWA and offline cache (P13).
- Hub or fanout changes, including the GF-192 identity binding (P13/P23).
- Mapview changes.
- `PATCH` / `DELETE /jobs`, pagination.
- README, `deploy/PORTS.md`, compose, or constitution edits.
- Migrations or trigger changes (the event log does not carry `failure_reason`).
- Merge to main, push.
