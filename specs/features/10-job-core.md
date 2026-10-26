# Feature 10 — Job core & state machine (P10)

Status: approved plan, persisted 2026-10-26. Branch `phase-10-job-core`.

Source plan: `/root/.claude/plans/p10-plan-quizzical-shamir.md` (approved, self-gate passed with no amendments). Binding constitution: `specs/constitution/roadmap.md` §1 (R-1…R-9, esp. R-7), §3 P10, §5 (append-only gate), `specs/constitution/tech-stack.md` §6.1–§6.3, §7.1–§7.2, §8.1, §9, §11 (D-17, D-23, D-24, D-26, GF-4), `specs/constitution/mission.md` §7 (D-23 and D-26 SPEC-AMEND rows), `specs/features/08-map-api-simulator.md` (GF-133 JobLookup seam your package implements), `AGENTS.md`, `CLAUDE.md`.

This spec is written before any P10 code (R-1, SDD). Implementation starts at Plan task group G0.

## Scope

Roadmap phase P10 (`specs/constitution/roadmap.md` §3) on branch `phase-10-job-core`, built on merged P9.

In scope:
- Migration `009_jobs_core` (up/down): `jobs` per README §5 plus D-26 additive columns; `job_transitions` reference table seeded with the 12 D-23 edges; `job_events` append-only; `BEFORE UPDATE` transition guard on `jobs`; `AFTER INSERT` creation event on `jobs`.
- `internal/jobs`: transition source (`transitions.go`), store (create/get/list/transition/assign/cancel with D-24 semantics), HTTP handlers + `Register`, NATS publisher, `mapview.JobLookup` implementation.
- Dispatcher endpoints (JWT): `POST /jobs`, `GET /jobs` (state, driver, day), `POST /jobs/:id/assign`, `POST /jobs/:id/cancel` (SPEC-AMEND).
- Publish `jobs.events.<fleet_id>` on core NATS per successful transition.
- Wiring in `services/api/server.go` and `services/api/main.go`; role-matrix rows; CLAUDE.md Jobs block.

Out of scope: see the final section.

## Plan

Build order. Each group lists its tests. A group is done when its tests pass locally.

**G0 — Preflight.** Confirm `git branch --show-current` is `phase-10-job-core`. Run `make fmt-check`, `make lint`, `make test` and record a green baseline. Confirm there is no `jobs` object in `db/migrations/001–008` and that GF-169 is the highest decision ID. Read `services/api/server.go`, `internal/auth` (`RequireRole` signature), `internal/registry/http.go` (`Register` role-group construction), and the repo logger call used on 5xx paths. No tests; this group only confirms the assumptions below.

**G1 — Migration 009 (`db/migrations/009_jobs_core.up.sql` / `.down.sql`).**
- Create `jobs` with the README §5 columns, types and defaults, plus D-26 columns, constraints and index per GF-170.
- Create `job_transitions(from_state, to_state, PRIMARY KEY (from_state, to_state))`, seeded with the 12 D-23 edges.
- Create `job_events` with an append-only trigger per GF-171 (mirror the migration-003 function + two triggers).
- Create the `BEFORE UPDATE` guard on `jobs` and the `AFTER INSERT` creation trigger per GF-171.
- Down file drops triggers, functions, `job_events`, `job_transitions`, `jobs` (in that order).
- No `BEGIN`/`COMMIT`; no `CREATE INDEX CONCURRENTLY` (GF-5).
- Tests: `db/roundtrip_test.go` updated for version 9 and the new objects (fence amendment, GF-170). `db/migrations_test.go` filename/pair checks stay green.

**G2 — Transition source and DB guard tests.**
- `internal/jobs/transitions.go`: `State` constants (8 states), `Edge{From, To}`, `Edges` (12 edges), `CanTransition(from, to State) bool`. This file is the only Go source of the table.
- Tests:
  - `TestTransitionEdges` (unit): exactly the 12 D-23 edges are true; a sample of illegal pairs (`created→accepted`, `delivered→failed`, `cancelled→assigned`, `assigned→created`) are false.
  - `TestJobTransitionsMirror` (integration, testcontainers PostGIS, `testinfra.NewMigratedDB`): `SELECT from_state, to_state FROM job_transitions` set-equals `jobs.Edges`.
  - `TestJobsTriggerRejectsInvalidUpdate` (integration): direct `UPDATE jobs SET state='delivered'` from `created` → SQLSTATE `FL409`; direct state change with no `app.actor_id` on a valid edge → `FL428`; a non-event update (`notes`) with no actor succeeds; a `fleet_id` change → `23514`.
  - `TestJobEventsAppendOnly` (integration): direct `UPDATE`, `DELETE` and `TRUNCATE` on `job_events` → SQLSTATE `23514` with message `job_events is append-only`.

**G3 — Job store (`internal/jobs/store.go`).**
- `Create(ctx, actor, NewJob)`: one tx; `set_config('app.actor_id', $uid, true)`; `INSERT … RETURNING` (state `created`; the AFTER INSERT trigger writes the creation event).
- `Get(ctx, fleetID, id)` and `List(ctx, fleetID, Filter)`: fleet-scoped (`WHERE fleet_id = $1`).
- `Transition(ctx, actor, fleetID, jobID, to) (Change, Outcome, error)`: generic state edge for any `to` except `assigned`. Implements D-24 literally: tx; set actor; `SELECT … FOR UPDATE`; `UPDATE jobs SET state=$to WHERE id=$1 AND fleet_id=$2 AND state=$from`; zero rows → re-read; current == target → `Replayed` (no event); otherwise `ErrConflict`.
- `Assign(ctx, actor, fleetID, jobID, vehicleID, driverID)`: the assign and reassign rules in GF-175. Same guard shape.
- `Cancel(ctx, actor, fleetID, jobID)`: `Transition` to `cancelled` with the replay rule in GF-176.
- `Change` carries `Job`, `From`, `To`, `At` (read from the `job_events` row written in the same tx before commit). `Outcome` is `Applied` or `Replayed`.
- Tests: `TestJobStoreTransitionD24` (integration): applied → one event; replay → `Replayed`, no event; illegal → `ErrConflict`, no row changed; cross-fleet id → `ErrNotFound`.

**G4 — HTTP endpoints and wiring (`internal/jobs/http.go`).**
- `Handler{Svc}` with `Register(e *echo.Echo, authn echo.MiddlewareFunc)`; role groups built like `registry.Handler.Register`. Roles per GF-177.
- Body decoding via a strict decoder (unknown fields → 400), as `registry.bindStrict` does. Empty body accepted only for cancel.
- Error mapping per GF-178.
- `services/api/server.go`: add `Events jobs.Publisher` to `Deps`; construct `&jobs.Handler{…}` and `Register` it; set `mapview.Handler.Jobs = &jobs.MapLookup{DB: d.DB}` (GF-133 wiring).
- `services/api/main.go`: pass the existing `nc` as `Deps.Events`. No new stream; no new env var.
- `services/api/harness_test.go`: `startServer` also connects core NATS with `testinfra.NATSURL` and passes it as `Events`.
- Tests (`services/api`, real `NewServer` on loopback):
  - `TestJobsCreateValidation` (table): valid minimal; valid full; missing pickup/dropoff; lat 91; lng -181; window_end ≤ window_start; eta_minutes -1 and 1441; notes 1001 chars; size_class `huge`; weight_kg 0 and negative; unknown field; malformed JSON → 201 or 400 `validation_failed` as specified.
  - `TestJobsListFilters`: state filter (valid and unknown state → 400); driver filter (valid, non-existent → empty 200, `abc` → 400); day filter with a fleet timezone that is not UTC (job at `2026-10-26T23:30:00Z` in `Europe/Istanbul`-like fleet → next day); NULL window excluded only when `day` is given; cross-fleet jobs never listed.
  - `TestJobsCrossFleet404`: assign, cancel, list-by-id: job from another fleet → 404; vehicle or driver from another fleet → 404.

**G5 — Assign and cancel behavior.**
- Tests:
  - `TestJobsAssignReplayAndReassign`: created → assigned (201-style 200, one event); same vehicle+driver again → 200, no event; different driver → one `assigned→assigned` event; assign to a vehicle in `maintenance` → 409 `vehicle_not_active`; assign on `accepted` job → 409 `conflict`.
  - `TestJobsCancelReplay`: cancel from `created`, `assigned`, `accepted` → 200, one event each; cancel again → 200, no event; cancel from `en_route` → 409; body `{"x":1}` → 400.
  - `TestJobsIllegalTransitionPersistsNothing`: each illegal request → 409 and `SELECT count(*) FROM job_events` and the `jobs` row unchanged (roadmap Validation item).
  - `TestJobsConcurrentCancel`: two parallel cancels of the same `assigned` job → exactly one `200` with event and the other `200` as replay; zero or one event total for that transition.

**G6 — NATS publishing (`internal/jobs/publish.go`).**
- After commit only; subject `jobs.events.<fleet_id>` (decimal); core `Publish`; failure logged and the HTTP response unchanged (GF-179).
- Tests: `TestJobsPublishOnePerTransition` (`services/api`): subscribe `jobs.events.*` on a separate conn; each applied transition → exactly one message with the exact body fields; replay and 409 → none (Flush then `NextMsg` with timeout). Also: creation publishes nothing. Also: publish failure does not change the response (unit with a failing publisher stub).

**G7 — `mapview.JobLookup` (`internal/jobs/maplookup.go`).**
- `MapLookup.ActiveJobStates` per GF-181; `mapview` code untouched.
- Tests: `TestJobLookupPrecedence` (integration): en_route beats picked_up beats accepted/assigned; delivered/failed/cancelled ignored; other fleet's jobs ignored. `TestMapShowsJobState` (`services/api`): vehicle with `en_route` job → `GET /map/vehicles` shows `en_route`; with `picked_up` job → `en_route`; with `accepted` job → not `en_route`.

**G8 — Role matrix (R-6).**
- Add four rows to `services/api/rolematrix_test.go` (GF-177). Rows that need a job id get a per-row fixture hook (a `setup` func returning the path) so each row starts from a fresh `created` job.
- Tests: `TestRoleMatrix` (extended rows).

**G9 — Rapid property test (`internal/jobs/transition_property_test.go`).**
- `rapid.Check` with `testinfra.NewMigratedDBFrom(t)` per check (the `pairing_property_test.go` idiom). Each check: one fleet, one admin actor, 2–4 active vehicles, 1–3 drivers, 1–5 jobs.
- Random ops: `create`; `assign(job, vehicle, driver)`; `transition(job, to)` for `to ∈ {accepted, en_route, picked_up, delivered, failed, cancelled}`; `cancel(job)`; `replayLast(job)`; `invalidAttempt(job, to)` drawn from non-edges.
- Model tracks each job's expected state from `CanTransition` and counts successful outcomes.
- Invariants checked after each op and at end:
  1. Persisted transitions ⊆ `job_transitions`: every `job_events` row with non-null `from_state` has `(from_state, to_state)` in `job_transitions`.
  2. Event count per job == 1 (create) + successful transitions (`Applied` outcomes).
  3. Replaying the last successful transition returns `Replayed` with no new event.
- Invalid attempts → `ErrConflict`, no event written, state unchanged.
- Cross-fleet op → `ErrNotFound`.

**G10 — CLAUDE.md Jobs block (GF-181 rows in CLAUDE.md per GF-183).**

**G11 — Full validation run** (see Validation).

## Requirements

### Binding decisions (carried from the constitution)

**D-23** (tech-stack §11, SPEC-AMEND; mission §7 rows for `job_transitions` table and `POST /jobs/:id/cancel`):
> Job transitions: `created→assigned` (assign), `assigned→assigned` (reassign), `assigned→accepted`, `accepted→en_route`, `en_route→picked_up`, `picked_up→delivered`, `{accepted,en_route,picked_up}→failed`, `{created,assigned,accepted}→cancelled`. Actors: dispatcher/admin assign and cancel; driver accept, start, pickup, deliver, fail on own jobs. Single source is `internal/jobs/transitions.go`; `job_transitions` mirrors it for the trigger. **SPEC-AMEND** (additive table, cancel endpoint).

**D-24** (tech-stack §11):
> Idempotent transitions: `UPDATE … WHERE id=$1 AND state=$from`; zero rows and current state == target → 200 with no new event; otherwise 409. Only the trigger writes `job_events`; actor passed by `SET LOCAL app.actor_id`.

**D-26** (tech-stack §11, SPEC-AMEND; mission §7 row for `jobs` additive columns, P10 and P17):
> Additive job columns: `notes`, `size_class`, `weight_kg`, `delivery_geom geography`, `failure_reason`, `dwell_min` on geofences. Additive vehicle column: none. **SPEC-AMEND** (additive, schema only).

**D-17** (tech-stack §11, SPEC-AMEND):
> Transport split: Redis pub/sub carries `position.batch` and `presence.changed` (per §3, §4, §12). NATS carries `jobs.events` and `alerts` (per §7). The §7 row putting `positions.batch.<fleet_id>` on NATS is superseded by §3 and §4. **SPEC-AMEND.**

**GF-4** (tech-stack §11):
> `jobs.events.*` and `alerts.*` use core NATS (no JetStream stream). Durable record is in PG; WS delivery is best-effort with polling fallback.

**R-7** (roadmap §1): Append-only tables are enforced by trigger in the phase that creates them: `admin_audit` (P2), `job_events` (P10), `geofence_events` (P18), `alerts` (P19).

**Mission §7 D-23 row:** `job_transitions` reference table, read by the state trigger (new) — SPEC-AMEND additive table, P10.

**Roadmap P10 scope (binding wording):** `jobs` additive columns; `job_events` append-only (trigger); `job_transitions` reference table + `BEFORE UPDATE` trigger (writes events, rejects invalid transitions; actor via `SET LOCAL app.actor_id`); dispatcher `POST /jobs`, `GET /jobs` (state, driver, day filters), `POST /jobs/:id/assign` (also reassign while assigned), `POST /jobs/:id/cancel` (SPEC-AMEND); publish `jobs.events.<fleet_id>`.

**GF-133** (feature 08): the jobs tables implement `mapview.JobLookup`.

### Decisions (GF-170…GF-182)

Numbering follows the 13 open decisions in the plan prompt.

**GF-170 — Migration 009 shape and constraints.** One up/down pair, `009_jobs_core`. Columns and types exactly as README §5: `id BIGSERIAL PK`, `fleet_id BIGINT NOT NULL`, `state TEXT NOT NULL DEFAULT 'created'`, `vehicle_id BIGINT`, `driver_id BIGINT`, `pickup geography(Point,4326) NOT NULL`, `dropoff geography(Point,4326) NOT NULL`, `window_start TIMESTAMPTZ`, `window_end TIMESTAMPTZ`, `eta_minutes INT`, `proof_photo TEXT`, `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`, `delivered_at TIMESTAMPTZ`; index `jobs_fleet_state_idx (fleet_id, state)` as in README. D-26 columns: `notes TEXT`, `size_class TEXT`, `weight_kg NUMERIC(10,2)`, `delivery_geom geography(Point,4326)`, `failure_reason TEXT`, all nullable. Table-level constraints (additive to the README DDL; they enforce README §2.1.5 and D-23 and do not change column shapes):
- `FOREIGN KEY` `fleet_id → fleets(id)`, `vehicle_id → vehicles(id)`, `driver_id → drivers(id)`.
- `jobs_state_vocab`: `state IN ('created','assigned','accepted','en_route','picked_up','delivered','failed','cancelled')`.
- `jobs_assignment_consistent`: `state IN ('created','cancelled') OR (vehicle_id IS NOT NULL AND driver_id IS NOT NULL)`.
- `jobs_window_order`: `window_start IS NULL OR window_end IS NULL OR window_end > window_start`.
- `jobs_eta_range`: `eta_minutes IS NULL OR eta_minutes BETWEEN 0 AND 1440`.
- `jobs_weight_positive`: `weight_kg IS NULL OR weight_kg > 0`.
- `jobs_size_class`: `size_class IS NULL OR size_class IN ('small','medium','large')`.
- `jobs_notes_len`: `notes IS NULL OR char_length(notes) <= 1000`.
- Extra index `jobs_fleet_window_idx (fleet_id, window_start)` for the day filter.
- No PostGIS index on `delivery_geom` (no P10 reader; P11/P12 decide).

`job_transitions(from_state TEXT, to_state TEXT, PRIMARY KEY(from_state,to_state))`, both columns CHECK-constrained to the 8-state vocabulary; seeded with the 12 D-23 edges in the up file.

`job_events(id BIGSERIAL PK, job_id BIGINT NOT NULL REFERENCES jobs(id), from_state TEXT NULL, to_state TEXT NOT NULL, actor_id BIGINT NOT NULL REFERENCES users(id), at TIMESTAMPTZ NOT NULL DEFAULT now())`; `from_state` NULL only for the creation row; both state columns CHECK-constrained to the vocabulary. `actor_id` NOT NULL: the trigger refuses to write an event without an actor (GF-171), so the column shape and the trigger agree. Index `job_events_job_id_idx (job_id, id)`.

Down file drops in order: `job_events` triggers and table, `jobs` triggers and table, `job_transitions`, then functions.

Fence amendment (flagged): `db/roundtrip_test.go` hardcodes version 8 and an object list; it is outside the P10 fence list, so the scope fence is amended to add exactly that file, touched only for version 9 and the new objects. The roadmap requires the round trip to stay green, so this change is needed.

**GF-171 — Trigger semantics and SQLSTATEs.**
- Custom SQLSTATEs (5 chars, mirroring the 003 `RAISE … USING ERRCODE` idiom):
  - `FL409` — illegal transition or illegal assignment-column change. Mapped to HTTP 409.
  - `FL428` — an event-writing change without `app.actor_id`. Mapped to HTTP 500 (programming error; the API always sets the actor).
  - Reason: 003's `check_violation` (`23514`) maps to 400 in `mapWriteErr`; using it for transitions would return 400 where D-24 requires 409.
- `jobs_transition_guard()` `BEFORE UPDATE ON jobs FOR EACH ROW`:
  1. `NEW.fleet_id <> OLD.fleet_id` → `23514` "jobs.fleet_id is immutable".
  2. `NEW.state <> OLD.state`: require `(OLD.state, NEW.state)` in `job_transitions`, else `FL409`. Assignment columns may change only on `created→assigned`; on any other state change, `vehicle_id`/`driver_id` must be unchanged, else `FL409`. Write one event.
  3. `NEW.state = OLD.state` and assignment columns changed: allowed only when `OLD.state='assigned'` and `assigned→assigned` is in `job_transitions`; write one `assigned→assigned` event. Otherwise `FL409`.
  4. Neither state nor assignment changed (pure no-op or non-assignment column such as `notes`/`eta_minutes`/`proof_photo`): no event, no actor requirement.
  5. When an event is written: read `NULLIF(current_setting('app.actor_id', true), '')::BIGINT`; NULL → `FL428`; else `INSERT INTO job_events(job_id, from_state, to_state, actor_id) VALUES (OLD.id, OLD.state, NEW.state, actor)`.
- `jobs_record_create()` `AFTER INSERT ON jobs FOR EACH ROW`: creation is recorded as an event with `from_state NULL`, `to_state 'created'`, same actor rule (no actor → `FL428`). Creation is not a transition: it is not in `job_transitions`, is not published (GF-179), and is excluded from the D-24 replay logic.
- `job_events_append_only()` `BEFORE UPDATE OR DELETE ... FOR EACH ROW` and `BEFORE TRUNCATE ... FOR EACH STATEMENT`: `RAISE EXCEPTION 'job_events is append-only' USING ERRCODE = 'check_violation'` — the 003 idiom, verbatim except names.
- Timestamps come from the DB `now()`, not the API's injected clock.

**GF-172 — `internal/jobs` package layout.**
- `transitions.go` (single source, GF-171 mirror), `store.go` (Create/Get/List/Transition/Assign/Cancel; `Change`, `Outcome`, `ErrNotFound`, `ErrConflict`, `ErrValidation`, `ErrVehicleNotActive` sentinels), `http.go` (handlers + `Register`), `publish.go` (`Publisher` interface and subject/body), `maplookup.go` (`MapLookup`), plus `*_test.go`.
- The generic store-level `Transition` is exported within the package and used by the property test to drive driver edges (`accepted→en_route`, `en_route→picked_up`, `picked_up→delivered`, `*→failed`) that have no endpoint until P11. `Transition` refuses `to == assigned` with `ErrValidation`; `assigned` is reachable only via `Assign`, which carries the vehicle/driver payload the CHECK requires.
- `Store` takes `*sql.DB`; `Service` is not introduced (`Handler` calls `Store` directly, as registry does with its service struct).

**GF-173 — Endpoint contracts.**
- `POST /jobs` body (strict decode):
  - `pickup`, `dropoff`: required objects `{"lat": number, "lng": number}`; `lat ∈ [-90, 90]`, `lng ∈ [-180, 180]`; unknown keys inside a point → 400.
  - `window_start`, `window_end`: optional RFC3339; if both present, `window_end > window_start`.
  - `eta_minutes`: optional integer `0..1440`.
  - `notes`: optional string ≤ 1000 characters.
  - `size_class`: optional, `small|medium|large`; absent or null → NULL.
  - `weight_kg`: optional number, `> 0` and `≤ 100000`.
  - No `vehicle_id`/`driver_id`/`state` in create: every created job is `state=created` and unassigned. Unknown fields → 400.
  - Success: `201` with the job object.
- Job object: `{id, fleet_id, state, vehicle_id, driver_id, pickup:{lat,lng}, dropoff:{lat,lng}, window_start, window_end, eta_minutes, notes, size_class, weight_kg, failure_reason, delivered_at, created_at}`. Nullable fields emit `null`. Coordinates come from `ST_Y/ST_X` of the geography columns. `proof_photo` and `delivery_geom` are not emitted (P12 owns proofs; `delivery_geom` has no P10 writer).
- Timestamps: UTC, RFC3339, millisecond precision, `Z` suffix (the GF-130 encoding used by the map API).
- `GET /jobs`: `200 {"jobs": [ … ]}`. The envelope lets pagination fields be added later without a breaking change. No pagination in P10 (out of scope). Ordering `ORDER BY id ASC`. No cap.
- `POST /jobs/:id/assign` → `200` job object (both applied and replay). `POST /jobs/:id/cancel` → `200` job object (both applied and replay).

**GF-174 — `GET /jobs` filter semantics.**
- `state`: must be one of the 8 vocabulary values; unknown → 400 `validation_failed`.
- `driver`: positive integer id; malformed or non-positive → 400. A well-formed id that matches no driver in the fleet → 200 with an empty list (filters do not 404).
- `day`: strict `YYYY-MM-DD`; malformed → 400. It means the scheduled day: `window_start` evaluated in the caller's fleet timezone (`fleets.timezone`, migration 004, default `UTC`): `(j.window_start AT TIME ZONE f.timezone)::date = $day`. Chosen over `created_at` because dispatch plans by the scheduled window. Chosen over the caller's wall clock because the fleet timezone is the authority.
- NULL `window_start`: excluded when `day` is given; included when it is not.
- Filters are ANDed. Every query is `WHERE j.fleet_id = $principal.FleetID`.

**GF-175 — Assign semantics.**
- Body `{"vehicle_id": int, "driver_id": int}`, both required positive integers, unknown fields → 400.
- Order of checks in one tx (`set_config` first; `SELECT … FOR UPDATE` on the job, `fleet_id` scoped):
  1. Job not in fleet → 404 `not_found`.
  2. Payload-aware replay: `state='assigned'` and vehicle and driver equal the request → `200` no event, nothing published. Vehicle status is not rechecked on replay.
  3. State check: `state ∈ {created, assigned}` required; otherwise 409 `conflict`.
  4. Vehicle in fleet (`vehicles WHERE id AND fleet_id`) else 404 `not_found`; `status != 'active'` → 409 `vehicle_not_active`.
  5. Driver in fleet (`drivers WHERE id AND fleet_id`) else 404 `not_found`. Drivers have no status column, so no status check.
  6. `created`: `UPDATE jobs SET vehicle_id, driver_id, state='assigned' WHERE id AND fleet_id AND state='created'` → one `created→assigned` event. `assigned` with different vehicle or driver: `UPDATE jobs SET vehicle_id, driver_id WHERE id AND fleet_id AND state='assigned'` → one `assigned→assigned` (reassign) event. Zero rows → 409 `conflict` (lost race).
- Roadmap's "reassign while created|assigned" is read as D-23 exactly: `created→assigned` for assign, `assigned→assigned` for reassign. D-23 has no `created→created` edge.
- Replay refines D-24's state-only rule: same state and same target payload → 200 no event. Same state with a different payload is a reassign, not a replay (only possible in `assigned`).

**GF-176 — Cancel semantics.**
- Body: empty, or `{}`. Any field → 400. Empty body is accepted without a decode error.
- From `created`, `assigned`, `accepted` → `cancelled` (D-23) → one event, published. From `cancelled` → `200` replay, no event, nothing published. Any other state → 409 `conflict`. Not in fleet → 404.
- Cancel from `created` leaves `driver_id` NULL; the event is still published (GF-179) and the hub drops it (GF-179 consequence).

**GF-177 — Role-matrix cells (README §8 "Jobs create/assign: Admin ✅, Manager read, Dispatcher ✅, Driver own jobs state transitions only").**
- `POST /jobs`: admin 201, manager 403, dispatcher 201, driver 403, unauthenticated 401.
- `GET /jobs`: admin 200, manager 200, dispatcher 200, driver 403, unauthenticated 401. Driver is 403 because the driver's own-jobs read is P11's `/driver/jobs/today`; P10's list is the dispatcher surface.
- `POST /jobs/:id/assign`: admin 200, manager 403, dispatcher 200, driver 403, unauthenticated 401.
- `POST /jobs/:id/cancel`: admin 200, manager 403, dispatcher 200, driver 403, unauthenticated 401 (D-23 "dispatcher/admin cancel").
- Role checks run before body read (GF-28 idiom).

**GF-178 — Error mapping.**
- 400 `validation_failed`: body or query failure; `23514` from the DB (defensive only; the API validates first).
- 401 `unauthorized`: missing/invalid JWT (middleware).
- 403 `forbidden`: role (middleware).
- 404 `not_found`: job, vehicle or driver not in caller's fleet; malformed job id (the `idParam` idiom treats malformed as absent).
- 409 `conflict`: illegal transition (Go check or `FL409`); lost race (zero rows after re-read, current != target).
- 409 `vehicle_not_active`: assign to a vehicle whose registry status is not `active`.
- 500: `FL428` (actor missing: programming error) and unmapped errors.
- Mapping is a jobs-local `writeErr` mirroring `registry.writeErr`; the jobs package does not import `registry`.

**GF-179 — NATS publishing.**
- Publish after commit, never inside the tx. Only applied transitions publish. Creation, replay, 409 and 404 do not publish.
- Subject: `jobs.events.<fleet_id>` with the decimal fleet id.
- Core NATS `Publish` (GF-4; no JetStream stream; `GPS_RAW` unchanged).
- Body (JSON): `{"type":"job.updated","fleet_id":F,"driver_id":D|null,"job_id":J,"from_state":"…","to_state":"…","at":"<RFC3339 ms Z>"}`. `at` is `job_events.at` read in the same tx.
- Best-effort: publish error is logged with the API's standard logger; the HTTP response is unchanged. The durable record is `job_events` (GF-4).
- Hub consequence (no hub change in P10): `internal/hub/subscribe.go` drops `job.updated` with nil `driver_id`. Transitions with NULL driver (cancel from `created`) are published but not relayed to WS clients until P13 polling covers them. Assign and every post-assign transition carry a driver and are relayed.
- Wiring: `jobs.Publisher` interface `Publish(subject string, data []byte) error`; `*nats.Conn` satisfies it. `Deps.Events` carries it; `main.go` passes the existing `nc`; the harness passes its own `nats.Connect(testinfra.NATSURL(t))`.

**GF-180 — Actor attribution.**
- Every jobs mutation (create, assign, reassign, cancel, transition) runs `SELECT set_config('app.actor_id', $1::text, true)` as the first statement in its tx. `true` = transaction-local; safe with pooled connections. This is the first `set_config` use in the repo; it is the D-24 mechanism, with `set_config` as the parameterizable form of `SET LOCAL`.
- Actor is the principal's `UserID` (`auth.PrincipalFrom`).
- Direct SQL maintenance: a state or assignment change without `app.actor_id` raises `FL428` (GF-171); non-event updates succeed.

**GF-181 — `mapview.JobLookup` implementation (GF-133).**
- `internal/jobs.MapLookup{DB}` implements `mapview.JobLookup.ActiveJobStates(ctx, fleetID, vehicleIDs)`. `internal/mapview` is not modified. `internal/jobs` imports `mapview` for the `JobState` constants; `mapview` does not import `jobs`, so no cycle.
- SQL:
  ```sql
  SELECT DISTINCT ON (vehicle_id) vehicle_id, state
  FROM jobs
  WHERE fleet_id = $1 AND vehicle_id = ANY($2)
    AND state IN ('assigned','accepted','en_route','picked_up')
  ORDER BY vehicle_id,
    CASE state WHEN 'en_route' THEN 0 WHEN 'picked_up' THEN 1 WHEN 'accepted' THEN 2 ELSE 3 END
  ```
  Passed `pq.Array(vehicleIDs)`.
- Precedence per vehicle: `en_route` > `picked_up` > `accepted` > `assigned`.
- Returned states: `en_route` → `JobEnRoute`; `picked_up` → `JobEnRoute` (roadmap display rule: `en_route`/`picked_up` both display `en_route`); `accepted`/`assigned` → `JobOther`. Vehicles with no active job are absent from the map (→ `JobNone`).
- `mapview.Handler.Jobs` is set in `server.go` (G4). `mapview` code is untouched.

**GF-182 — Scope of the mapview wiring and the jobs fence.** Wiring `Jobs` in `server.go` is the only change to the map path. The P10 scope fence (Validation) lists `internal/mapview/` only if a file there changes; the plan expects none.

**GF-183 — CLAUDE.md rows (mechanical only).** A `## Jobs (P10)` block: endpoints and their roles; "every jobs mutation sets `app.actor_id` with `set_config(…, true)`; the trigger writes `job_events`"; subject `jobs.events.<fleet_id>` (core NATS, no stream); `day` filter = `window_start` in `fleets.timezone`; `FL409`/`FL428` meanings. No new env vars or make targets. The file is 107 lines; the block adds under 25.

## Validation

Run on this host (Docker available; compose is never started; PostGIS and NATS come from testcontainers).

1. `make fmt-check` → exit 0.
2. `make lint` → exit 0 (gofmt + golangci-lint v2.15.0).
3. `make test` → exit 0 (`go test ./... -race -count=1`); includes the round trip (`db/roundtrip_test.go`).
4. Named tests, each run with `-race -count=1`, all PASS:
   - `go test ./internal/jobs -run 'TestTransitionEdges|TestJobTransitionsMirror|TestJobsTriggerRejectsInvalidUpdate|TestJobEventsAppendOnly|TestJobStoreTransitionD24|TestJobLookupPrecedence|TestTransitionProperty' -race -count=1`
   - `go test ./services/api -run 'TestJobsCreateValidation|TestJobsListFilters|TestJobsCrossFleet404|TestJobsAssignReplayAndReassign|TestJobsCancelReplay|TestJobsIllegalTransitionPersistsNothing|TestJobsConcurrentCancel|TestJobsPublishOnePerTransition|TestMapShowsJobState|TestRoleMatrix' -race -count=1`
   - `go test ./db -run 'Roundtrip|Migrations' -race -count=1`
5. Roadmap validation items, each with a pass condition:
   - Illegal transition → 409, zero rows persisted: `TestJobsIllegalTransitionPersistsNothing` passes (event count and job row unchanged).
   - Every successful transition has exactly one event: `TestTransitionProperty` invariant 2 passes; `TestJobsAssignReplayAndReassign` and `TestJobsCancelReplay` count events.
6. Scope fence: `git diff --name-only main...HEAD` lists only paths under `db/migrations/`, `db/roundtrip_test.go`, `internal/jobs/`, `services/api/`, `CLAUDE.md`, `specs/features/10-job-core.md`, `prompts/`. `internal/mapview/` appears only if a wiring-only change was needed (none expected). Must NOT appear: `README.md`, `deploy/`, `specs/constitution/`, `internal/hub/`, `apps/`, `go.mod`, `go.sum`. Pass = no forbidden path.
7. CLAUDE.md under 200 lines: `wc -l CLAUDE.md` < 200.
8. No secrets: `git diff main...HEAD | grep -E '^\+' | grep -iE 'password=|secret=|BEGIN [A-Z ]*PRIVATE KEY'` returns nothing. Test fixtures that set `JWT_SECRET` via `testinfra`/harness helpers are not added as literals.
9. Migration numbering: `ls db/migrations | sort` shows `009_jobs_core.up.sql` and `.down.sql` and nothing beyond 009.

## Testing depth

| Layer | What it proves | Tests | Location |
|---|---|---|---|
| Unit | D-23 edge set; `CanTransition` truth table; request validators (point ranges, window order, eta, notes, size_class, weight_kg) | `TestTransitionEdges`, `TestCreateValidators` | `internal/jobs` |
| Rapid property | Three invariants (persisted ⊆ `job_transitions`; events = create + applied; replay → `Replayed`, no event); invalid attempts → `ErrConflict`, nothing persisted; cross-fleet → `ErrNotFound` | `TestTransitionProperty` | `internal/jobs` (testcontainers PostGIS per check) |
| Integration (testcontainers PostGIS) | Trigger rejections by SQLSTATE (`FL409`, `FL428`, `23514`); `job_events` append-only (UPDATE/DELETE/TRUNCATE); mirror (DB rows == Go edges); D-24 store semantics; `MapLookup` precedence | `TestJobTransitionsMirror`, `TestJobsTriggerRejectsInvalidUpdate`, `TestJobEventsAppendOnly`, `TestJobStoreTransitionD24`, `TestJobLookupPrecedence` | `internal/jobs` |
| services/api harness (real `NewServer`, loopback, NATS + PG) | Endpoint validation table; list filters incl. fleet timezone and NULL windows; cross-fleet 404; assign replay/reassign; cancel replay; illegal → 409 persisting nothing; concurrent cancel; NATS one-per-transition; map displays job state | `TestJobsCreateValidation`, `TestJobsListFilters`, `TestJobsCrossFleet404`, `TestJobsAssignReplayAndReassign`, `TestJobsCancelReplay`, `TestJobsIllegalTransitionPersistsNothing`, `TestJobsConcurrentCancel`, `TestJobsPublishOnePerTransition`, `TestMapShowsJobState` | `services/api` |
| Role matrix (R-6) | Four new endpoints × five role columns | `TestRoleMatrix` (extended rows) | `services/api/rolematrix_test.go` |
| Migration round trip | Up → down → up, version 9, new objects present/absent | `Roundtrip` (updated expectations), `Migrations` | `db` |
| Race | Everything above under `-race` | `make test` | repo |

## SPEC-AMEND repeat

Amendments this phase implements (repeated from mission §7 and tech-stack §11; no new row is added):
- **D-23** — `job_transitions` reference table (additive table) and `POST /jobs/:id/cancel` (additive endpoint). Edges per D-23 text above.
- **D-24** — idempotent transition mechanism: `UPDATE … WHERE id=$1 AND state=$from`; zero rows and current == target → 200, no event; otherwise 409; only the trigger writes `job_events`; actor via `SET LOCAL app.actor_id` (implemented as `set_config(…, true)`, GF-180).
- **D-26** — additive `jobs` columns `notes`, `size_class`, `weight_kg`, `delivery_geom geography`, `failure_reason` (schema only; `delivery_geom` and `failure_reason` have no P10 writer).
- **D-17 / GF-4** — `jobs.events.<fleet_id>` on core NATS, no JetStream stream; durable record in `job_events`; best-effort delivery.
- **R-7** — `job_events` append-only, enforced by trigger in the phase that creates it.

## Out of scope

- Driver endpoints and `GET /driver/jobs/today`, `accept`/`start`/`pickup`/`deliver`/`fail` (P11).
- Proofs, `proof_photo`, MinIO, `delivery_geom` writers (P12).
- Assignment UI, nearest-van suggestion (P13). Hub changes and any fanout/presence changes.
- `PATCH /jobs/:id`, `DELETE /jobs/:id`, pagination on `GET /jobs`, OSRM ETA (D-35).
- `README.md`, `deploy/`, `PORTS.md`, compose, `specs/constitution/` edits; `go.mod`/`go.sum` changes.
- Migrations beyond `009`; geofences (P17+).
- Merge to `main`; push.
