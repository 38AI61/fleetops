# Feature 19 — Alerts core (P19)

Status: planned on `phase-19-alerts-core`, not implemented, 2026-10-28. Validation results and deviations are appended at the end when the phase runs.
Source plan: /root/.claude/plans/p19-plan-tranquil-avalanche.md (approved).

## Scope

In: `alert_rules` (three fixed kinds seeded per fleet, D-08); `alerts` append-only with a one-time ack transition (D-09, R-7); `GET /alerts?since=`; `PATCH /alerts/:id/ack`; `GET /alert-rules`, `PATCH /alert-rules`; `device_silent` evaluator (D-34); `geofence_exit_outside_schedule` evaluator (D-34); `alert.raised` on `alerts.<fleet_id>` (core NATS, GF-4); `Notifier` stub with `LogNotifier` (D-11); notification bell in `apps/web`.

Out: the `eta_slip` evaluator (P20, see GF-290). Everything in the Out of scope section.

## Plan

**G0 — Preflight.** Confirm the branch is clean on `main` plus the P19 plan commit. Run the baseline gates before any change: `make lint`, `make test`, `npm --prefix apps/web ci --include=dev` then web typecheck, test, build; `apps/driver-pwa` typecheck, test, build recorded as baseline only. Pin facts from migrations `001`–`010` and the code, which P19 reads but does not change:
- highest migration `010`; P19 adds `011_alerts_core`; `db/roundtrip_test.go` asserts version `10` and is bumped to `11`;
- highest decision GF-289; P19 starts at GF-290;
- `fleets.timezone` (`004`, default `UTC`);
- `geofence_events` columns (`id`, `kind`, `occurred_at`, `created_at`, `fleet_id`, `geofence_id`, `vehicle_id`);
- `geofences.schedule` shape `{days, from, to, tz}`;
- `vehicles_latest` columns and `vehicles.status` / `vehicles.paired_device_id`;
- the `users` role and fleet column names and whether a deactivation flag exists (for the Notifier recipient query);
- whether `apps/web` already holds a `/stream` client on any page (decides the bell's WS path, GF-299);
- the hub flake policy: `TestHubRelaysNATSJobAndAlert` re-runs in isolation before a failure counts.
Tests: none new. The baseline outcome goes into Validation results.

**G1 — Migration `011_alerts_core` + per-fleet seeding.** Up and down files, one transaction-friendly script each.
- `alert_rules(id, fleet_id, kind, params jsonb not null default '{}', enabled bool not null default true, updated_at, UNIQUE(fleet_id, kind), CHECK kind IN three kinds)`.
- `alerts(id, fleet_id, kind, severity, payload jsonb, raised_at, acked_by → users(id) ON DELETE RESTRICT, acked_at, CHECK (acked_by IS NULL) = (acked_at IS NULL), CHECK kind IN three kinds, CHECK severity IN ('warning','critical'))`.
- Backfill: one `alert_rules` row per existing fleet per kind (device_silent enabled, threshold 600; eta_slip enabled false; geofence_exit_outside_schedule enabled).
- Function and triggers (GF-295): `BEFORE UPDATE` allows only the one-time transition; `BEFORE DELETE` and `BEFORE TRUNCATE` raise `check_violation`. Same shape as the `geofence_events` trigger (`010`, GF-266/GF-267).
- Go: `internal/alerts/seed.go` `SeedRules(ctx, tx, fleetID)` is called from `internal/seed` inside the existing seed transaction, after the fleet insert.
- Down: drops triggers, function, then tables.
Tests: `db/roundtrip_test.go` (up, down to zero, up, version `11`). Integration (testcontainers PostGIS): first ack via raw SQL succeeds; second ack rejected (`check_violation`); UPDATE of `severity` or `payload` rejected; DELETE rejected; TRUNCATE rejected; CHECK rejects `acked_by` set with `acked_at` NULL. Seed: three rules per fleet, idempotent on re-seed, backfill produces the same rows.

**G2 — Store and HTTP.** `internal/alerts/store.go` (`ListAlerts`, `Ack`, `ListRules`, `PatchRules`), `internal/alerts/http.go` (`Register(e, authn)` following the P17 geofence pattern). One line in `services/api/server.go` to register it. Shapes and roles are GF-298. Fleet always comes from the principal (GF-269 precedent).
Tests (API, table-driven): role matrix for each route (admin, manager, dispatcher, driver, anonymous); `since` parse (bad value 400 `validation_failed`); `limit` bounds (0 and 201 are 400); ack 200 with the alert body; second ack 409 `conflict`; ack of another fleet's alert 404; malformed id 404; `GET /alerts` cross-fleet isolation; `PATCH /alert-rules` unknown kind 400, out-of-range `threshold_s` 400, partial update, `eta_slip` returns `evaluated: false`, dispatcher 403 on rules.

**G3 — Pure rules.** `internal/alerts/rules.go` (kinds, severities, default params, per-kind param validation), `internal/alerts/silence.go` (`decideSilence(lastSeen, now, threshold, existing) bool`, GF-292), `internal/alerts/schedule.go` (`InSchedule(s *Schedule, t time.Time) bool`, GF-296).
Tests (unit): schedule table cases (day membership, `from` inclusive, `to` exclusive, overnight window, `NULL` schedule never outside, weekday wrap Sunday→Monday); DST boundaries in `America/New_York` spring-forward and fall-back, plus `Europe/London`; `decideSilence` boundary (`now-lastSeen == threshold` does not alert, `+1 s` does); rule param validation.

**G4 — Evaluator passes and wiring.** `internal/alerts/evaluator.go`: `Run(ctx, interval)` ticker at 30 s (GF-291). Each pass:
1. `BEGIN`; `pg_try_advisory_xact_lock(<fixed key>)`; if not acquired, skip the pass (no error, metric).
2. Load `alert_rules` for all fleets (one query).
3. `device_silent` candidate query and `geofence_exit` scan (GF-292, GF-293), with `now` passed in as a parameter.
4. Insert alerts; `COMMIT`.
5. After commit: publish each inserted alert (G6), notify critical ones (G7).
Wiring: `services/ingest/main.go` starts the goroutine beside the presence sweeper and stops it with the same cancel/done pattern.
Tests (integration, testcontainers, seeded fleet, injected `now`): silence raises once, re-arms after a report, raises again on the next silence; vehicle without a `vehicles_latest` row never alerts; `status` other than `active` never alerts; no `paired_device_id` never alerts; disabled rule raises nothing; exit outside schedule raises one critical alert across two passes; exit inside window raises nothing; `schedule` NULL raises nothing; cross-fleet isolation; a pass that cannot take the lock does nothing.

**G5 — Property test on silence episodes.** `internal/alerts/silence_property_test.go`, using `pgregory.net/rapid` (D-37) with `rapid.Check`, the same shape as `internal/geofence/debounce_test.go` and `internal/jobs/transition_property_test.go`. Model and oracle in GF-292. The generator draws random report-gap and tick-gap sequences; the in-memory key set mirrors the SQL `NOT EXISTS`.
Assertions per sequence: at most one alert per episode key; the set of alerted keys equals the oracle set; each alert fires at the first observing tick after the threshold; a new report starts a new episode that can alert again.
Seed policy: no fixed seed. rapid draws a fresh seed per run; a failure prints the seed for `-rapid.seed=<n>`, and that seed is added as a fixed regression case before the fix lands (P18 G3 style).
Fixed coverage: three hand-written deterministic report/tick streams live in the same file as plain tests (`TestSilenceEpisodesFixed`), so `-count=1` still exercises known cases on every run.

**G6 — Fanout and WS.** `internal/alerts/publish.go`: `Publisher` interface (`Publish(subject string, data []byte) error`) like `internal/jobs/publish.go`, backed by `services/ingest`'s NATS connection. Subject `alerts.<fleet_id>`, envelope in GF-299. Publish runs after commit; failure is logged and counted, never rolls back the alert row.
No production change in `internal/hub/` (the subscription and `alert.raised` relay already exist, `internal/hub/subscribe.go:71`, `:131-132`, `:141-143`). New test file only.
Tests (integration): NATS publish reaches a dispatcher WS client within 5 s; the driver client receives nothing (`notDriver`); a fake publisher returning an error leaves the alert row in place and increments the failure counter.

**G7 — Notifier stub.** `internal/alerts/notify.go`: `Notifier` interface `Notify(ctx, Notice) error`; `Notice{EventType, FleetID, AlertID, Severity, Kind, RecipientUserIDs []int64}`; `LogNotifier` logs event type, kind, fleet id and user id only (GF-300). Recipient query: users with the manager role in the fleet (G0 pin for column names). Called after commit for `critical` only.
Tests (unit): critical calls the notifier with manager ids; warning does not; `LogNotifier` output contains the event type and user id and does not contain a manager's email address (asserted with a fixture address); a notifier error is logged and counted, and the alert row remains.

**G8 — Bell UI (`apps/web`).** `apps/web/src/alerts/api.ts` (list and ack helpers via the existing `apiFetch`/`getJson`/`sendJson`), `apps/web/src/alerts/AlertBell.tsx` (unacked badge from the response `unacked`, dropdown of the last 24 h, Ack button). Rendered in the topbar of `map/MapPage.tsx`, `jobs/JobsPage.tsx`, `geofences/ZonesPage.tsx`. Polls `GET /alerts` every 30 s via react-query (`refetchInterval`); ack invalidates the query. No new WS client (GF-299).
Tests (vitest, testing-library, mocked `api`): badge shows the unacked count; list renders kind and severity; Ack calls the PATCH and refreshes; a 409 from Ack shows a message; the badge is absent when the request fails with 401 (session handled by the existing layer).

**G9 — CLAUDE.md rows and full validation.** Alerts block of at most 12 lines (GF-302, mechanical only). Run the validation section. Record results and any deviation in this spec.

Dependencies: G1 → G2, G4; G3 → G4, G5; G4 → G6, G7; G2 → G8.

## Requirements

Binding (carried from the constitution):
- **D-08:** `alert_rules(id, fleet_id, kind, params jsonb, enabled, updated_at)`; three fixed kinds seeded per fleet (silent 10 min, ETA slip 15 min, schedule window); parameters configurable, kinds not; `GET/PATCH /alert-rules` admin and manager.
- **D-09:** `alerts` append-only except one trigger-enforced `NULL → value` transition on `acked_by`/`acked_at`; DELETE forbidden.
- **D-11:** `notify.Notifier` interface; `LogNotifier` logs event type and recipient user id, never the address; critical alerts go to managers.
- **D-34:** `device_silent` = last `server_ts` older than 10 min, one alert per silence episode, re-armed when the device reports again, warning. `geofence_exit_outside_schedule` = exit event outside the schedule window, critical. `eta_slip` is P20 (GF-290).
- **R-1 / R-2:** smallest MVP. **R-7:** `alerts` trigger lands in the phase that creates it.
- **tech-stack §6, GF-4:** `alerts.<fleet_id>` is core NATS, no JetStream stream. Durable record is `alerts`; WS delivery is best-effort with polling fallback (README §3).
- **tech-stack §9:** silence-episode property test; testcontainers PostGIS.
- **tech-stack §11, D-37:** property tests use `pgregory.net/rapid`; the property test in G5 uses it and follows the repo seed policy (G5).
- **mission §7:** D-08, D-09, D-11, D-34 are already registered. No new SPEC-AMEND row (see SPEC-AMEND repeat).
- **README §2.1.7** (alert kinds and thresholds), **§5** (`alerts(id, fleet_id, kind, severity, payload jsonb, raised_at, acked_by)`, `acked_at` added by D-09), **§6** (`GET /alerts?since=`, `PATCH /alerts/:id/ack`, `alert.raised` WS event), **§8** (`alerts` append-only; the role table has no alerts row, so GF-298 defines it). README is not edited (R-8).

Reused decisions (cited, not restated):
- GF-266 / GF-267: trigger and `ON DELETE RESTRICT` precedents.
- GF-278 / GF-279: hook conventions. The alert evaluator is not a flush hook, but the rule that an alert error never changes an ack carries over.
- GF-286: exit rows carry the `occurred_at` and `schedule` input that the schedule rule reads.
- GF-93: metric naming and label pre-seeding.
- GF-4: core NATS for `alerts.*`.
- GF-265: schedule JSON shape and validation.
- GF-269: fleet from the principal.
- GF-192: hub identity binding stays open (P23).

### GF-290 — Scope: `eta_slip` is not a P19 evaluator
Roadmap §3 lists `eta_slip` under P20 and omits it from P19 scope. D-08 requires three seeded kinds, so the `eta_slip` row is seeded with `enabled = false`. Its evaluator, its OSRM dependency and its cadence are P20. `PATCH` on that row is accepted and returns `evaluated: false`.
**Why:** the roadmap is the scope contract; a P19 `eta_slip` would need projected arrival, which needs P20 routing. **How to apply:** no `eta_slip` code in P19; the string `eta_slip` appears only in the rule kind list and the seed.

### GF-291 — Placement and cadence
The alert evaluator is a goroutine in `services/ingest`, started beside the presence sweeper. Tick 30 s. Each pass takes `pg_try_advisory_xact_lock` on a fixed key, so only one pass runs across replicas. A pass that cannot take the lock does nothing and increments a counter.
Alerts are raised at evaluation time (`raised_at = now`). Silence is measured from `server_ts`.
**Why:** `services/ingest` already hosts the single-writer sweeper and maintenance loops and holds the DB and NATS connections. The P18 `Evaluate` returns counts only and P18 code is frozen, so a flush hook would need a P18 change. **How to apply:** `ws-hub` never writes alerts.

### GF-292 — Silence episodes are stateless
The episode key is `(vehicle_id, last_seen_at)`, where `last_seen_at` is `vehicles_latest.server_ts`. The `device_silent` candidate query returns vehicles with `status = 'active'`, `paired_device_id IS NOT NULL`, a `vehicles_latest` row, `server_ts < now - threshold` and no existing alert with the same key. The alert payload is `{"vehicle_id", "last_seen_at", "silent_for_s"}`.
Re-arm is structural: a new report changes `server_ts`, so the next silence is a new key. No in-memory state. Restart-safe: the key lives in `alerts`.
Pure function: `decideSilence(lastSeen, now, threshold, existing)` returns true iff `now - lastSeen > threshold` and the key is not in `existing`.
Oracle for G5: for each report `L` with next report `N`, the episode alerts iff some tick `t` satisfies `L ≤ t < N` and `t - L > threshold`; it alerts at the first such tick.
**Why:** no state to lose on restart, and the property test can express episodes as data. **How to apply:** the SQL `NOT EXISTS` and the in-memory key set in G5 use the same key definition.

### GF-293 — Exit rule consumes `geofence_events` directly
Each pass scans `geofence_events` with `kind = 'exit'` and `created_at > now - 24 h` for fences with `schedule IS NOT NULL` and an enabled rule. A row is an alert candidate if no `geofence_exit_outside_schedule` alert has `payload.event_id` equal to its `id`. Candidates are checked with `InSchedule(schedule, occurred_at)`; outside means an alert.
Payload: `{"event_id", "geofence_id", "vehicle_id", "occurred_at"}`. No coordinates.
Inside-window exits are rescanned each pass until they age out of the 24 h lookback. An evaluator outage longer than 24 h loses those alerts; this is recorded as a known limitation.
**Why:** no cursor column, so no schema beyond D-08/D-09. The 24 h lookback matches the P18 rebuild window. **How to apply:** the evaluator never writes to `geofence_events`.

### GF-294 — Insert, dedup and concurrency
Each pass is one transaction: lock, candidates, inserts, commit. Dedup is the candidate `NOT EXISTS` check run under the advisory lock. There is no unique index and no new column. An index on `alerts (fleet_id, kind, raised_at)` is not added in P19; the per-candidate existence checks are acceptable at 100–500 vans with rare alerts. If the table grows, that is a SPEC-AMEND row.
**Why:** keeps the schema to D-08/D-09. **How to apply:** do not add a unique index in P19 without a SPEC-AMEND row.

### GF-295 — Ack transition: trigger shape and responses
Trigger `alerts_guard` (BEFORE UPDATE, BEFORE DELETE, BEFORE TRUNCATE). An UPDATE passes only if `OLD.acked_by IS NULL AND OLD.acked_at IS NULL`, `NEW.acked_by` and `NEW.acked_at` are both non-null, and every other column is `IS NOT DISTINCT FROM` its old value. Anything else raises `check_violation`.
API ack: `UPDATE alerts SET acked_by, acked_at = now() WHERE id AND fleet_id AND acked_at IS NULL RETURNING …`. Zero rows: 404 if no alert with that id in the fleet, otherwise 409 `conflict`. The trigger is the backstop; the API never triggers it on the normal path.
Ack writes no `admin_audit` row and does not publish. `acked_by` is the principal's user id.
**Why:** the one permitted state change, ack once. 409 rather than 400 because the request is well formed and conflicts with state. **How to apply:** G1 tests call the trigger with raw SQL; G2 tests the API.

### GF-296 — Schedule semantics
- `schedule IS NULL` → never outside; no alert.
- `days` are ISO weekdays, 1 = Monday to 7 = Sunday. P17 validates the range but does not document the base; P19 pins ISO as a clarification of GF-265, not a change.
- The window is evaluated in `schedule.tz`, the zone the fence stores (validated by P17). `fleets.timezone` is not used for schedules. It remains the default for jobs.
- `from` inclusive, `to` exclusive, in local wall-clock minutes. Same-day window (`from < to`): inside iff the local weekday is in `days` and the time is in `[from, to)`. Overnight window (`from > to`): inside iff (the weekday is in `days` and time ≥ `from`) or (the previous weekday is in `days` and time < `to`).
- DST: the instant is converted with `t.In(loc)` and the wall clock is read from that. Both occurrences of a repeated fall-back hour get the same answer; a skipped spring-forward hour cannot be observed.
**Why:** the fence already stores its zone, and discarding it for the fleet zone would be wrong for a fence in another zone. **How to apply:** `InSchedule` is the only schedule logic in P19.

### GF-297 — Rule resolution and `enabled` semantics
Each pass loads all `alert_rules` in one query (at most three rows per fleet). No cache and no invalidation: a `PATCH` takes effect within one tick (≤ 30 s). `enabled = false` skips that kind for that fleet. `device_silent` takes `params.threshold_s` (default 600) and the SQL reads it per row. Re-enabling a rule alerts once for any silence that is still open, because the key was never written.
**Why:** three rows per fleet makes the cache a liability. **How to apply:** no cache type in P19.

### GF-298 — Role matrix and API shapes
| Route | Admin | Manager | Dispatcher | Driver | Anonymous |
|---|---|---|---|---|---|
| `GET /alerts` | ✅ | ✅ | ✅ | 403 | 401 |
| `PATCH /alerts/:id/ack` | ✅ | ✅ | ✅ | 403 | 401 |
| `GET /alert-rules` | ✅ | ✅ | 403 | 403 | 401 |
| `PATCH /alert-rules` | ✅ | ✅ | 403 | 403 | 401 |

`GET /alerts?since=<RFC3339>&limit=<n>`: `since` filters `raised_at >= since`, default now − 24 h; ordered `raised_at DESC, id DESC`; `limit` default 50, integer 1..200 else 400. Response: `{"alerts": [...], "unacked": N}`, where `unacked` counts all unacked alerts in the fleet regardless of `since`.
Alert object: `{id, fleet_id, kind, severity, payload, raised_at, acked_by, acked_at}`.
`PATCH /alert-rules` body: `{"rules": [{"kind", "enabled"?, "params"?}]}`, at most three entries; unknown kind 400; `params` validated per kind (`device_silent`: `threshold_s` integer 60..86400; others: `{}` only). Response: `{"rules": [{"kind", "enabled", "params", "updated_at", "evaluated"}]}` with the full list. `evaluated` is false for `eta_slip`.
**Why:** the role matrix is D-08 for rules and the dispatcher-reads-alerts row follows P17's dispatcher pattern. The README §8 table has no alerts row, so this section is the definition. **How to apply:** the fleet always comes from the principal.

### GF-299 — `alert.raised` fanout and bell update path
Subject `alerts.<fleet_id>` on core NATS. Envelope: `{"type": "alert.raised", "fleet_id", "alert_id", "kind", "severity", "raised_at", "payload"}`. `fleet_id` equals the subject suffix, or the hub drops it (`envelopeMatches`). The hub's existing `alerts.*` subscription and `notDriver` relay carry it unchanged: drivers never receive alerts. `internal/fanout/` is not extended: the D-41 seam covers Redis positions and presence only.
Bell: polling every 30 s. No new WS client in `apps/web`. If G0 finds an existing `/stream` client on a page, that page may invalidate the bell query on `alert.raised`; otherwise polling is the only path.
**Why:** `alert.raised` reuses the hub relay that already exists, so the WS path needs no hub code. Polling is the README §3 fallback. **How to apply:** the bell reads `GET /alerts`, not the WS stream.

### GF-300 — Notifier stub
`notify.Notifier` (in `internal/alerts/notify.go`): `Notify(ctx, Notice) error`. `Notice{EventType, FleetID, AlertID, Severity, Kind, RecipientUserIDs}`. `LogNotifier` logs `event=alert.raised kind=… severity=… fleet_id=… user_id=…`, never an address or a payload body. Recipients are users with the manager role in the fleet (`admin` is not a recipient; G0 pins column names). Called after commit, for `critical` alerts only; a failure is logged and counted, and the alert stays.
**Why:** D-11 names managers and forbids addresses in logs. **How to apply:** the stub is the interface the P19 test uses; real delivery is out of scope.

### GF-301 — Metrics
Names follow GF-93, registered in the ingest registry with label sets pre-seeded to zero, not exposed until P22:
- `fleetops_alerts_raised_total{kind,severity}`
- `fleetops_alert_evaluation_duration_seconds`
- `fleetops_alert_evaluation_failures_total{stage}`, stage ∈ `rules`, `candidates`, `insert`, `publish`, `notify`
- `fleetops_alert_passes_skipped_total`
**Why:** the same conventions as P18. **How to apply:** no `/metrics` route in P19.

### GF-302 — CLAUDE.md rows and scope fence additions
CLAUDE.md gets an alerts block of at most 12 lines: rule kinds, the `alerts` endpoints, the 30 s evaluator, the Notifier stub. No env vars, no ports, no make targets.
Scope fence additions: `internal/seed/**` (the per-fleet rule seed hook; `tools/seed` is untouched), `services/ingest/main.go` (goroutine wiring), `services/api/server.go` (one route registration line), `internal/hub/*_test.go` (test only), `db/roundtrip_test.go` (version bump only), `apps/web/src/alerts/**`, and the topbar line in `map/MapPage.tsx`, `jobs/JobsPage.tsx` and `geofences/ZonesPage.tsx`.
**Why:** each is the minimal touch the design needs. **How to apply:** anything outside this list fails the scope check.

## Validation

Runnable on this host; each has a pass condition.

- **(a)** `make fmt-check`, `make lint`, `make test` all exit 0, including `internal/alerts` and the hub relay test. A failure in `internal/hub` counts only after an isolated re-run (`go test ./internal/hub -race -count=1`) also fails.
- **(b) Scope fence:** `git diff --name-only main...HEAD` lists only the paths in GF-302 plus `specs/features/19-alerts-core.md`, `prompts/`, `CLAUDE.md`, `db/migrations/011_alerts_core.{up,down}.sql`, and `internal/alerts/**`. Pass: nothing else.
- **(c) No secrets:** `git diff main...HEAD | grep -iE 'password|secret|BEGIN [A-Z ]*PRIVATE'` is empty.
- **(d) e2e untouched:** `git diff --name-only main...HEAD -- e2e/` is empty. `make e2e` passes if the host has Docker and Playwright; if not, the spec records "not run" with the reason.
- **(e)** `wc -l CLAUDE.md` is below 200.
- **(f) Determinism:** `go test ./internal/alerts -run TestSilenceEpisodesProperty -race -count=1` passes twice in a row, with a fresh rapid seed each run (no fixed seed; the seed printed on failure is recorded in the spec and added as a regression case).
- **(g) P20 boundary:** `grep -rniE 'osrm|/reports|routing|/metrics' internal/alerts services/api/server.go` is empty. `eta_slip` appears only in the rule kind list and the seed.
- **(h) Web gates:** `npm --prefix apps/web run typecheck`, `npm --prefix apps/web test`, `npm --prefix apps/web run build` all exit 0.
- **(i) Ack idempotence:** `go test ./internal/alerts -run TestAlertsAckTrigger -count=1` passes; the second ack is rejected at the trigger and returns 409 at the API.
- **(j) driver-pwa untouched:** `git diff --name-only main...HEAD -- apps/driver-pwa/` is empty; baseline from G0 recorded.
- **(k) No geofence change:** `git diff --name-only main...HEAD -- internal/geofence/` is empty.

## Testing depth

| Layer | What | Where | Roadmap P19 item |
|---|---|---|---|
| Property (rapid, D-37) | silence episodes: one alert per episode, re-arm on report, alert matches oracle; `rapid.Check` with a fresh seed per run, plus three fixed streams in the same file | `internal/alerts/silence_property_test.go` | "property test on silence episodes" |
| Integration (PostGIS) | ack trigger: second ack rejected, DELETE/TRUNCATE/extra UPDATE rejected; seed and backfill; round-trip `011` | `internal/alerts`, `db/roundtrip_test.go` | "ack exactly once; DELETE and extra UPDATE rejected" |
| Integration | silence raises once and re-arms; exit outside schedule fires only outside window; cross-fleet isolation | `internal/alerts/evaluator_test.go` | "silence raises once and re-arms" · "geofence_exit_outside_schedule fires only outside the window" |
| Integration (NATS+WS) | `alert.raised` reaches the dispatcher WS client via `alerts.<fleet_id>`; driver receives nothing | `internal/hub/alerts_relay_test.go` | "alert.raised reaches the WS hub" |
| Integration (API) | role matrix, `since`, `limit`, ack 200/404/409, rules PATCH validation | `internal/alerts/http_test.go` | — |
| Unit | schedule windows including DST and overnight; `decideSilence` boundary; param validation; Notifier no-PII and critical-only | `internal/alerts/*_test.go` | "unit tests for schedule windows across fleet timezone (DST boundaries)" |
| Component (vitest) | bell count, list, ack, 409 message | `apps/web/src/alerts/*.test.tsx` | "bell shows unacked count" |
| Existing gates | Go suite, web gates, e2e untouched; driver-pwa baseline only | — | "existing gates unchanged green" |

## SPEC-AMEND repeat

P19 adds **no new SPEC-AMEND row**. D-08 (`alert_rules`), D-09 (`alerts.acked_at`, trigger) and D-11 (Notifier) are registered in mission §7, and the `alerts` table shape is already in README §5. The CHECK constraints, the seed backfill and the trigger are part of those tables and the D-09 transition. No index, no extra column, no extra table. If a later step adds one, it becomes a SPEC-AMEND row before it lands.

The GF-192 hub identity binding remains open and belongs to P23.

## Out of scope

- `eta_slip` evaluator, OSRM, ETA recomputation (P20, GF-290).
- Reports and CSV export (P21).
- Dashboards and the `/metrics` export (P22).
- k6 load tests (P23).
- Real email or SMTP delivery beyond the D-11 stub.
- Push notifications (GF-1).
- Alert-rules UI (the API exists; the UI is not in the roadmap P19 scope).
- README edits (R-8); compose and PORTS changes; `e2e/` changes; `apps/driver-pwa` changes.
- Any `geofences` or `geofence_events` schema change, and any P18 evaluator change.
- Any hub production change beyond the test file.
- Merge to `main`, push, and any P20+ content.
