# Feature 17 — Geofence registry & drawing (P17)

Status: implemented on `phase-17-geofence-registry-drawing` (G0–G4). Validation results and deviations are at the end of this file. `make test` is not green: `db/roundtrip_test.go` still expects migration version 9 (see deviation D1).
Source plan: `/root/.claude/plans/p17-plan-wiggly-glacier.md` (approved).

Target file: `specs/features/17-geofence-registry-drawing.md` (to be persisted verbatim on branch `phase-17-geofence-registry-drawing`). Decisions GF-260…GF-277. Highest existing decision is GF-259.

Two findings from research change the brief and are decided below: (1) no migration creates `geofences` yet, so P17 creates the base table (GF-260); (2) README §8 lets dispatchers write geofences, which conflicts with the admin-only write rule in the brief (GF-268).

## Scope

P17 adds the geofence registry and its draw UI. It does not evaluate any fence:

- one migration pair `010_geofences_events` creates `geofences` (README §5 base shape, plus the D-26/D-33 columns `radius_m`, `bbox`, `dwell_min`) and `geofence_events` with its append-only trigger;
- `GET/POST/DELETE /geofences` in GeoJSON, JWT, fleet-scoped;
- the §8 role matrix extended per R-6;
- a MapLibre draw UI in `apps/web` (route `/zones`) that produces the GeoJSON POST consumes and renders stored fences read-only.

The evaluator (bbox prefilter, `ST_Covers`/`ST_DWithin`, debounce, enter/exit/dwell emission) is P18. P17 writes zero `geofence_events` rows.

## Plan

Build order. Each group names its tests. Commands run from the repo root.

**G0 — Preflight.**
- Baseline gates green on the phase branch before any change:
  - Go: `make fmt-check`, `make lint`, `make test` (PostGIS tests need Docker).
  - `apps/web`: `npm --prefix apps/web run typecheck`, `npm --prefix apps/web test`, `npm --prefix apps/web run build`.
  - `apps/driver-pwa`: same three commands, baseline only; the app is not changed.
- Record pins: migrations 001–009 (9 files, pair each); highest decision GF-259 (`grep -rhoE 'GF-[0-9]+' specs | sort -t- -k2 -n | tail -1`); `CLAUDE.md` line count (169 at P16 close, must stay under 200); `maplibre-gl` 5.24.0 in `apps/web/package-lock.json`.
- Read the id types of `fleets` and `vehicles` (migrations 001 and 005) and use the same types for the new foreign keys.
- Tests: none new.

**G1 — Migration `010_geofences_events` (one up/down pair).**
- Up, in order:
  - `geofences`: `id bigserial PK`; `fleet_id` (fleets id type) `NOT NULL REFERENCES fleets(id)`; `name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100)`; `kind text NOT NULL CHECK (kind IN ('polygon','circle'))`; `geom geography(Geometry,4326) NOT NULL`; `radius_m double precision`; `bbox geometry(Polygon,4326) NOT NULL`; `dwell_min integer CHECK (dwell_min BETWEEN 1 AND 1440)`; `schedule jsonb`.
  - Shape check: `(kind='polygon' AND radius_m IS NULL AND GeometryType(geom::geometry)='POLYGON') OR (kind='circle' AND radius_m BETWEEN 1 AND 100000 AND GeometryType(geom::geometry)='POINT')`.
  - Indexes: `geofences_fleet_name_uq UNIQUE (fleet_id, lower(name))`; `geofences_bbox_gix USING GIST (bbox)`.
  - `geofence_events`: `id bigserial PK`; `fleet_id` `NOT NULL REFERENCES fleets(id)`; `geofence_id bigint NOT NULL REFERENCES geofences(id) ON DELETE RESTRICT`; `vehicle_id` (vehicles id type) `NOT NULL REFERENCES vehicles(id)`; `kind text NOT NULL CHECK (kind IN ('enter','exit','dwell'))`; `occurred_at timestamptz NOT NULL`; `created_at timestamptz NOT NULL DEFAULT now()`; index `geofence_events_fleet_at_idx (fleet_id, occurred_at)`.
  - Trigger: function `geofence_events_append_only()` raises `'geofence_events is append-only'` with `ERRCODE = 'check_violation'`; `geofence_events_no_update_delete` BEFORE UPDATE OR DELETE FOR EACH ROW; `geofence_events_no_truncate` BEFORE TRUNCATE FOR EACH STATEMENT. Copies `009_jobs_core.up.sql:61-74` and `003_admin_audit.up.sql:11-24`.
- Down: drop triggers, function, `geofence_events`, `geofences`.
- No `BEGIN`/`COMMIT`, no `CREATE INDEX CONCURRENTLY` (GF-5).
- Tests:
  - `TestGeofenceEventsAppendOnly` (`internal/geofence/store_test.go`): insert one fixture row via SQL (test-only; no app emission path exists in P17), then UPDATE, DELETE and TRUNCATE each fail with message containing `append-only`.
  - `TestGeofenceBboxIndexUsed` (`internal/geofence/store_test.go`): see GF-270.
  - Existing migration round-trip test passes with 010 (up, down 1, up).

**G2 — `/geofences` API (`internal/geofence` + `services/api`).**
- Files:
  - `internal/geofence/validate.go`: GeoJSON Feature in, normalized values out.
  - `internal/geofence/bbox.go`: server-side bbox for polygon and circle.
  - `internal/geofence/store.go`: SQL, one transaction per write.
  - `internal/geofence/http.go`: handlers and role groups (pattern `internal/registry/http.go:28-50`).
  - `services/api/server.go`: register beside the registry and jobs routes.
  - `services/api/rolematrix_test.go`: rows per GF-268.
- Behaviour: GF-261 to GF-269.
- Tests (integration, `testinfra.NewMigratedDB`, no build tags):
  - `TestGeofenceRoundTrip`: POST a counter-clockwise polygon and a circle; GET list; the returned Feature equals the posted geometry and properties (exact coordinate equality).
  - `TestGeofenceRejectsInvalidPolygon`: self-intersecting bowtie, open ring, 3 positions, hole, more than 1000 positions, lng 200. Each → 400 `validation_failed`; GET count unchanged.
  - `TestGeofenceRejectsBadFields`: radius 0 and 100001; radius on polygon; circle without radius; dwell 0 and 1441; tz `Mars/Base`; from == to; days `[0]` and `[1,1]`; malformed HH:MM; unknown property. Each → 400; nothing stored.
  - `TestGeofenceScheduleAccepted`: valid schedule including overnight (22:00 to 06:00); stored canonical (days sorted).
  - `TestGeofenceNameUnique`: case-insensitive duplicate in the same fleet → 409 `conflict`.
  - `TestGeofenceFleetScope`: fleet B's fence absent from GET; DELETE → 404 `not_found`; malformed id → 404.
  - `TestGeofenceDeleteBlockedByEvents`: no events → DELETE 204; one fixture event row → DELETE 409 `conflict`.
  - `TestCircleBboxCovers` (unit, `internal/geofence/bbox_test.go`): for lat 0, 52.5 and 70, the bbox contains points on the circle at every 45° step.
  - Role matrix rows in `services/api/rolematrix_test.go`: `GET /geofences`, `POST /geofences`, `DELETE /geofences/:id` (fixture per caller, the `jobRowSetup` pattern). Expected values in GF-268.

**G3 — Draw UI (`apps/web`).**
- Files:
  - `apps/web/src/geofences/draw.ts`: pure draw state and GeoJSON conversion (GF-272).
  - `apps/web/src/geofences/api.ts`: TanStack Query hooks for `GET/POST/DELETE /geofences`, following `jobs/queries.ts`.
  - `apps/web/src/geofences/ZonesPage.tsx`: list, draw controls, save and delete; login required.
  - `apps/web/src/map/MapView.tsx`: optional `fences` and `draft` props, each a GeoJSON source with fill, line and circle layers. `onMapClick` is reused (P13 pattern). Absent props leave the map unchanged.
  - `apps/web/src/App.tsx`: route `/zones` and a nav link.
  - `apps/web/vite.config.ts`: proxy `/geofences` to `127.0.0.1:10010` (XHR only; GF-273).
- Tests:
  - `apps/web/src/geofences/draw.test.ts`:
    - polygon path: three clicks, then close by clicking the first vertex, gives 4 positions with first equal to last and `[lng,lat]` order;
    - Escape cancels to idle; fewer than 3 vertices cannot close;
    - circle path: centre plus radius 500 gives a Point with `properties.radius_m` 500;
    - `circleRing` is closed, 65 positions, each within 0.5% of the radius;
    - client bounds mirror the server (0 and 100001 rejected).
  - `apps/web/src/geofences/ZonesPage.test.tsx`: with the list mocked, admin and dispatcher see draw controls, manager sees none.
  - No jsdom map test (GF-167). Existing web tests unchanged.
- Gates: typecheck, test, build.

**G4 — CLAUDE.md rows, mission §7 row, full validation.**
- `CLAUDE.md`: add a "Geofences (P17)" block of at most 12 lines (endpoints, role rule, validation summary, route `/zones`, dev proxy `/geofences`). No env vars, no ports.
- `specs/constitution/mission.md` §7: one SPEC-AMEND row (see SPEC-AMEND repeat).
- Persist `specs/features/17-geofence-registry-drawing.md` with the validation results filled in.
- Run every check in Validation.

## Requirements

Carried from earlier sources (cited, not restated):

- **D-26** (`geofences.dwell_min` and jobs columns; mission §7 row, line 73) and **D-33** (geofence model: `radius_m`, `bbox`, `dwell_min`, `geofence_events`; mission §7 row, line 77; tech-stack §11 lines 151–152). P17 covers the columns and table; the evaluation semantics are P18.
- **D-37** (PostGIS via testcontainers, `postgis/postgis:16-3.4`; tech-stack §9 role-matrix test, line 234).
- **D-38** (MapLibre GeoJSON circle layers, not DOM markers; tech-stack line 297).
- **R-6** (roadmap §1 line 14): an endpoint phase extends the §8 role-matrix test.
- **R-7** (roadmap §1 line 15): append-only tables use a trigger in the creating phase. Timing for `geofence_events` is reconciled in GF-266.
- **README §2.1.6** (line 31: dispatcher draws polygons and circles), **§5** (line 97: `geofences` DDL), **§7** (enter/exit/dwell semantics, P18 only), **§8** (line 175: Geofences row).
- **Mission §2** manager row (line 17) and line 21: the §8 matrix is authoritative over the summary.
- **P10 `job_events` trigger** (`009_jobs_core.up.sql:61-74`): the append-only precedent.
- **P8 `vehicles_latest` EXPLAIN test** (`internal/mapview/store_test.go`, around lines 176–198): the index-assertion precedent.
- **apierr**: error body is `{"error": code}` only (`internal/apierr/apierr.go:32`).
- **GF-192** (hub identity binding) stays open; it belongs to P23.

Decisions (GF-260 to GF-277):

- **GF-260 — P17 creates the `geofences` base table.** No migration creates it: P3 excludes geofences (`specs/features/03-registry-pairing.md:19`), and migrations 001–009 contain none. README §5 is the base shape; the D-26/D-33 columns are the additions, all in migration 010. Migration count goes from 9 to 10. The brief's "additive migration" means one migration that creates the base shape plus the additive columns.

- **GF-261 — Circle stores centre plus radius.** `kind='circle'` stores the centre as a Point in `geom` and the radius in `radius_m` (1 to 100000 m). It is not a polygon approximation. Reason: P18's `ST_DWithin` needs the exact radius, and an approximation would drift at the boundary for enter/exit tests.

- **GF-262 — bbox is computed server-side on every write and never taken from the client.** Polygon: `ST_Envelope(geom::geometry)`. Circle: a Go helper with `dLat = r/111320 × 1.001`, `dLng = dLat / max(cos(lat), 0.01)`, clamped to valid ranges, returned as a Polygon. Reason: a prefilter that misses a fence is a P18 correctness bug. The 0.1% margin and cosine floor cover the geography-versus-planar difference, and `TestCircleBboxCovers` checks containment. Responses expose it as the GeoJSON `bbox` member.

- **GF-263 — Wire format is GeoJSON Feature.**
  - POST body: `{"type":"Feature","geometry":…,"properties":{"name","kind","radius_m"?,"dwell_min"?,"schedule"?}}`. Kind must match geometry: `polygon` with `Polygon`, `circle` with `Point`. Client `id` and `bbox` are ignored. Unknown property → 400.
  - POST response: 201 with the Feature, `id` and `bbox` added.
  - GET: FeatureCollection, fleet-scoped, ordered by id ascending, no pagination and no per-fleet cap in P17.
  - DELETE: 204, no body. Hard delete (GF-267).
  - Coordinates are returned with `ST_AsGeoJSON(geom::geometry, 15)` so posted values round-trip exactly.
  - Errors use apierr codes only: 400 `validation_failed`, 401 `unauthorized`, 403 `forbidden`, 404 `not_found`, 409 `conflict`.

- **GF-264 — Geometry rules and validity policy.**
  - Polygon: exactly one exterior ring, no holes; 4 to 1000 positions; first equals last; lng in [-180, 180], lat in [-90, 90], all finite; lng span at most 180° (no antimeridian crossing).
  - Storage normalizes the ring to counter-clockwise with `ST_ForcePolygonCCW` (RFC 7946 right-hand rule).
  - Validity: `ST_IsValid(ST_GeomFromGeoJSON(...))` runs inside the write transaction. False (self-intersection, spike, duplicate ring) → 400 `validation_failed`.
  - The apierr body has no reason field, so the reason is not returned. Validity is planar on lng/lat, which is accurate for depot-scale zones. `ST_IsValid` is geometry-only, so the check casts.

- **GF-265 — Field bounds and schedule shape.**
  - `name`: trimmed, 1 to 100 runes, unique per fleet case-insensitively (`geofences_fleet_name_uq`); duplicate → 409 `conflict`.
  - `radius_m`: finite number, 1 to 100000 m; required for circle, forbidden for polygon (400 otherwise).
  - `dwell_min`: integer 1 to 1440, or absent (absent means no dwell rule).
  - `schedule`: absent or null means always active. Otherwise an object with exactly the keys `days`, `from`, `to`, `tz`.
    - `days`: non-empty, distinct integers 1 to 7 (ISO, Monday = 1); stored sorted.
    - `from` and `to`: `"HH:MM"` 24-hour, matching `^([01]\d|2[0-3]):[0-5]\d$`. `from == to` is rejected; `from > to` means the window crosses midnight and is allowed.
    - `tz`: IANA name accepted by Go `time.LoadLocation`, at most 64 characters.
  - Stored as canonical jsonb. Enter/exit/dwell semantics are P18's; P17 validates shape only.

- **GF-266 — `geofence_events` table and append-only trigger are created in P17 (scope reconciliation).** Roadmap §3 P17 scope lists "geofence_events table (append-only trigger)". R-7 (roadmap line 15) and tech-stack §11 name P18 as the creating phase. Decision: P17 creates the table and trigger, because the phase scope line is the binding text for P17 and P18 then only adds the writer. P17 writes zero rows; the only inserts are test fixtures. The trigger copies the P10 shape. The timing change is recorded as one SPEC-AMEND row (SPEC-AMEND repeat).

- **GF-267 — DELETE is a hard delete, and events are restricted.** `geofence_events.geofence_id` is `ON DELETE RESTRICT`. Deleting a fence with events fails with SQLSTATE 23503, which maps to 409 `conflict`. Reason: append-only events must never be cascade-deleted. P18 inherits this and must not add soft delete without a new decision.

- **GF-268 — Role matrix (R-6), following README §8.** The README §8 Geofences row is Admin ✅, Manager read, Dispatcher ✅, Driver —.
  - `GET /geofences`: admin, manager, dispatcher → 200; driver → 403; anonymous → 401.
  - `POST /geofences`: admin, dispatcher → 201; manager, driver → 403; anonymous → 401.
  - `DELETE /geofences/:id`: admin, dispatcher → 204; manager, driver → 403; anonymous → 401.
  - Enforced by `RequireRole` groups in `internal/geofence/http.go`, with matching rows in `rolematrix_test.go`.
  - **This overrides the brief's "POST/DELETE admin-only".** README §8 is authoritative (mission §2 line 21), and README §2.1.6 has dispatchers drawing fences. If admin-only is wanted instead, change the two rows and the role group, and add a SPEC-AMEND row because the matrix would then deviate from README §8.

- **GF-269 — Fleet scoping.** All verbs take `fleet_id` from the JWT claim. Another fleet's rows are invisible. An id from another fleet or a malformed id → 404 `not_found`, as in the registry.

- **GF-270 — bbox index assertion.** The GIST index `geofences_bbox_gix` is on `bbox`. `TestGeofenceBboxIndexUsed` copies the `internal/mapview/store_test.go` pattern: `SET LOCAL enable_seqscan = off`, substitute literals, run `EXPLAIN` on `SELECT id FROM geofences WHERE bbox && ST_MakeEnvelope(minLng,minLat,maxLng,maxLat,4326)`, and assert the plan contains `geofences_bbox_gix`. P17 runs no prefilter in production code; the test proves the index P18 will use.

- **GF-271 — No draw library is added.** Research: `@mapbox/mapbox-gl-draw` is reported unmaintained and hard to use with MapLibre (FOSS4G 2025 talk); `maplibre-gl-draw-circle` 0.1.1 is an unverified mapbox-gl-draw fork; Terra Draw's current version and maintenance were not verified. P17 needs two shapes: click-placed polygon vertices and centre plus radius. Both are built with MapLibre click handlers and GeoJSON sources, the P9/P13 pattern. `maplibre-gl` stays at 5.24.0 (features/09 pin). Revisit only if vertex editing is required, which P17 does not need.

- **GF-272 — Draw-to-GeoJSON lives in a pure module.** `apps/web/src/geofences/draw.ts`.
  - Polygon: clicks add vertices; close by clicking the first vertex, or Finish with at least 3 vertices; Escape cancels. Output is a closed ring in `[lng,lat]` order. The client does not normalize orientation; the server does.
  - Circle: click the centre, then radius from a number input (1 to 100000 m). Output is a Point with `properties.radius_m`.
  - `circleRing(center, r, 64)` is display-only.
  - Keeping this pure makes the conversion testable without WebGL (GF-167).

- **GF-273 — UI route and placement.** The route is `/zones`, not `/geofences`: Vite dev proxy prefix-matches full page loads to the API, which is why P16 used in-app links for `/jobs` and `/map`. Page: `geofences/ZonesPage.tsx`. `MapView` gains optional `fences` and `draft` sources; absent props leave the map unchanged, so the P16 `.new-job-map .map-canvas` path is unaffected. Draw and save controls are shown to admin and dispatcher. Manager sees the list and read-only fences. The proxy entry for `/geofences` is XHR only.

- **GF-274 — Stored fences render read-only (D-38).** Polygons render as fill plus line. Circles render the centre as a circle-layer point and the ring as a line from `circleRing`. No DOM markers.

- **GF-275 — Test placement.** Go integration tests sit in `internal/geofence/*_test.go` using `testinfra.NewMigratedDB` (D-37). Role-matrix rows sit in `services/api/rolematrix_test.go`. No build tags, matching the registry and jobs tests.

- **GF-276 — No new configuration.** No env vars, ports, compose or PORTS changes. The `CLAUDE.md` block documents the endpoints only.

- **GF-277 — P18 boundary.** P17 stores `bbox` and `dwell_min` and creates the index. It runs no prefilter, no `ST_Covers` or `ST_DWithin` in production code, no debounce, no event writes, and no alerts. Validation (g) checks this by grep.

## Validation

Runnable on this host. Each check has a pass condition.

- (a) `make fmt-check`, `make lint`, `make test` each exit 0. `go test ./... -race -count=1` output includes `ok … internal/geofence`.
- (b) `npm --prefix apps/web run typecheck`, `npm --prefix apps/web test` and `npm --prefix apps/web run build` each exit 0.
- (c) `apps/driver-pwa` typecheck, test and build each exit 0 (baseline check; no change).
- (d) Scope fence: `git diff --name-only main...HEAD` is a subset of:
  - `db/migrations/010_*`;
  - `internal/geofence/**`;
  - `services/api/server.go` and `services/api/*_test.go`;
  - `apps/web/src/geofences/**`, `apps/web/src/map/MapView.tsx`, `apps/web/src/App.tsx`, `apps/web/vite.config.ts`;
  - `specs/features/17-geofence-registry-drawing.md`;
  - `db/roundtrip_test.go` (amended at validation, 2026-10-28: the version constants 9→10 and the comment only — the round-trip test must track the newest migration, per this spec's G1 requirement that the existing round-trip test pass with 010);
  - `prompts/` (new files only);
  - `CLAUDE.md`;
  - `specs/constitution/mission.md` (one §7 row).
  - It must not touch `README.md`, `deploy/`, `e2e/`, `apps/driver-pwa/`, `tools/`, other `internal/` packages, or any other `specs/constitution/` file.
- (e) EXPLAIN test exists and passes: `grep -n 'geofences_bbox_gix' internal/geofence/store_test.go` has at least one match, and `go test ./internal/geofence -run TestGeofenceBboxIndexUsed -count=1` passes.
- (f) No secrets: `git diff main...HEAD | grep -iE 'secret|password|private key|api[_-]?key'` hits are only test fixtures or spec prose, checked by hand.
- (g) P18 boundary: `grep -rnE 'ST_Covers|ST_DWithin|INSERT INTO geofence_events' internal/geofence apps/web/src/geofences | grep -v _test.go` returns zero matches.
- (h) `wc -l CLAUDE.md` is under 200.
- (i) P16 e2e untouched: `git diff --name-only main...HEAD -- e2e/` is empty.
- (j) `010` pair exists and is clean: `ls db/migrations | tail -2` shows the `010_` up and down files; `grep -ciE 'BEGIN|COMMIT|CONCURRENTLY' db/migrations/010_*` returns 0.
- (k) Manual draw check in a browser against the dev stack: NOT RUN unless a browser and compose are available on this host. Record the reason if not run.

## Testing depth

| Layer | Test | Location | Source |
|---|---|---|---|
| Integration (Go, PostGIS) | GeoJSON round-trip, POST → GET identical, polygon and circle | `internal/geofence/http_test.go` | roadmap P17 |
| Integration | Invalid and self-intersecting polygon rejected with 400; nothing stored | `internal/geofence/http_test.go` | roadmap P17 |
| Integration | Schedule, dwell, radius and name rejections; overnight schedule accepted | `internal/geofence/http_test.go` | roadmap P17, GF-265 |
| Integration | `EXPLAIN` shows `geofences_bbox_gix` used | `internal/geofence/store_test.go` | roadmap P17, GF-270 |
| Integration | `geofence_events` UPDATE, DELETE and TRUNCATE blocked | `internal/geofence/store_test.go` | roadmap P17, R-7 |
| Integration | Role matrix: manager GET ok, POST rejected; anonymous 401 | `services/api/rolematrix_test.go` | roadmap P17, R-6 |
| Unit | Circle bbox containment | `internal/geofence/bbox_test.go` | D-33, GF-262 |
| Vitest | Draw-to-GeoJSON, polygon path and circle path | `apps/web/src/geofences/draw.test.ts` | roadmap P17 |
| Vitest | Role-based draw controls | `apps/web/src/geofences/ZonesPage.test.tsx` | README §8 |
| Existing gates | Go suite; `apps/web` typecheck, test, build; driver-pwa untouched | make targets | roadmap P17 |

## SPEC-AMEND repeat

- **One new row in mission §7**, added at P17 plan time and flagged for review like the other auto-approved items:

  `| D-33 (trigger timing) | geofence_events append-only trigger created in P17 (roadmap P17 scope) instead of P18 (R-7; tech-stack §11); P17 writes no rows | §2.1.6, §5; R-7 | SPEC-AMEND deviation | P17 |`

  Reason: R-7 is a constitution statement that P17 changes, so the change needs a ledger entry (GF-266).
- **No new row for:**
  - the `geofences` base table (README §5 owns it; D-26 and D-33 already cover the columns);
  - the circle representation, bbox and GeoJSON format (inside the D-33 model);
  - the draw UI (README §2.1.6 and D-38 already cover it);
  - the role matrix, which follows README §8 with no deviation (GF-268).
- If the role decision is reversed to admin-only, a SPEC-AMEND row is required, because the matrix would then deviate from README §8.
- GF-192 (hub identity binding) remains open and belongs to P23.

## Out of scope

- Geofence evaluator, bbox prefilter in production code, debounce, and enter/exit/dwell event emission (P18).
- Alerts (P19), ETA and OSRM (P20), reports and CSV (P21), k6 (P23).
- Push notifications (GF-1).
- Vertex editing of existing fences, fence history, soft delete, or a per-fleet fence cap.
- README edits, compose or PORTS changes, new env vars.
- Any `e2e/` change; the P16 happy path stays green and untouched.
- Any `apps/driver-pwa` change.
- Behaviour changes beyond geofences.
- GF-192 hub identity binding (P23).
- Merge to main, push, or any P18+ content.

## Validation results

Run on this host from the repo root. Exit codes are from the final run after G4.

| Check | Command | Result |
|---|---|---|
| (a) | `make fmt-check` | exit 0 |
| (a) | `make lint` | exit 0, 0 issues |
| (a) | `make test` | **exit 2**. Every package passes, including `ok fleetops/internal/geofence` and `ok fleetops/services/api` (role matrix rows for GET, POST and DELETE `/geofences`). The one failure is `TestMigrationRoundTrip` in `db/roundtrip_test.go:41` and `:59`: `version: want 9, got 10`. See D1. |
| (b) | `npm --prefix apps/web run typecheck`, `test`, `build` | exit 0, 0, 0. Vitest: 83 tests pass (68 existing, 15 new). |
| (c) | `apps/driver-pwa` typecheck, test, build | exit 0, 0, 0 (129 tests). The app is not changed. |
| (d) | `git diff --name-only main...HEAD` plus uncommitted files | 26 files, all inside the fence. No match outside it. |
| (e) | `grep -n geofences_bbox_gix internal/geofence/store_test.go`; `go test ./internal/geofence -run TestGeofenceBboxIndexUsed -count=1` | Matches at lines 87, 125 and 126. Test exit 0. |
| (f) | `git diff main...HEAD \| grep -iE 'secret\|password\|private key\|api[_-]?key'` | Hits are test fixtures only: `testSecret` (32 bytes) in `http_test.go`, and the `'x'` password hash in fixture SQL. Also spec prose. Checked by hand. |
| (g) | `grep -rnE 'ST_Covers\|ST_DWithin\|INSERT INTO geofence_events' internal/geofence apps/web/src/geofences \| grep -v _test.go` | No matches. |
| (h) | `wc -l CLAUDE.md` | 178 (under 200). |
| (i) | `git diff --name-only main...HEAD -- e2e/` | Empty. |
| (j) | `ls db/migrations \| tail -2`; `grep -ciE 'BEGIN\|COMMIT\|CONCURRENTLY' db/migrations/010_*` | The pair is present. The grep returns **1**, not 0. The single match is the `BEGIN` of the PL/pgSQL body of `geofence_events_append_only()` (line 36, inside `$$`). Migration 003 has the same construct, and 009 has three. The repo's own transaction-safety test skips dollar-quoted bodies and passes (`go test ./db -run TestMigrationFiles`). See D2. |
| (k) | Manual browser draw check against the dev stack | **NOT RUN.** No browser or interactive dev session is available in this run. Draw logic is covered by `draw.test.ts`. MapLibre rendering cannot run under jsdom (GF-167). |

## Deviations

- **D1: `db/roundtrip_test.go` expected version 9 — RESOLVED.** The spec requires the existing round-trip test to pass with 010, but the test hard-coded `assertVersion(t, m, 9)` at lines 41 and 59. The fence was amended to include the file and the two constants (plus the comment) were changed to 10 at validation time on 2026-10-28 by the pipeline driver. `make test` is expected green after this change.
- **D2: check (j) grep returns 1.** The match is the PL/pgSQL `BEGIN`, not transaction control. The spec's intent (no transaction control in migrations, GF-5) holds, and the repo's own check agrees.
- **D3: circle bbox formula changed (GF-262).** The spec's `dLat = r/111320 × 1.001` does not contain the circle. On a sphere, 111320 m per degree is larger than the true 111195 m, so 100 km is about 11 m short. On WGS84 the equatorial meridian degree is 110574 m, so the miss is about 560 m. `internal/geofence/bbox.go` uses 110574 m per degree with a 1.01 margin. It also uses the cosine at the poleward edge and widens longitude to ±180 when the circle reaches near a pole or crosses the antimeridian. `TestCircleBboxCovers` checks containment at 45° steps, for three Earth radii, at 0°, 52.5°, 70° and the antimeridian.
- **D4: no nav link to `/zones` yet.** The spec asks for a nav link in `App.tsx`, but `App.tsx` renders no header, and the existing links live in `JobsPage.tsx` and `MapPage.tsx`, which are outside the fence. The route `/zones` exists and is guarded. The page links back to `/map` and `/jobs`. A link from those pages needs a fence amendment.
- **D5: the G0 preflight `internal/hub` failure.** One full run failed `TestHubRelaysNATSJobAndAlert`. An isolated re-run passed. Later full runs passed. This is the known flake; no hub code was touched.
- Role matrix (GF-268), the `geofence_events` trigger timing (SPEC-AMEND row in mission §7), and the `geofences` base table (GF-260) are implemented as specified.
