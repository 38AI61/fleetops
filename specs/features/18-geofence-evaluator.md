# Feature 18 — Geofence evaluator (P18)

Status: implemented 2026-10-28 on `phase-18-geofence-evaluator` (G0–G5). Validation results and deviations are appended at the end.
Source plan: `/root/.claude/plans/p18-plan-rustling-moonbeam.md` (approved).

Target file: `specs/features/18-geofence-evaluator.md`. Decisions GF-278…GF-289. Highest existing decision is GF-277.

## Scope

P18 turns the P17 registry into live evaluation. After each ingest flush that stores positions, the stored fixes are evaluated against candidate fences, and transitions are written as `enter`, `exit` and `dwell` rows in `geofence_events`. In scope: the flush hook, the evaluator (candidate query, exact geometry, state machine), debounce at 30 s per vehicle-fence pair, dwell on the first fix after `dwell_min` (no sweeper), the evaluation latency metric and its failure counters, the debounce property test, the flush-hook integration tests, the CLAUDE.md evaluator block, and this spec.

Out of scope: alert rules, the ack transition, alert fanout, Notifier (all P19). See Out of scope.

## Plan

**G0 — Preflight (no code).**
- Confirm `phase-18-geofence-evaluator` is clean and based on `main`.
- Run `make fmt-check`, `make lint`, `make test` on the untouched branch and record the result. If only `internal/hub` fails, re-run `go test ./internal/hub -race -count=1` in isolation (flake policy). A second failure stops G0.
- Pin the migration facts: 20 files (001–010 up/down), highest `010`. Confirm the `geofence_events.kind` CHECK admits `enter|exit|dwell` in `010_geofences_events.up.sql`. Confirm in `internal/geofence/store.go` `Create` that a circle centre is stored in `geofences.geom` with `radius_m` alongside. If either differs from this spec, stop and amend before G1.
- Pin the flush call sites (consume loop and `shutdown`) and the consumer `AckWait`. Record whether `AckWait` exceeds the flush deadline plus the 1 s evaluation budget (GF-279).
- Record the web and driver-pwa baseline (`npm --prefix apps/web test`, `npm --prefix apps/driver-pwa test`) if `node_modules` is present. Otherwise record "not run; untouched by P18".
- Tests: none new. Output is the baseline table in Validation results.

**G1 — Evaluator core (`internal/geofence`).**
- Observation and Result types. The Result struct carries per-kind emitted counts, debounced count, failures by stage and duration. The geofence package has no Prometheus import (GF-285).
- Pure state machine (GF-281, GF-282, GF-283): per-pair state, debounce window, dwell. No I/O.
- Candidate SQL (GF-284): one statement per flush, bbox prefilter on `geofences_bbox_gix`, exact `ST_Covers` / `ST_DWithin` on hits only.
- Rebuild (GF-280): lazy per-vehicle load of pair state from `geofence_events` over a 24 h lookback, on first sight after process start.
- Tests:
  - Unit, state machine (table tests): crossing gives one enter and one exit; exit without a recorded enter is ignored; unknown start with an inside truth emits enter; unknown start with an outside truth emits nothing; a suppressed flip inside 30 s emits nothing and is counted; a flip back to the emitted state inside the window emits nothing; out-of-order fix is ignored; dwell at exactly `dwell_min` and at `dwell_min` + 1 s fires once per episode; dwell re-arms after an exit emission; `dwell_min` NULL never dwells.
  - Integration, candidate SQL (`testinfra.NewMigratedDB`): bbox miss is treated as outside; point on a polygon edge is inside (`ST_Covers`); circle at radius minus and plus 1 m; fence in another fleet is never returned.
  - Integration, plan: `EXPLAIN` with `enable_seqscan` off asserts `geofences_bbox_gix` appears (GF-270 technique, `store_test.go:88–128`). The rebuild query is asserted to use `geofence_events_fleet_at_idx` the same way; failure triggers the GF-280 stop rule.
  - Unit, circle prefilter superset: random centres with latitude in [-70, 70], radius 1 m to 100 km; every point for which `ST_DWithin` is true also satisfies `bbox &&`.
  - Unit, rebuild: restart inside the lookback produces no duplicate enter; an enter older than the lookback produces one new enter (documents the accepted GF-280 residual).

**G2 — Flush hook and metrics (`internal/ingest`).**
- `FlushResult.Observed` (GF-278), populated after commit in `writer.go`.
- Evaluator interface in `ingest`, injected into the consumer. Hook call between `pub.Publish` (`consume.go:350`) and `beforeAck` (`consume.go:352`), with a 1 s detached context (GF-279).
- Metrics registered in `NewMetrics` (GF-285).
- Tests (integration, `testinfra` Postgres + Redis + NATS, real flush path):
  - Crossing a depot through real flushes produces exactly one `enter` and one `exit`.
  - Boundary jitter within 30 s produces at most one event.
  - Dwell fires on the first position at or after `dwell_min`.
  - Failure path: a fake evaluator returns an error; the batch is still acked (`ackAfterCommit` called), positions are in the table, the failure metric is +1, pair state is not advanced, and the next batch emits the transition.
  - Mismatch path: the hook is not called.
  - Replay: a redelivered batch (all Stale) produces zero new events.
  - Registry gather finds the new metric names with zero label values pre-created.

**G3 — Debounce property test (`internal/geofence`, `pgregory.net/rapid`).**
- Generator: 1–3 vehicles, 1–3 fences (polygons and circles around a fixed origin), 1–200 fixes per vehicle with server_ts gaps of 0–120 s and positions jittered across boundaries, batched into random flush sizes.
- Oracle: for every fix, the exact inside/outside truth is computed by a PostGIS query on the same geometry, not by the Go code.
- Assertions:
  - Per pair, consecutive emitted `occurred_at` values differ by at least 30 s (≤1 event per pair per 30 s).
  - Each emitted `enter` matches an inside oracle truth at its `occurred_at`; each `exit` matches an outside truth.
  - Per pair, kinds alternate starting with `enter`.
  - Each `dwell` is inside, at least `dwell_min` after the enter, and at most once per episode.
- Seed policy: the repo's convention (no fixed seed anywhere). rapid draws a fresh seed per run; a failure prints the seed for replay with `-rapid.seed=<n>`. Every failing seed is then added as a fixed regression case before the fix lands.
- Three hand-written fixed streams (crossing, jitter burst, dwell) run as plain tests so there is deterministic coverage under `-count=1`.

**G4 — Docs.**
- CLAUDE.md evaluator block, at most 12 lines (GF-288).
- Mark this spec's status and record validation results.

**G5 — Full validation.** Run the Validation section below.

## Requirements

**Binding decisions carried verbatim.**
- D-33 (tech-stack §11, line 292): "Geofences: `geofences(id, fleet_id, name, kind, geom geography, radius_m NULL, bbox geometry, dwell_min NULL, schedule jsonb)`; schedule `{days:[1..7], from:"HH:MM", to:"HH:MM", tz}`; `geofence_events` append-only; evaluation runs in ingest after flush for moved vehicles; prefilter `bbox && point`, then `ST_Covers` (polygon) or `ST_DWithin` (circle); debounce 30 s per pair; dwell evaluated on next position after `dwell_min` (no sweeper; accepted). **SPEC-AMEND.**"
- D-34 (tech-stack §11, line 293): "Alert semantics: `device_silent` … `geofence_exit_outside_schedule` = exit event outside the schedule window (critical); `eta_slip` …" P18 implements only the event-producing side of `geofence_exit_outside_schedule` (GF-286).
- Cost row (tech-stack §11, line 332): "Geofence evaluation cost | Bbox prefilter, 30 s debounce, exact geometry on candidates only | P18".
- R-7 (roadmap §1): "Append-only tables are enforced by trigger in the phase that creates them: … `geofence_events` (P18) …" Already reconciled by the P17 SPEC-AMEND trigger-timing row (mission §7, line 94).
- R-1/R-2 (roadmap §1): smallest MVP. P18 adds no behaviour beyond the roadmap P18 scope.
- Mission §7 ledger: D-33 row (line 77) is the registered SPEC-AMEND for geofences and events. P18 adds no new row (see SPEC-AMEND repeat).
- README §2.1.6 (geofences product row), §7 (evaluation mechanics), §13 (cost control). README is not edited (R-8). Where §7 and D-33 differ, D-33 governs (GF-287).

**Reused decisions (cite, do not restate).**
- GF-262: bbox is computed server-side on every write and never taken from the client. P17 amended the circle constants to `110574` m/deg and margin `1.01` (`bbox.go:8,11`). P18 relies on that bbox being a superset of the circle; G1 tests it.
- GF-266: P17 created the table and trigger; P17 wrote zero event rows. P18 is the first writer.
- GF-267: `geofence_events.geofence_id` is `ON DELETE RESTRICT`. A delete of a fence with events is 409.
- GF-270: the GIST index `geofences_bbox_gix` is asserted in an `EXPLAIN` plan. P18 uses the same technique.
- GF-277: P18 is the boundary crossing (the P17 spec excluded prefilter, `ST_Covers`, `ST_DWithin`, debounce, event writes, and alerts from P17).

**GF-278 — Flush hook placement and inputs.** The evaluator runs inside `flush` (`internal/ingest/consume.go`) after the mismatch check and after `pub.Publish` (`consume.go:350`), before the ack loop (`consume.go:352–357`). Its input is `FlushResult.Observed`, a new field the writer fills after commit (`writer.go:159–162`) from the paired messages the insert stored. Stale and unpaired messages are never observed. A "moved vehicle" in this batch is one with at least one fix stored in the flush. Every stored fix is evaluated in `(server_ts, msg_id)` order, not only the latest per vehicle, so a crossing and a recrossing inside one batch both register.
*Rationale:* synchronous, before ack, so pair state advances in arrival order with one writer (the consume goroutine). An async hook would reorder per-pair state and undercut GF-280. Hook is skipped on mismatch (publish is skipped at the same point) and on writer error or timeout.

**GF-279 — Failure handling and redelivery.** The hook runs on its own context, detached from the flush deadline (as `publish.go:36` does), with a 1 s budget. An evaluation error or timeout never changes the ack: the batch is acked as today after commit. Failures increment `fleetops_geofence_evaluation_failures_total{stage}` and are logged with fleet id and count, never with addresses. No retry. A failed pair does not advance state, so the next stored fix for that pair re-derives the transition with that fix's server_ts. A transition is lost only if the vehicle sends no later fix for that pair. Redelivery never re-evaluates: a redelivered fix is Stale in the writer and is not observed.
*Rationale:* positions are the source of truth and a side-effect failure must not NAK a committed batch (redelivery would multiply writes). Accepted trade-off: events are at-most-once on evaluation failure.

**GF-280 — Pair state and restart.** State is in memory, keyed by `(vehicle_id, geofence_id)`, owned by the single consume goroutine. No migration. On the first flush that stores a fix for a vehicle after process start, that vehicle's pair state is rebuilt from `geofence_events` with `fleet_id = $1 AND vehicle_id = $2 AND occurred_at >= now() - interval '24 hours'`, served by the existing `geofence_events_fleet_at_idx (fleet_id, occurred_at)`. Per pair, the latest row gives the emitted state, `last_emit_at`, and `entered_at` for an enter; `dwell_emitted` is set when a dwell row follows the latest enter. Pairs with no rows in the window start as unknown (= outside).
*Residual:* a vehicle still inside a fence whose last event is older than 24 h gets one new `enter` after restart. Accepted.
*Stop rule:* G1 `EXPLAIN` of the rebuild query must use `geofence_events_fleet_at_idx`. If it does not, the phase stops and a migration plus its own SPEC-AMEND row is proposed to the user before continuing.
*Rationale:* no schema change for P18 unless the index fails the check; the lookback bounds boot cost to recent events.

**GF-281 — Enter/exit state machine.** Per pair: emitted state ∈ {inside, outside}, default outside (unknown start = outside). Each observation has a truth value. Observations older than the pair's `last_obs_at` are ignored. Truth inside with emitted outside is an enter candidate. Truth outside with emitted inside is an exit candidate. Exit requires a recorded inside state, so an outside truth with emitted outside emits nothing. Unknown start with an inside truth emits enter. Candidates pass through the debounce (GF-282). A suppressed candidate changes nothing and the next fix re-tests it. The first event emitted for a pair wins a jitter burst: later flips inside the window are suppressed, and a flip back to the emitted state emits nothing.
*Rationale:* emitted state tracks recorded events, not raw geometry, so debounce suppression never desynchronises state from the table.

**GF-282 — Debounce.** The window is anchored at the pair's last emitted event (`last_emit_at` = its `occurred_at`). A candidate from a fix with server_ts `T` is emitted iff `T >= last_emit_at + 30 s`. Consequence: consecutive emitted events for a pair are at least 30 s apart in `occurred_at`. This is the G3 property. The window is anchored at emission, not at the first candidate, so a bursty boundary cannot push the window forward. Suppressed candidates increment `fleetops_geofence_debounced_total`. The clock is the stored fix server_ts, not wall clock, so the rule is deterministic under replay.

**GF-283 — Dwell.** Only when `dwell_min` is non-NULL. Dwell is anchored at the `occurred_at` of the enter that opened the current inside episode. It fires at most once per episode, on the first fix where emitted state is inside, `T >= enter.occurred_at + dwell_min`, and `T >= last_emit_at + 30 s`. `dwell_emitted` resets when an exit is emitted. No sweeper: a vehicle that stops reporting while inside does not dwell until its next fix (D-33). `dwell_min` is at least 1 minute (P17 validation), so the 30 s window from the enter has closed when dwell comes due, unless another event for the pair intervened, in which case the second clause applies.

**GF-284 — Evaluation SQL shape.** One statement per flush. Observed points (vehicle_id uuid, lng, lat, server_ts, msg_id) are unnested into a CTE. Vehicles are joined for `fleet_id`. A `LATERAL` join over `geofences g WHERE g.fleet_id = v.fleet_id AND g.bbox && ST_SetSRID(ST_MakePoint(lng, lat), 4326)` uses `geofences_bbox_gix`. The exact predicate runs only on prefilter hits: polygon `ST_Covers(g.geom, p.geog)`, circle `ST_DWithin(g.geom, p.geog, g.radius_m)`, with `p.geog` cast to `geography` at SRID 4326. The statement returns `(vehicle_id, msg_id, server_ts, geofence_id, inside)` for hits only. A fence absent for a fix is outside. That is exact, not approximate, because the bbox is a superset (G1 test). Pairs tracked as inside that are absent from the result are therefore evaluated as outside. Points on a polygon boundary are inside (`ST_Covers` is boundary-inclusive).
*Plan proof:* G1 `EXPLAIN` test (GF-270 technique).

**GF-285 — Latency and failure metrics.** Registered in `NewMetrics` (`internal/ingest/metrics.go:40`, `MustRegister` at `:110–115`), with zero label values pre-created as the existing set does (`:92–109`). Not exposed until P22 (`metrics.go:22`). New:
- `fleetops_geofence_evaluation_duration_seconds` — histogram, no labels, buckets `evalBuckets` from 0.5 ms to 1 s; observed for every flush that reaches the hook, success or failure.
- `fleetops_geofence_events_total{kind}` — counter, `kind` ∈ {enter, exit, dwell}.
- `fleetops_geofence_debounced_total` — counter.
- `fleetops_geofence_evaluation_failures_total{stage}` — counter, `stage` ∈ {candidates, state, insert}.
The evaluator returns a Result struct; the ingest hook records Prometheus metrics, so the geofence package has no Prometheus import. The GF-83 atomic Counters are not used (these are exported metrics, GF-93).

**GF-286 — D-34 interface.** Each event row carries `fleet_id`, `geofence_id`, `vehicle_id`, `kind`, and `occurred_at` (the server_ts of the triggering fix). That is sufficient for P19's `geofence_exit_outside_schedule` rule, which applies the schedule to exit `occurred_at` in `fleets.timezone`. P18 does not evaluate schedules. P18 writes no alert, no ack, no fanout, no Notifier, and nothing alert-shaped in types or columns.

**GF-287 — README §7 vs D-33.** Where README §7 says `ST_Contains` and a `ST_DWithin` prefilter, D-33 is authoritative: a `bbox &&` prefilter, then `ST_Covers` (polygon) or `ST_DWithin` (circle). README is not edited (R-8).

**GF-288 — CLAUDE.md rows (mechanical only).** One evaluator block, at most 12 lines: flush hook location, debounce 30 s, dwell rule, rebuild lookback 24 h, the metric names. No env vars, no ports, no make targets.

**GF-289 — Scope fence.** P18 adds no migration (GF-280 stop rule aside), no API route, no change to `geofences` or `geofence_events` schema, no change to P17 `/geofences` behaviour, no `e2e/` change, no `apps/` change. P18 writes only `enter`, `exit`, and `dwell` rows.

## Validation

Each check has a pass condition on this host.

a. **Gates.** `make fmt-check`, `make lint`, `make test` exit 0, including the new evaluator and hook tests. `make test` needs Docker for testinfra. Flake policy: if only `internal/hub` fails, run `go test ./internal/hub -race -count=1` alone; a pass there is recorded as a flake, not a failure.

b. **Scope fence.** `git diff --name-only main...HEAD` is a subset of: `internal/ingest/**`, `internal/geofence/**`, `specs/features/18-geofence-evaluator.md`, `prompts/**`, `CLAUDE.md`. Pass: no other path. A migration path or the mission §7 row appears only if the GF-280 stop rule was triggered and the user approved a SPEC-AMEND row.

c. **No secrets.** `git diff main...HEAD | grep '^+' | grep -E -i 'password|secret|token|BEGIN [A-Z ]*PRIVATE KEY|postgres://[^ ]*:[^ ]*@'` prints nothing.

d. **Frozen trees.** `git diff --name-only main...HEAD -- e2e/ apps/ README.md deploy/ services/api/` prints nothing.

e. **CLAUDE.md size.** `wc -l CLAUDE.md` is under 200. The evaluator block is at most 12 lines.

f. **Property determinism.** `go test ./internal/geofence -run Debounce -race -count=1` passes twice in a row. Seed policy is as stated in G3.

g. **P19 boundary.** `git diff main...HEAD -- internal/ | grep '^+' | grep -E 'Notifier|alert_rules|alerts\.|/ack|PATCH|geofence_exit_outside_schedule'` prints nothing.

h. **Metric registration.** A unit test gathers the registry and finds all four new metric names. Pass: the test is green.

i. **Ack timing.** G0 records that `AckWait` exceeds the flush deadline plus the 1 s evaluation budget. Pass: recorded in Validation results.

j. **Apps baseline.** `apps/web` and `apps/driver-pwa` are untouched, so their test output is unchanged from the G0 baseline. Re-run only if `git diff --name-only main...HEAD -- apps/` is non-empty.

## Testing depth

| Layer | What | Where | Gate |
|---|---|---|---|
| Property | Debounce: random streams vs PostGIS oracle, ≤1 event per pair per 30 s, alternation, dwell rules (rapid) | `internal/geofence` | G3 |
| Integration | Flush hook: crossing (1 enter, 1 exit), jitter ≤1 event, dwell on first fix ≥ `dwell_min`, failure path acked, mismatch skip, replay zero events | `internal/ingest` (testinfra) | G2 |
| Integration | Candidate SQL: bbox miss = outside, boundary, circle radius, fleet scope, `EXPLAIN` uses `geofences_bbox_gix`, rebuild uses `geofence_events_fleet_at_idx` | `internal/geofence` (testinfra) | G1 |
| Unit | State machine table tests; circle prefilter superset; rebuild restart and lookback residual | `internal/geofence` | G1 |
| Unit | Metric registration | `internal/ingest` | G2 |
| Existing | Full Go suite (with hub flake policy); `apps/web` and `apps/driver-pwa` unchanged; P16 `e2e/happy-path.spec.ts` untouched | repo | G0, G5 |

## SPEC-AMEND repeat

P18 adds **no new SPEC-AMEND row**. The D-33 SPEC-AMEND row (mission §7, line 77) and the P17 trigger-timing row (line 94) already cover P18's evaluation semantics and the `geofence_events` writer. R-7's "`geofence_events` (P18)" naming is reconciled by the trigger-timing row. GF-280 requires no migration, so no additional row is expected. If the GF-280 stop rule fires, a migration and its own SPEC-AMEND row are required and will be proposed to the user before continuing.

The GF-192 hub identity binding remains open and belongs to P23.

## Out of scope

- Alert rules, the ack transition, alert WS fanout, Notifier (P19).
- ETA and OSRM (P20). Reports (P21). Dashboards and `/metrics` export (P22). k6 (P23).
- Push notifications (GF-1).
- README edits. Compose or PORTS changes.
- `e2e/` changes (the happy path stays green and untouched).
- `apps/web` and `apps/driver-pwa` changes.
- Any API surface change, or any `geofences` or `geofence_events` schema change (beyond the GF-280 stop-rule case, which needs approval).
- Schedule evaluation (P19).
- Merge to main, push.
- Any P19+ content.

## Validation results and deviations

Run 2026-10-28 on `phase-18-geofence-evaluator` after G4 (commits `64b85b5`, `bc064fa`, `135a95b`, `fb0c2db`), with the G5 lint fix `f554f14`.

**G0 baseline (untouched branch, before any P18 code).** `make fmt-check` exit 0. `make lint` exit 0, 0 issues. `make test` exit 0 with every package `ok` (including `internal/hub` on its first run, so no flake retry). Migrations: 20 files, 001–010 up and down, highest `010`. `010_geofences_events.up.sql` admits `kind IN ('enter','exit','dwell')`. `geofences.geom` holds the circle centre with `radius_m` (`store.go:64–75`). Flush call sites: `consume.go` `flushWithTimeout` (from `cycle` on a full batch and on the interval tick) and `shutdown`, which calls `flush` directly. `defaultConsumerSpec`: `ackWait` 30 s, `flushTimeout` 5 s. Web and driver-pwa `npm test` were run at G5 on the same tree (apps untouched, so identical to baseline): both exit 0.

**Validation checks.**

- (a) Gates. `make fmt-check` exit 0. `make lint` exit 0, 0 issues (after one fix: gosec G115 in the debounce test, commit `f554f14`). `make test` exit 0, 25 `ok` packages, no FAIL, including `internal/geofence` and `internal/ingest`.
- (b) Scope fence. `git diff --name-only main...HEAD` lists only `CLAUDE.md`, `internal/geofence/*`, `internal/ingest/{consume,metrics,metrics_test,writer}.go`, `internal/ingest/geofence_hook_test.go`, `prompts/58–60-p18-*.md`, and `specs/features/18-geofence-evaluator.md`. No migration, no mission row, no README, no `services/`.
- (c) No secrets. Code paths (`internal/ services/ tools/ db/ CLAUDE.md`): the pattern prints nothing. The literal grep over the full diff matches two lines of spec and prompt text that quote the check itself. Those lines are documentation, not credentials.
- (d) Frozen trees. `git diff --name-only main...HEAD -- e2e/ apps/ README.md deploy/ services/api/` prints nothing.
- (e) `CLAUDE.md` is 187 lines (under 200). The evaluator block is 8 lines (heading and 7 bullets, at most 12).
- (f) Property determinism. `go test ./internal/geofence -run Debounce -race -count=1` passed twice in a row: `ok`, and rapid "passed 100 tests" each run, with a fresh seed each run. Extra runs: `-rapid.checks=1000` passed. A mutation check (debounce window disabled in `state.go`, then reverted) made the property and the jitter stream fail, so the test detects the bug it targets.
- (g) P19 boundary. `git diff main...HEAD -- internal/ | grep '^+' | grep -E 'Notifier|alert_rules|alerts\.|/ack|PATCH|geofence_exit_outside_schedule'` prints nothing.
- (h) Metric registration. `TestMetricsRegisteredNames` (extended with the four names) and `TestGeofenceMetricsSeriesPreCreated` pass. All four names are present with label values pre-created.
- (i) Ack timing. `ackWait` 30 s exceeds the flush deadline (5 s) plus the 1 s evaluation budget (6 s). Pass.
- (j) Apps baseline. `git diff --name-only main...HEAD -- apps/` is empty, so the apps tests were not required to re-run. They were run anyway: `apps/web` and `apps/driver-pwa` `npm test` both exit 0.

**Check outcomes of the named tests.** Candidate SQL: bbox miss, boundary (vertex and meridian midpoints inside), circle at radius d−1 and d+1, and fleet scope all pass. `EXPLAIN` (seqscan off) shows `geofences_bbox_gix` for the candidate statement and `geofence_events_fleet_at_idx` for the rebuild, so the GF-280 stop rule was not triggered. Circle superset property: 100 rapid checks pass. Rebuild: restart inside the lookback gives no duplicate enter. An enter older than 24 h yields one new enter (the accepted residual). Flush-hook integration: crossing (1 enter, 1 exit), jitter (1 event, 3 debounced), dwell at exactly `dwell_min` only, mismatch (hook not called), replay of a stale batch (zero events), and the failure path through real NATS (batch acked, positions stored, failure counted, next batch emits the enter) all pass.

**Deviations and interpretations.**

1. `specs/constitution/constitution.md` (named in the brief) does not exist. The constitution is `mission.md`, `roadmap.md`, and `tech-stack.md`. R-1 to R-9 were read from `roadmap.md` §1.
2. The source plan `/root/.claude/plans/p18-plan-rustling-moonbeam.md` (cited in the spec header) is not present. The approved feature spec was used as the sole source.
3. Production wiring. The evaluator is built inside `NewConsumer` from the writer's DB (`consume.go`), and `services/ingest/main.go` is unchanged. The scope fence (b) excludes `services/`. The seam is `Consumer.geo`, which tests replace.
4. Candidate rows. The candidate query returns every bbox hit with its exact `inside` flag, not only rows where `inside` holds. A fence with no row for a fix is outside that fix. This is how I read "hits only" in GF-284.
5. `last_obs_at` is maintained only for pairs the evaluator visits (a bbox hit, or a fence the vehicle is currently inside). A fix that is outside and touches no tracked pair does not advance `last_obs_at`. So a later older inside fix for that pair is not ignored by the out-of-order rule. This is narrower than GF-281 says. It only matters for out-of-order fixes across flushes. Accepted for bounded per-flush cost; not measured.
6. Debounce count. `fleetops_geofence_debounced_total` counts suppressed enter and exit candidates only. Dwell waiting for the debounce window is not counted.
7. The circle superset test (`TestCircleBBoxSupersetProperty`) is database-backed. Its truth is PostGIS `ST_DWithin` on the spheroid, rather than a pure-Go formula. It sits in the `internal/geofence` package, as the Unit row required.
8. The duration histogram is observed on every successful flush that reaches the hook, including flushes with no observations (the evaluator returns immediately on empty input). This follows GF-285's wording.
9. Pair state is kept in memory for every pair ever visited, with no eviction. The spec does not address this. Growth is bounded by vehicles times fences touched. Noted for P22 or later.
10. Test fixtures. Several plan-level tests use `PREPARE` plus `EXPLAIN EXECUTE`, not literal substitution, so the real plan is tested with the real parameter types.

**Manual checks.** None are part of this phase.

