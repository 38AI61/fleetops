# P13 plan: dispatcher assignment UI and nearest-van suggestion

Status: approved plan, persisted 2026-10-27. Branch `phase-13-dispatcher-assignment-ui`.

Source plan: `/root/.claude/plans/p13-plan-tingly-wigderson.md` (approved).

This spec is written before any P13 code (R-1, SDD). Implementation starts at Plan task group G0.

## Scope

P13 (roadmap §3) gives dispatchers a jobs screen: list and filter jobs, create a job by picking pickup and dropoff on the map, and assign it to the nearest active van. The backend already has everything needed except the nearest read. `POST /jobs`, `GET /jobs` and `POST /jobs/:id/assign` exist from P10, and `vehicles_latest` (migration 007) carries positions with geography GIST indexes. So P13 adds one read endpoint, one frontend area, and no migration, no mutation code, and no new port.

Facts checked in this plan that correct the earlier proposal:

- `GF-175` requires `driver_id` in the assign body. A vehicle with no current driver cannot be assigned, so the nearest list excludes it (GF-210) rather than showing rows that always fail.
- `ST_Distance` on `geography` uses the WGS84 spheroid by default (`use_spheroid = true`). It is not haversine. The fixture therefore uses the ellipsoidal meridian-arc length as the reference (GF-212).
- `internal/mapview` uses `database/sql` with `lib/pq`, not `pgx`. The new store mirrors that.
- The Vite dev proxy has no `/jobs` entry (`apps/web/vite.config.ts`). The jobs UI cannot reach the API in dev without adding it (GF-222).

In scope (roadmap P13 only):

1. Jobs list with filters (state, driver, day) and a create form. Pickup and dropoff are picked on the map. The form also takes time window, notes, size class and weight.
2. Assignment panel listing the nearest active vehicles to a job's pickup, ordered by `ST_Distance` on `vehicles_latest` within the caller's fleet, with an assign action that calls `POST /jobs/:id/assign`.
3. Validation: nearest-van ordering matches a hand-computed fixture (`TestNearestOrderingFixture`).
4. Testing depth: PostGIS integration (fleet-scoped nearest) and Vitest component tests for the form and the panel.

Out of scope: see the final section.

## Plan

Build order. Each group names its tests. Groups G1–G4 are backend and must be green before G5.

**G0 — Preflight (no code).**
- Baseline `make fmt-check lint test` green on `phase-13-dispatcher-assignment-ui`; `npm --prefix apps/web ci --include=dev`, `typecheck`, `test`, `build` green.
- Confirm `db/migrations` ends at `009_jobs_core`, and that `vehicles` (`id, fleet_id, plate, status, paired_device_id`), `vehicles_latest`, `vehicle_pairings` (`driver_id` nullable, `ended_at`, one-open-row partial index), `drivers` (confirm the name column, expected `name`) and `jobs` (`size_class`, `weight_kg`, `notes` nullability, and whether `size_class` is optional in `createBody`) have the columns used below.
- Confirm highest decision id is GF-208 (it is).
- Confirm GF-175 (driver required) and GF-177 (role cells) as quoted in the Requirements section.
- Test: none. Gate: all of the above green or any discrepancy recorded in the spec before G1.

**G1 — `internal/dispatch` nearest store.** (GF-209, 210, 211, 212)
- `Store{DB *sql.DB}` with `Nearest(ctx, fleetID int64, lat, lng float64, limit int) ([]Candidate, error)`. One SQL statement. Joins `vehicles` (status `active`), `vehicles_latest`, an open `vehicle_pairings` row (`ended_at IS NULL`, `driver_id IS NOT NULL`) and `drivers` (`fleet_id = $1`). Orders by `ST_Distance(vl.geom, pt) ASC, vl.vehicle_id ASC`, `LIMIT $n`.
- `Candidate` fields: `VehicleID, Plate, DriverID, DriverName, DistanceM, Lat, Lng, ServerTS`.
- Tests:
  - `TestNearestOrderingFixture` (`internal/dispatch`, testcontainers `testinfra.PostgisImage`). Fixture described in Testing depth.
  - `TestNearestFleetScope` (`internal/dispatch`). Pickup placed inside the other fleet's cluster; no row from the other fleet ever returned.

**G2 — Nearest HTTP route.** (GF-213, 214, 215)
- `internal/dispatch/http.go`: `Handler{Store}`, `Register(e, authn)`. Route `GET /jobs/:id/nearest?limit=`, group `readers` (admin, manager, dispatcher), role check before the query.
- Handler: `idParam` idiom (malformed id → 404); job lookup `SELECT pickup geometry FROM jobs WHERE id=$1 AND fleet_id=$2` (missing → 404, cross-fleet → 404); `limit` default 10, cap 50 (GF-211); the job's pickup is the reference point.
- Wire in `services/api/server.go`: `dh := &dispatch.Handler{Store: &dispatch.Store{DB: d.DB}}`, `dh.Register(e, auth.RequireAuth(d.Issuer))`.
- Add role-matrix row to `services/api/rolematrix_test.go` with a per-row `setup` hook that creates a fresh `created` job (GF-177 idiom).
- Tests:
  - `TestNearestEndpoint` (`services/api` harness): dispatcher on fixture job → 200 with ordered candidates matching G1; job in other fleet → 404; malformed id → 404; `limit=0`, `limit=51`, `limit=abc` → 400 `validation_failed`; `limit` absent → 200 with at most 10; driver → 403 before lookup.
  - `TestRoleMatrix` extended with the nearest row (GF-215).
  - `db/roundtrip_test.go` unchanged and green.

**G3 — Vite proxy and API client (frontend data layer).** (GF-217, GF-222)
- `apps/web/vite.config.ts`: add `/jobs` → `http://127.0.0.1:10010` alongside `/auth`, `/map`, `/vehicles`.
- `src/api/types.ts`: `Job` (GF-173 fields), `JobState`, `JobCreateBody`, `NearestCandidate`, `NearestResult`.
- `src/api/jobs.ts`: `listJobs(filter)`, `createJob(body)`, `assignJob(id, vehicleId, driverId)`, `fetchNearest(id, limit)`, each via `getJson`/`apiFetch`.
- `src/jobs/queries.ts`: `useJobs(filter)`, `useNearest(jobId, enabled)` (`staleTime: 0`), `useCreateJob()`, `useAssignJob()` (invalidates `['jobs']` and `['nearest', id]` on success).
- Tests: `src/api/jobs.test.ts` (URL and body shape per function; mock `apiFetch`).

**G4 — Pure modules and MapView pick mode.** (GF-218, 219, 220)
- `src/jobs/pick.ts`: `pickReducer(state, click)`. First click sets pickup; second sets dropoff; third and later replace dropoff. `clearPickup`, `clearDropoff` return new state. Pure, map-free.
- `src/jobs/format.ts`: `formatDistance(m)`: below 1000 m → `"<n> m"` (rounded); otherwise `"<x.xx> km"`. Pure.
- `src/jobs/form.ts`: `validateJobForm(values)` returns field errors. Rules: pickup and dropoff required; window start ≤ end when both set; notes ≤ 1000 runes (`Array.from(notes).length`); weight > 0 when set; size class one of `small|medium|large` when set. `toCreateBody(values)` converts `datetime-local` to RFC3339 via `new Date(v).toISOString()`.
- `src/map/MapView.tsx`: two optional props: `onMapClick?(lngLat)` (fires on empty-space click when provided, replacing `onDeselect` only in that mode) and `marks?: {pickup?, dropoff?}` rendered as a small GeoJSON source `'picks'` with one circle layer. Existing props and behavior unchanged.
- Tests: `src/jobs/pick.test.ts` (first, second, third-replaces-dropoff, clear), `src/jobs/format.test.ts` (0, 999.4, 1000, 1234.5, 12345.6), `src/jobs/form.test.ts` (rules and `toCreateBody`). `MapView` itself is not jsdom-tested (no MapLibre under jsdom); the logic it calls is.

**G5 — JobCreateForm.** (GF-220)
- `src/jobs/JobCreateForm.tsx`: props `pickup`, `dropoff`, `onClearPickup`, `onClearDropoff`, `onSubmit(body)`, `pending`. Fields: window start and end (`datetime-local`), notes (textarea, counter), size class (select), weight kg (number). Submit disabled while `pending` or when `validateJobForm` returns errors; errors render inline.
- Tests: `src/jobs/JobCreateForm.test.tsx` (Testing Library): required pickup/dropoff errors shown and submit disabled until both set (props set by test); window, notes, size and weight wire through to `onSubmit` body; notes over 1000 runes blocked; submit disabled while `pending`.

**G6 — AssignmentPanel and assign wiring.** (GF-216, GF-221, GF-223)
- `src/jobs/AssignmentPanel.tsx`: container uses `useNearest(jobId, enabled)`; presentational table of rows (plate, driver name, `formatDistance`, last seen). Row select, then "Assign" calls `useAssignJob().mutate({ jobId, vehicleId, driverId })`. Assign disabled while pending or no row selected. Errors mapped: `vehicle_not_active` → "Vehicle is no longer active", `conflict` → "Job changed; refresh", other → generic. Rows come back unfiltered by presence; no display-status filter (GF-223).
- The panel calls only the existing `POST /jobs/:id/assign`. No new assign code (GF-216).
- Tests: `src/jobs/AssignmentPanel.test.tsx` (mocks `src/api/jobs`): rows render from mock nearest data in server order; distances formatted; assign button calls `assignJob` with the picked vehicle and driver; disabled while pending; `vehicle_not_active` error message shown.

**G7 — JobsPage and routing.** (GF-218)
- `src/jobs/JobsPage.tsx`: filter controls (state, driver id, day), table of jobs, row select opens `AssignmentPanel` for `created`/`assigned` jobs.
- `src/jobs/NewJobPage.tsx`: `MapView` in pick mode (`onMapClick` → `pickReducer`, `marks` from state) beside `JobCreateForm`; on success navigate to `/jobs`.
- `src/App.tsx`: `/jobs` and `/jobs/new` under `RequireSession`, lazy-loaded like `/map`. Topbar link to `/jobs` in `MapPage`.
- Tests: `src/jobs/JobsPage.test.tsx` (mocked `src/api/jobs`): filters change the query arguments; panel opens on selectable row. `App.test.tsx` extended: `/jobs` redirects to login without session.

**G8 — CLAUDE.md and docs rows.**
- Add mechanical rows only: `GET /jobs/:id/nearest` contract (role, `limit` 1..50 default 10, response fields), jobs UI routes, `/jobs` added to the dev proxy list. File stays under 200 lines (currently 130). No env names, make targets or `FLEETOPS_*` tunables.
- Test: `wc -l CLAUDE.md` < 200.

**G9 — Full validation run.** See Validation. Any failure goes back to the owning group before the next one starts.

## Requirements

### Carried from existing decisions (cite, do not restate)

- **D-23**: job lifecycle, `POST /jobs/:id/assign` owned by the dispatcher/admin, transitions in `internal/jobs/transitions.go`.
- **D-26**: dispatcher assignment of vehicle and driver to a job.
- **D-27**: registry status `active` is the only "active" for vehicles; display status (`idle`/`en_route`/`offline`) is derived separately.
- **D-38**: web dev server 10012, `/stream` proxied to 10011 (hub unchanged).
- **D-37**: frontend test tooling (Vitest, Testing Library, jsdom).
- **R-6**: every endpoint has a role-matrix row.
- **GF-170/171/173/174**: job columns, trigger, job object, create contract (P10).
- **GF-175**: assign body `{vehicle_id, driver_id}` both required; `vehicle_not_active` 409; driver must exist in fleet; replay and reassign rules.
- **GF-177**: role-matrix cells for jobs routes; role checks before body read.
- **GF-178**: error envelope mapping.
- **GF-179**: publish on applied transitions (unchanged; the assign path already publishes).
- **GF-187/188**: ownership guard and `idParam` idiom (P11).
- **GF-196**: job object shape with `proof_photo_url` (P12).
- **GF-133/135**: map conventions (P8/P9): circle layers, bbox and follow helpers.

### New decisions (GF-209 onward)

**GF-209 — Nearest store location.** The nearest query lives in new package `internal/dispatch` as `Store.Nearest`. It is a read. No mutation, no new table, no migration. *Why:* keeps `internal/jobs` and `internal/mapview` untouched; the query belongs with neither (it reads registry, pairings, and positions).

**GF-210 — Candidate eligibility.** A row is returned only when all hold:
- `vehicles.fleet_id = $1` and `vehicles.status = 'active'` (D-27);
- a `vehicles_latest` row exists for the vehicle (never-reported vehicles are not candidates);
- an open pairing exists (`vehicle_pairings.ended_at IS NULL`) with `driver_id IS NOT NULL`;
- the driver row exists with `drivers.fleet_id = $1`.

Vehicles with no open pairing, or an open pairing with no driver, are excluded. *Why:* GF-175 rejects assign without a driver, so listing such rows would offer actions that always fail. The roadmap decoy "unpaired vehicle must not appear" is therefore satisfied by the same rule. This corrects the earlier proposal that listed driverless rows.

**GF-211 — Ordering and limit.** `ORDER BY ST_Distance(vl.geom, pt) ASC, vl.vehicle_id ASC`, `LIMIT` from the query. `limit` default 10, cap 50; non-integer or outside 1..50 → 400 `validation_failed`. *Why:* ties are deterministic; cap bounds the response. Exact ORDER BY (not `<->` KNN) is kept: fleets here are small (≤ thousands per fleet) and the exact ordering keeps the distance and the order consistent by construction.

**GF-212 — Distance metric.** `ST_Distance` on `geography` (WGS84 spheroid, meters). The fixture reference is the ellipsoidal meridian-arc length (standard WGS84 series) computed by an independent helper in the test; the test asserts exact ordering and `distance_m` within ±1 m of that helper. The spec records the formula. *Why:* it is the metric PostGIS computes and the one the dispatcher sees; haversine would differ by up to ~0.5% and fail the tolerance. Ordering stays exact because fixture spacing is far larger than the metric difference.

**GF-213 — HTTP route.** `GET /jobs/:id/nearest?limit=` in `internal/dispatch/http.go`, group `readers` (admin, manager, dispatcher). Job must exist in the caller's fleet (`jobs.fleet_id = principal.FleetID`); missing or cross-fleet → 404 `not_found`; malformed id → 404 (`idParam`). No job-state gate: the panel decides when to show; the assign endpoint enforces state (GF-175). *Why:* keeps the read independent of state; the list contract (`GET /jobs`) stays untouched, as the alternative (folding into it) was rejected.

**GF-214 — Response shape.** `200 {"job_id": int, "candidates": [{"vehicle_id", "plate", "driver_id", "driver_name", "distance_m", "lat", "lng", "server_ts"}]}`. Field naming is snake_case like GF-173. Ordered per GF-211. Distance is a float rounded to 1 decimal in meters (rounding done in SQL with `round(..., 1)`). *Why:* the panel needs only these; no display status (see GF-223).

**GF-215 — Role-matrix row.** `GET /jobs/:id/nearest`: admin 200, manager 200, dispatcher 200, driver 403, unauthenticated 401. The driver cell is 403 because the role group rejects before any lookup (same as `GET /jobs`, GF-177). Per-row `setup` hook creates a fresh `created` job. *Why:* manager reads jobs (`GET /jobs` 200), so the nearest read follows the same read group.

**GF-216 — Assign reuse.** The assign action calls the existing `POST /jobs/:id/assign` with `{vehicle_id, driver_id}` from the chosen candidate. No new endpoint and no new assign code. `vehicle_not_active`, replay and reassign semantics come from GF-175 unchanged. The frontend maps the error codes to messages. *Why:* one assignment path keeps GF-179 publish and the `job_events` record in one place.

**GF-217 — Frontend data layer.** `src/api/jobs.ts` functions and `src/jobs/queries.ts` hooks mirror the existing `useMapVehicles` pattern (TanStack Query, `getJson`/`apiFetch`, `ApiError` codes). Nearest query uses `staleTime: 0` (rows change with positions and assignments). *Why:* matches P9 conventions; no new client.

**GF-218 — Routes and surfaces.** `/jobs` (list + assignment panel) and `/jobs/new` (create with map pick), both under `RequireSession`, lazy-loaded. Assignment panel is opened by selecting a row in `/jobs`; no `/jobs/:id` route. *Why:* the roadmap asks for a list and a create form; a detail route is extra surface.

**GF-219 — Pick mode.** `MapView` gains optional `onMapClick(lngLat)` and `marks`. The pick sequence is pure (`pickReducer`): first click sets pickup, second sets dropoff, third and later replace dropoff; explicit clear buttons reset each point. Existing `MapView` props and behavior are unchanged; vehicle click still calls `onSelect`. *Why:* the simplest sequence that never leaves the form stuck; re-picking pickup is an explicit action, not a side effect of a click.

**GF-220 — Create form.** Fields and rules: pickup and dropoff required (`{lat,lng}` from the map); `window_start` ≤ `window_end` when both set; notes ≤ 1000 runes; `size_class` in `small|medium|large` when set; `weight_kg` > 0 when set. Times are converted from local `datetime-local` to RFC3339 with `toISOString()`. Notes are sent as typed. Server remains authoritative (GF-174/178); the client check is for immediate feedback. *Why:* the server already validates; duplicating rules client-side only for UX.

**GF-221 — Assignment panel behavior.** Rows in server order; distance via `formatDistance`; assign disabled while pending or with no row selected; on success invalidate `['jobs']` and the nearest query; error codes mapped as in G6. *Why:* the dispatcher sees exactly the server's ordering; no client-side re-sort that could disagree with the fixture.

**GF-222 — Dev proxy.** `/jobs` → `127.0.0.1:10010` in `apps/web/vite.config.ts`. CLAUDE.md dev-proxy line updated. *Why:* without it the jobs UI cannot reach the API in dev (D-38 is the same proxy mechanism).

**GF-223 — No presence filter.** Nearest does not filter by display status (`offline` etc.); it shows `server_ts` so the dispatcher can judge freshness. *Why:* D-27 defines "active" as registry status; presence is a display concern. Filtering by presence would hide the nearest van during a brief GPS gap.

## Validation

Runnable on this host (Docker available for testcontainers). Pass conditions in brackets.

1. `make fmt-check` [exit 0].
2. `make lint` [exit 0; golangci-lint v2.15.0].
3. `make test` [exit 0; `-race -count=1`; includes `db/roundtrip_test.go` unchanged].
4. `go test ./internal/dispatch/... -race -count=1 -v -run 'TestNearestOrderingFixture|TestNearestFleetScope'` [both PASS; fixture expectations in the output match the test's helper values].
5. `go test ./services/api/... -race -count=1 -v -run 'TestNearestEndpoint|TestRoleMatrix'` [both PASS; the nearest row shows `200 200 200 403 401`].
6. `npm --prefix apps/web ci --include=dev` [exit 0].
7. `npm --prefix apps/web run typecheck` [exit 0].
8. `npm --prefix apps/web test` [exit 0; includes `JobCreateForm.test.tsx`, `AssignmentPanel.test.tsx`, `pick.test.ts`, `format.test.ts`, `form.test.ts`, `jobs.test.ts`, `JobsPage.test.tsx`].
9. `npm --prefix apps/web run build` [exit 0].
10. Scope fence: `git diff --name-only main...HEAD` touches only `internal/dispatch/`, `services/api/`, `apps/web/`, `CLAUDE.md`, `specs/features/13-dispatcher-assignment-ui.md`, `prompts/`, and the frontend lockfile if needed. It must not touch `README.md`, `deploy/PORTS.md`, `deploy/docker-compose.yml`, `specs/constitution/`, `db/`, `internal/hub/`, `internal/config/`, `internal/testinfra/`, `internal/jobs/`, `internal/mapview/`, `internal/proofs/` [empty intersection].
11. `ls db/migrations | tail -1` [`009_jobs_core.down.sql` / `.up.sql` — no new migration].
12. `wc -l CLAUDE.md` [< 200].
13. Secrets check: `git diff main...HEAD | grep -iE 'secret|password|key=' ` [no new secret values; only test fixtures named as such].

Manual follow-up (not a CI gate, roadmap §5): API latency for assign and nearest, first measured in P13. Note it in the spec's result section; do not automate.

## Testing depth

| Layer | Tests | Covers |
|---|---|---|
| Integration (PostGIS, `internal/dispatch`) | `TestNearestOrderingFixture` | Exact ordering and membership; `distance_m` within ±1 m of the meridian-arc reference; `LIMIT` case; decoys: other-fleet vehicle at zero distance, `maintenance`, `retired`, unpaired active, paired with NULL driver, never-reported vehicle, ended pairing — none appear |
| Integration (PostGIS, `internal/dispatch`) | `TestNearestFleetScope` | Pickup inside the other fleet's cluster; zero rows from the other fleet |
| Services/API harness | `TestNearestEndpoint` | 200 ordered; cross-fleet job 404; malformed id 404; `limit` 0/51/abc 400; default limit 10; driver 403 |
| Role matrix | `TestRoleMatrix` (new row) | GF-215 cells; `setup` hook per row |
| Migrations | `db/roundtrip_test.go` (unchanged) | No schema change |
| Vitest unit | `pick.test.ts`, `format.test.ts`, `form.test.ts`, `api/jobs.test.ts` | Pick sequence, distance formatting, validation and wire body, client URLs and bodies |
| Vitest component | `JobCreateForm.test.tsx`, `AssignmentPanel.test.tsx`, `JobsPage.test.tsx` | Form validation rendering and wire-through; panel rows, assign call with picked vehicle and driver, pending state, error copy; filters |
| Vitest route | `App.test.tsx` (extended) | `/jobs` gated by session |
| Race | `make test` (`-race -count=1`) | All Go packages |

**Fixture derivation (GF-212, roadmap validation).** Pickup at `(lat 0, lng 0)`. Vehicles placed on the same meridian at latitudes that give well-separated distances (e.g. 0.01°, 0.02°, 0.05°, 0.20°, plus one in a different longitude at a clearly larger distance to show ordering is not by latitude alone). Expected distance for a meridian offset is the difference of meridian arc lengths `M(φ) = a(1−e²)·[A0·φ − A2·sin2φ + A4·sin4φ − A6·sin6φ]` with WGS84 `a`, `e²` and standard `A0..A6` coefficients, computed by an independent helper in the test. The derivation is recorded in the spec with the helper's output for each vehicle. Tolerance ±1 m.

## SPEC-AMEND repeat

P13 adds **no new SPEC-AMEND row**. The nearest endpoint is new read surface on top of the already-amended P10 job endpoints; `POST /jobs/:id/assign` remains owned by D-23 and is only called, not changed. Recorded as GF-209–223, not as an amendment.

GF-192 (hub identity binding) remains open. P13 does not touch the hub; GF-192 now belongs to P23, the only later phase that names it.

## Out of scope

- Driver PWA and its offline queue (P14, P15).
- Playwright or any E2E test (P16).
- Hub changes, including GF-192 (P23).
- Geofence drawing or evaluation (P17+).
- ETA and OSRM routing (P20).
- Reports (P21).
- Any `db/` migration.
- Changes to `internal/config` or `internal/testinfra`.
- Changes to `README.md`, `deploy/PORTS.md`, `deploy/docker-compose.yml`, `specs/constitution/`.
- Compose start, merge to main, push, or any P14+ content.
- Changes to `internal/jobs/` and `internal/mapview/` (reused, not modified).

## Execution record

**G0 preflight (2026-10-27, branch `phase-13-dispatcher-assignment-ui`).** All facts hold: `db/migrations` ends at `009_jobs_core`; `vehicles` has `id, fleet_id, plate, status, paired_device_id`; `vehicles_latest` has `vehicle_id, fleet_id, geom, speed_kmh, heading_deg, device_ts, server_ts`; `vehicle_pairings.driver_id` is nullable with `ended_at` and a partial unique index on open `vehicle_id`; `drivers.name` exists; `jobs.size_class`, `weight_kg`, `notes` are nullable and `createBody.SizeClass` is a pointer, so size class is optional; GF-208 is the highest decision id; GF-175 (driver required) and GF-177 (role cells) match the quotes above. Baseline `make fmt-check lint test` and `npm --prefix apps/web ci --include=dev`, `typecheck`, `test`, `build` are green before any P13 code.

**Correction to the GF-212 fixture formula (G1).** The line above writes `M(φ) = a(1−e²)·[A0·φ − …]` with `A0 = 1 − e²/4 − …`. That mixes two forms and is off by the factor `(1−e²)`, about 0.67% (7.4 m at 1106 m, 148 m at 22 km), so the reference would fail the ±1 m tolerance. The correct series is `M(φ) = a·[A0·φ − A2·sin2φ + A4·sin4φ − A6·sin6φ]` with no prefactor. `TestMeridianArcReference` checks it against the published WGS84 quarter meridian (10001965.729 m). The equatorial vehicle at a longitude offset uses `a·Δλ` (exact along the equator).

**Fixture derivation output (G1, pickup at lat 0 lng 0, `TestNearestOrderingFixture`).**

| Vehicle | Position | Reference (m) | PostGIS (m, 0.1 rounded) |
|---|---|---|---|
| FX-LAT-001 | lat 0.01, lng 0 | 1105.743 | 1105.7 |
| FX-LAT-002 | lat 0.02, lng 0 | 2211.486 | 2211.5 |
| FX-LAT-005 | lat 0.05, lng 0 | 5528.714 | 5528.7 |
| FX-LNG-010 | lat 0, lng 0.10 | 11131.949 | 11131.9 |
| FX-LAT-020 | lat 0.20, lng 0 | 22114.856 | 22114.9 |

Decoys (none returned): other-fleet vehicle at zero distance, `maintenance`, `retired`, unpaired active, paired with NULL driver, ended pairing, paired to an other-fleet driver, and never-reported.

**G9 validation record (2026-10-27).** All checks in the Validation section were run on this branch and passed: `make fmt-check` (0), `make lint` (0, 0 issues), `make test` (0, 24 packages ok, includes `db/roundtrip_test.go`), `go test ./internal/dispatch/...` with `TestNearestOrderingFixture` and `TestNearestFleetScope` (PASS), `go test ./services/api/...` with `TestNearestEndpoint` and `TestRoleMatrix` (PASS; nearest row `200 200 200 403 401`), `npm --prefix apps/web ci --include=dev`, `typecheck`, `test` (13 files, 68 tests) and `build` (all 0). Scope fence touches only allowed paths; `db/migrations` ends at `009_jobs_core`; CLAUDE.md is 136 lines.

**Manual follow-up (roadmap §5, not a CI gate).** API latency for assign and nearest is first measurable in P13 and has not been measured yet. Measure it against a running API and record the numbers here.
