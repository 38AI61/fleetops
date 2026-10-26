# Feature 08 — Map read API & GPS simulator (P8)

Status: approved plan, persisted 2026-10-26. Branch `phase-8-map-api-simulator`.
Source plan: `/root/.claude/plans/p8-plan-cozy-sphinx.md` (approved, self-gate amendments applied). Binding constitution: `specs/constitution/roadmap.md` §1 (R-1…R-9), §3 P8, `specs/constitution/tech-stack.md` §3, §4, §5, §6, §7.1, §9, §11 (D-05, D-12, D-27, D-40, GF-5, GF-8, GF-12, GF-13), `specs/constitution/mission.md` §7 (SPEC-AMEND rows), `specs/features/07-fanout-ws.md` (P7 decisions GF-100…GF-125 your code builds on), `AGENTS.md`, `CLAUDE.md`.

This spec is written before any P8 code (R-1, SDD). Implementation starts at Plan task group G0.

Ready to persist verbatim as `specs/features/08-map-api-simulator.md`. Decisions GF-126 onward are new in P8. Nothing here edits README, PORTS.md, compose, or the constitution. (GF-128 and GF-134 were amended 2026-10-26 at the implement self-gate: the bbox predicate is planar over a new expression index, which is migration 008 — see those decisions.)

## Scope

P8 delivers three things and nothing else:

1. `GET /map/vehicles?bbox=` — the caller's fleet's latest vehicle positions inside a bounding box, with the D-27 display status. JWT required. Row-scoped by the token's `fleet_id`.
2. `tools/seed --demo-devices` — on first seed only, creates demo vehicles and tracker devices in the seeded fleet, pairs them, generates each vehicle's waypoint loop (seeded waypoints for the simulator), and writes `deploy/.local/devices.json` (gitignored).
3. `tools/simulator --fleet=N --rate=D` — drives straight-line legs between the seeded waypoints from `devices.json` and POSTs `/ingest/position` at a sustained per-vehicle rate. No OSRM, no DB access.

Not in P8: the UI (P9), jobs table or job endpoints (P10), any WS change, follow-vehicle, MinIO, OSRM, load tests (P23).

## Plan

Build order. Each group lists its tests; test names are binding.

**G0 — Preflight (no code).**
- Baseline `make fmt-check lint test` green on `phase-8-map-api-simulator`. Record the result.
- Confirm (already verified in research): no `CREATE TABLE jobs` in migrations; migrations stop at 007; `.gitignore:23` ignores `deploy/.local/`; devicekey format is `fo_dev_<id>_<43-char base64url>`.
- Confirm by reading before G2 (signatures not yet verified): `registry.Service.CreateVehicle` input struct, `SetPairing` signature, `IssuedDevice` key field name, `services/api/main.go` calls `config.Load`, the existing §8 role-matrix test location, the apierr code used for unexpected 500s, whether ingest requires a paired vehicle to write `vehicles_latest` (via `vehicles.paired_device_id`), and `wc -l CLAUDE.md`.
- If any confirmation contradicts a decision below, stop and amend this spec before coding.

**G1 — Map read API (`internal/mapview`, wired in `services/api`).**
- Files: `bbox.go` (grammar + validation), `status.go` (`DeriveStatus`, `JobState`, `JobLookup` seam, `noJobLookup`), `store.go` (SQL), `http.go` (`Handler`, `Register(e, authn)`, `Get`).
- Wiring: `services/api/server.go` mounts `mapview.Handler` via `Register(e, auth.RequireAuth(d.Issuer))`, same as `registry.Handler`. `Deps` gains `PresenceTTL time.Duration`; `services/api/main.go` passes `cfg.PresenceTTL`. Role-matrix row added (GF-126).
- Tests:
  - `TestParseBBox` (table: valid, missing, duplicated, empty, 3 or 5 parts, whitespace, non-numeric, `NaN`, `Inf`, out of range, zero-area, inverted/antimeridian, leading/trailing comma).
  - `TestParseBBoxProperty` (rapid: random valid boxes parse to the same four numbers; random mutations are rejected).
  - `TestDeriveStatusOfflineEdge` (fake clock; subtests `fresh`, `exactly_120s_not_offline`, `120s_plus_1ns_offline`, `negative_skew_not_offline`).
  - `TestDeriveStatusPrecedence` (offline beats `en_route`; `en_route`/`picked_up` → `en_route`; other → `idle`; TTL 30s and 600s bounds respected).
  - `TestBBoxQueryFixture` (integration, PostGIS testcontainer: in-box, out-of-box, other fleet in-box, and empty result; vehicles kept ≥100 m off box edges, see GF-128).
  - `TestBBoxIndexScan` (integration: `EXPLAIN` with `enable_seqscan=off` contains `vehicles_latest_geom_planar_gix`).
  - `TestMapVehiclesAuth` (no token → 401; device key in `X-Device-Key` with no JWT → 401; each of admin, manager, dispatcher, driver → 200).
  - `TestMapVehiclesResponseShape` (field names, types, RFC3339 UTC ms, `truncated`, empty array not null).
  - `TestMapVehiclesTruncation` (1001 rows → 1000 returned, `truncated=true`).
  - `TestMapVehiclesReturnsOnlyInBboxOfCallerFleet` (services/api harness, real HTTP; **roadmap Validation item**).

**G2 — Seed extension (`internal/seed`, `internal/devicesfile`, `tools/seed`).**
- `internal/devicesfile`: typed `File` struct, `Write(path, File)` (temp file in same dir, mode 0600, fsync, rename), `Read(path) (File, error)` (validates version, non-empty, unique ids, key parses via `devicekey.Parse`, key id equals `device_id`). Shared by seed (writer) and simulator (reader).
- `internal/seed/demo.go`: `RunDemo` called from `Run` only in the branch where `Created == true` (GF-135). Uses `registry.Service{DB, Salt, Clock}` with `Actor{UserID: result.UserID, FleetID}`.
- `tools/seed/main.go`: adds `--demo-devices` (bool). Reads `DEVICE_KEY_SALT` only when the flag is set.
- Tests:
  - `TestDevicesFileRoundTripAndShape` (write → read equal; JSON shape per GF-137 incl. per-device `waypoints` from GF-142).
  - `TestDevicesFilePerms` (file 0600, dir 0700 when created).
  - `TestDevicesFileRejects` (table: bad version, empty, duplicate id, malformed key, key/id mismatch, missing waypoints).
  - `TestDemoDevicesCreatesFiftyPairedDevices` (50 vehicles `DEMO-001..DEMO-050`, 50 trackers, 1:1 pairing, file has 50 entries).
  - `TestDemoDevicesRerunIdempotent` (second run: `Created=false`, no new rows, file bytes unchanged, exit 0).
  - `TestDemoKeysAuthenticate` (every key in the file passes `registry.AuthenticateDeviceKey`).
  - `TestDemoRequiresSalt` (missing or <32-byte salt with the flag → usage error, zero DB writes).
  - `TestDemoAuditRows` (audit rows exist for issue and pair under the seeded admin).

**G3 — Simulator core (`internal/simulator`).**
- Files: `route.go` (waypoint generation, leg model, `PositionAt`), `schedule.go` (pure `Scheduler`), `client.go` (`Sender` interface, HTTP implementation), `run.go` (`Run(ctx, Config)`, summary).
- Tests:
  - `TestPropInterpolatedPointsStayOnLeg` (rapid).
  - `TestPropLegSpeedAndHeadingWithinBounds` (rapid).
  - `TestPropEmitIntervalRespected` (rapid).
  - `TestScheduleDropsMissedSlots` (unit: a late tick emits once, counts skipped, never bursts).
  - `TestRouteDeterministic` (same device id → same route).
  - `TestSendClassification` (`httptest` server returns 204, 400, 401, 503, closed connection → counters and no panic).

**G4 — Simulator CLI (`tools/simulator/main.go`).**
- Thin main: flags, signal context (SIGINT/SIGTERM), exit codes per GF-141/GF-147. No logic beyond wiring.
- Tests:
  - `TestSimulatorFlagBounds` (table: rate 499ms and 61s, fleet 0, fleet greater than devices, bad api URL → exit 2).
  - `TestSimulatorDevicesMissing` (exit 1 with a message pointing to `--demo-devices`).

**G5 — End-to-end (`services/api/simulator_e2e_test.go`).**
- Real harness (`startServer`), real registry issuance, real `Run` with an HTTP sender.
- Tests:
  - `TestSimulatorSustainsConfiguredRate` (**roadmap Validation item**): 4 vehicles, rate 250ms, window 3s; GF-144/GF-146 measurable criteria.
  - `TestSimulatorSurvivesRejectedKey` (one device revoked mid-run: its POSTs are counted as rejected, the others keep accepting, the run does not exit).

**G6 — Docs and validation.**
- `CLAUDE.md`: mechanical rows only (GF-151).
- Run the full validation list. No README, PORTS.md, compose, constitution, or `db/` edits.

## Requirements

### Binding, carried verbatim

- **D-05** — "First fleet: `make seed` runs `tools/seed`, which creates one fleet and one admin only when `fleets` is empty; password read from a no-echo TTY prompt or stdin; no default credentials; `--timezone` (default UTC)."
- **D-12** — "Simulator stays in Phase 8 (§10). Reads device keys from `deploy/.local/devices.json` (gitignored), written by `tools/seed --demo-devices`. Straight-line legs, no OSRM dependency."
- **D-27** — "`vehicles.status` = registry status `{active, maintenance, retired}`. Map display status `{idle, en_route, offline}` is derived server-side: `offline` if `server_ts` older than 120 s; `en_route` if the vehicle's job is `en_route` or `picked_up`; otherwise `idle`."
- **D-40** — "Spec process: every **SPEC-AMEND** item is listed in mission.md §7 and in the tech-stack decision log; each feature spec repeats the amendments it needs. Changes to env names update CLAUDE.md in the same branch."
- **GF-5** — Migrations: sequential `NNN_snake_name.up.sql` / `.down.sql` in `db/migrations/`, from `001`, no timestamps, no `BEGIN`/`COMMIT`, no `CREATE INDEX CONCURRENTLY`, forward-only once merged.
- **GF-8** — Testcontainers helper at `internal/testinfra/`; `tools/` holds `cmd` binaries only (D-39).
- **GF-12** — Test and CI PostGIS image `postgis/postgis:16-3.4` (`testinfra.PostgisImage`).
- **GF-13** — Testcontainers use OS-assigned ephemeral host ports.
- **mission §7** — No new SPEC-AMEND row is added by P8 (see SPEC-AMEND repeat). Note: the phase brief also cited GF-85, which is a P5 spec-local decision (REAL storage floor, `specs/features/05-batch-writer.md`), not a tech-stack §11 entry; P8 does not depend on it and it is not carried as binding (see SPEC-AMEND repeat).

### New decisions (GF-126 onward)

**GF-126 — Map endpoint auth and role matrix.** `GET /map/vehicles` requires `auth.RequireAuth(d.Issuer)` with no `RequireRole`. Roles admin, manager, dispatcher, and driver all receive 200. README §8 has no per-role map restriction, and row scoping by `fleet_id` is the isolation boundary. A device key (`X-Device-Key`, no JWT) and no credential both receive 401 (GF-47, GF-52). The route is added to the §8 role-matrix test (R-6). *Why:* the map is a read of the caller's own fleet, so role-gating would add nothing; device keys must never read.

**GF-127 — Bbox grammar and rejection.** Query parameter `bbox`, exactly one occurrence, value `minLng,minLat,maxLng,maxLat` (GeoJSON and MapLibre order), four decimal numbers separated by commas, no whitespace. Parsed with `strconv.ParseFloat(_, 64)`. Each value must be finite. Constraints: `-180 ≤ minLng < maxLng ≤ 180` and `-90 ≤ minLat < maxLat ≤ 90`. Rejected with `400 validation_failed` (apierr): missing, duplicated, empty, wrong arity, non-numeric, `NaN`/`Inf`, out of range, zero-area (`minLng == maxLng` or `minLat == maxLat`), inverted (`minLng > maxLng`, which includes antimeridian-crossing boxes). Antimeridian-crossing boxes are not supported; the client splits them into two calls. *Why:* one strict grammar keeps the handler a pure function. Zero-area boxes never come from a real viewport. Errors never echo the value (matches ingest's `FieldError` style).

**GF-128 — SQL predicate and index.** *(Amended 2026-10-26 at implement self-gate: the original geography-typed predicate was proven wrong on this host against `postgis/postgis:16-3.4` — `ST_MakeEnvelope(-180,-90,180,90,4326)::geography` raises `ERROR: Antipodal (180 degrees long) edge detected!` (whole-world bbox → 500), and for a wide box `ST_MakeEnvelope(-90,30,90,70,4326)::geography` returns false for a point (13.4, 52.5) inside it (silent wrong answer). The amended predicate is planar, which is exact for point-vs-envelope and serves wide boxes correctly; it is index-driven through the new expression index from GF-134, verified via `EXPLAIN` showing an index scan on it.)* Single query:
```sql
SELECT vehicle_id, ST_Y(geom::geometry), ST_X(geom::geometry), speed_kmh, heading_deg, device_ts, server_ts
FROM vehicles_latest
WHERE fleet_id = $1
  AND geom::geometry && ST_MakeEnvelope($2, $3, $4, $5, 4326)
  AND ST_Covers(ST_MakeEnvelope($2, $3, $4, $5, 4326), geom::geometry)
ORDER BY vehicle_id
LIMIT $6
```
`$2..$5` are `minLng, minLat, maxLng, maxLat`; `$6` is the cap plus one (GF-129). The `&&` bound uses the `vehicles_latest_geom_planar_gix` expression GIST index (GF-134); `fleet_id = $1` uses `vehicles_latest_fleet_idx`; `ST_Covers` is the exact refinement. **Semantics:** planar rectangle containment in EPSG:4326 degrees. For point geometries this is unambiguous — a point is a degenerate rectangle, so the geodesic-edge sagitta problem of the geography type cannot arise. Boxes wider than 180° of longitude are still rejected by the GF-127 grammar (`minLng < maxLng`); a whole-world bbox (`-180,-90,180,90`) is valid and correct under this predicate. *Why:* correctness for every grammar-valid box beats geodesic edge fidelity at sub-metre scale; the fixture vehicles stay ≥100 m off edges anyway (now a belt-and-braces margin, not a semantic caveat).

**GF-129 — Result cap.** At most 1000 vehicles per response, ordered by `vehicle_id`. The query asks for 1001 rows; a 1001st row sets `truncated: true` and is dropped. *Why:* bounds response size for a whole-world bbox over a large fleet; P9 can tighten the bbox to see the rest.

**GF-130 — Response schema.** `200 OK`, `application/json`:
```json
{
  "vehicles": [
    {
      "vehicle_id": 1,
      "lat": 52.52,
      "lng": 13.405,
      "speed_kmh": 42.0,
      "heading_deg": 271.5,
      "status": "idle",
      "device_ts": "2026-10-26T10:00:00.000Z",
      "server_ts": "2026-10-26T10:00:00.120Z"
    }
  ],
  "truncated": false
}
```
- `vehicles` is always an array, never `null`.
- `speed_kmh` and `heading_deg` are `null` when the column is null.
- `status` is one of `idle`, `en_route`, `offline` (GF-131).
- Timestamps are RFC3339 UTC with millisecond precision and a `Z` suffix (Go layout `2006-01-02T15:04:05.000Z07:00` after `.UTC()`).
- No `fleet_id` in the body (the caller already knows it).
- Errors use the existing `{"error": "<code>"}` envelope from apierr.
- *Why:* field names mirror the DB columns, so no mapping layer. The envelope leaves room for `truncated` and later cursors. P9 map init consumes this shape directly.

**GF-131 — Display-status derivation.** Function:
```go
type JobState string // "" (none), "en_route", "picked_up", "other"
type DisplayStatus string // "idle", "en_route", "offline"

func DeriveStatus(serverTS, now time.Time, ttl time.Duration, job JobState) DisplayStatus
```
Rules, in D-27 order: `age := now.Sub(serverTS)`. If `age > ttl`, return `offline`. Else if `job` is `en_route` or `picked_up`, return `en_route`. Else return `idle`. A negative age (clock skew between DB and API) is fresh, not offline. Exactly `ttl` is not offline; `ttl + 1ns` is. *Why:* this is the literal D-27 rule with the boundary chosen to match "older than 120 s".

**GF-132 — Offline threshold source.** The threshold is `config.Config.PresenceTTL`, read from `FLEETOPS_PRESENCE_TTL` (default `120s`, range `30s`–`600s`, existing). It reaches the handler through `api.Deps.PresenceTTL`. There is no new env name and no hard-coded 120 constant in `mapview`. The Redis presence key (`device:<id>`, `EX` = same TTL) and the DB display-status offline edge are one knob. Presence is per device, offline is per vehicle, but both measure the same silence. The P9 UI gray-out reads `status`, so it follows the knob too. *Why:* D-27 and README §2.1.4 agree on 2 minutes by default; one config value keeps them agreeing when the operator changes it.

**GF-133 — en_route seam and P10 follow-up.** P8 defines `JobLookup`:
```go
type JobLookup interface {
    ActiveJobStates(ctx context.Context, fleetID int64, vehicleIDs []int64) (map[int64]JobState, error)
}
```
P8 wires `noJobLookup`, which returns an empty map. So every fresh vehicle derives `idle` and no job data is invented. `JobState` constants exist now because they are the D-27 vocabulary. **P10 follow-up:** implement `JobLookup` over the P10 jobs tables (§6.3 `job_transitions` and the jobs state) and wire it in `services/api`; the map handler does not change. Tests use a fake `JobLookup` to prove the seam (`TestMapVehiclesEnRouteFromLookup`). The seam is a test double of our own interface, not a mock of infrastructure. *Why:* honest about the missing jobs table; no fake job rows; the P10 change is one implementation.

**GF-134 — Migration 008: planar expression index on `vehicles_latest`.** *(Amended 2026-10-26 at implement self-gate: GF-128's corrected planar predicate needs an index over `(geom::geometry)`; the original text forbidding a migration reserved exactly this case — "a predicate change that GF-128's index test rejects with no working fallback" — and the geography form was proven to reject wide boxes with no working fallback. This is the legitimate reopening the original text anticipated.)* Migration `008_vehicles_latest_planar_gix.up.sql` adds:
```sql
CREATE INDEX vehicles_latest_geom_planar_gix ON vehicles_latest USING GIST ((geom::geometry));
```
with the matching `.down.sql` dropping it. One index, no `BEGIN`/`COMMIT`, no `CONCURRENTLY`, sequential number after 007 (GF-5). `008` exists for this index only; `vehicles_latest` columns are unchanged and no other table is touched. The only tables read remain `vehicles_latest`; no jobs table is read.

**GF-135 — Demo-devices gating (D-05).** `tools/seed --demo-devices` is a bool flag. It runs only in the first-seed branch, when `fleets` is empty and `Run` creates the fleet (`Created == true`). On an already-seeded DB it is a no-op: exit 0, a stderr note that the flag applies only on first seed, and `devices.json` is not rewritten. Keys are shown once and cannot be recovered, so a rerun must not rewrite them. Adding demo devices to an existing DB is not supported in P8; reset the DB. The flag requires `DEVICE_KEY_SALT` (≥32 bytes); missing or short → usage error, exit 2, before any DB write. *Why:* preserves D-05's idempotence exactly and keeps the file consistent with the DB.

**GF-136 — Demo fleet, vehicles, devices, pairing.** The demo fleet is the seeded first fleet. `DemoVehicleCount = 50` is a constant, not a flag. Vehicles are `DEMO-001` through `DEMO-050` (plate-style names), created through `registry.Service` vehicle creation. Each gets one tracker device issued through `registry.Service.IssueDevice(ctx, actor, KindTracker)`, paired 1:1 through `SetPairing`. The actor is the seeded admin (`Result.UserID`, `FleetID`), so audit rows (`device.issue`, `vehicle.pair`, and the vehicle create action) are written by the normal registry `s.write` path. No drivers are created. Seed does not write SQL for devices or keys itself, which keeps key hashing in one place. The seed package imports `internal/registry`; registry does not import seed, so there is no cycle (confirmed in G0). *Why:* reusing the registry path gives audit and key hashing for free. Vehicles start with no `vehicles_latest` rows; the first positions come from the simulator.

**GF-137 — devices.json schema.** One file, JSON, UTF-8:
```json
{
  "version": 1,
  "fleet_id": 1,
  "generated_at": "2026-10-26T10:00:00Z",
  "devices": [
    {"device_id": 2, "vehicle_id": 1, "kind": "tracker", "key": "fo_dev_2_<43-char>",
     "waypoints": [{"lat": 52.51, "lng": 13.34}, {"lat": 52.55, "lng": 13.42},
                   {"lat": 52.48, "lng": 13.47}, {"lat": 52.58, "lng": 13.31}]}
  ]
}
```
- `devices` is ordered by `device_id` ascending.
- `key` is the plaintext device key (`devicekey.Format`), the only copy.
- `waypoints` is the vehicle's seeded route loop (GF-142): exactly four `{lat, lng}` objects, lat in `[-90, 90]`, lng in `[-180, 180]`, consecutive waypoints distinct.
- The reader rejects: `version != 1`, empty `devices`, duplicate `device_id`, a key that fails `devicekey.Parse`, a key id that differs from `device_id`, and a missing or malformed `waypoints`.
- Package `internal/devicesfile` is the single definition for writer and reader.

*Why:* the key is needed by the simulator, and `fleet_id` is recorded for operators, not used by the simulator.

**GF-138 — devices.json path, perms, plaintext handling.** Default path `deploy/.local/devices.json`, relative to the current working directory. The Makefile runs from the repo root, so this resolves correctly. Seed creates `deploy/.local/` with mode 0700 if missing, and writes the file through a temp file in the same directory, mode 0600 (`O_EXCL`), fsync, then rename. The simulator has one path flag, `--devices` (default the same path); it exists so tests can use a temp dir and so other checkouts can point elsewhere. Plaintext keys are never printed to stdout or stderr, never logged, and never stored in the DB. `.gitignore:23` already ignores `deploy/.local/` (verified). *Why:* D-12 requires the file's location; the cwd default matches how `make` runs.

**GF-139 — Failure ordering.** DB writes (vehicles, devices, pairings) commit through the registry calls first, then the file is written. If the file write fails, seed exits 1 with the message that devices exist but the keys file was not written, and that the DB must be reset. P8 does not add cross-store atomicity. *Why:* the DB is the source of truth. Writing the file first would leave keys the DB never accepted.

**GF-140 — Simulator package layout.** `tools/simulator/main.go` is a thin command: flags, signals, exit codes, nothing else. Logic lives in `internal/simulator` (importable and testable). `internal/devicesfile` is shared with seed. *Why:* GF-8 and D-39 — `tools/` holds `cmd` binaries only.

**GF-141 — Simulator flags, defaults, bounds.**
- `--fleet` (int, default `50`, range `1` to `len(devices)`). The number of simulated vehicles, taken as the first N entries of devices.json. It is **not** a fleet id: the fleet is implied by the device keys, and ingest stamps `fleet_id` from the device. Exceeding the file count is a usage error (exit 2).
- `--rate` (duration, default `5s`, range `500ms` to `60s`). One position per vehicle per interval.
- `--api` (string, default `http://127.0.0.1:10010`, the reserved API port). Must parse as `http` or `https` with a host; a trailing slash is trimmed.
- `--devices` (string, default `deploy/.local/devices.json`, GF-138).
- No other flags. The simulator takes no env vars and connects to no DB, NATS, or Redis.
- Exit codes: 0 normal or signal-initiated shutdown; 1 runtime (devices file missing, unreadable, or invalid); 2 usage (flag parse, bounds, URL, fleet greater than devices).
- *Why:* the roadmap line `--fleet=50` reads as a vehicle count, and the seed already creates 50 demo vehicles. Keeping `fleet_id` out of the simulator removes a flag that could only disagree with the key file.

**GF-142 — Waypoint and route model.** Each vehicle gets a closed loop of four waypoints, generated at seed time by `RunDemo` with a `math/rand/v2` PCG seeded by the device id, written into the device's entry in `devices.json` as `waypoints` (four `{lat, lng}` objects, roadmap: "drives straight-line legs between seeded waypoints"), and read from there by the simulator. The simulator does not generate or invent waypoints; a devices file without them fails `devicesfile.Read` (GF-137's reader validates them). Waypoints are drawn uniformly inside a fixed box: lat `[52.45, 52.60]`, lng `[13.30, 13.50]`. The box is a placeholder area; there is no DB read and no OSRM. Route order is `w0→w1→w2→w3→w0`. Consecutive waypoints are distinct. *Why:* the roadmap pins seeded waypoints; deterministic per-device generation at seed time keeps tests reproducible and the simulator DB-free (D-12).

**GF-143 — Leg interpolation, speed, heading.**
- Each leg gets a speed `v` drawn uniformly from `[20, 90]` km/h, from the same PRNG.
- Leg duration = haversine length in metres ÷ `v/3.6`.
- Position at elapsed time `t` (since route epoch): take `t mod cycle` (cycle = sum of leg durations), find the leg via cumulative durations, fraction `f = elapsed/legDuration`, and interpolate linearly in lat and lng: `lat = lat0 + f·(lat1−lat0)`, `lng = lng0 + f·(lng1−lng0)`.
- `speed_kmh = v`, constant per leg, within `[20, 90]` ⊂ ingest `[0, 300]`.
- `heading_deg` = initial great-circle bearing from the leg start to end, normalized to `[0, 360)`.
- `accuracy = 5` (metres, within ingest `[0, 10000]` and under the default `FLEETOPS_ACCURACY_MAX_M` of 50).
- Realized speed between two positions at least 1 s apart on the same leg is within 1% of `v` (planar-in-degrees error at spans ≤ 20 km is under 0.5%).
- *Why:* linear lat/lng interpolation keeps points exactly collinear with their leg, which is the testable property. Speed is constant per leg so the bounds test is exact.

**GF-144 — Pacing mechanics and sustained rate.** For vehicle index `i` (0-based) of `N`, slot `k` is due at `due(i,k) = start + i·rate/N + k·rate`. The schedule is absolute: it does not sleep after each request, so request latency does not cause drift. Each slot's position and `device_ts` are computed at `due(i,k)`, so consecutive received `device_ts` values for one vehicle differ by exactly `rate` (within 1 ms from ms rounding). Phase offsets spread vehicles evenly across the interval, so no burst is synchronized. *Sustained rate* is defined as: over a window of length `W`, each vehicle's accepted-slot count is `floor(W/rate)` within ±2 slots, and the aggregate accepted count is at least 95% of `N·floor(W/rate)`.

**GF-145 — Catch-up policy.** `Scheduler.Due(now)` returns, per vehicle, at most one emit: the newest slot with `due ≤ now`. Older missed slots are dropped and counted as `skipped`. The next due is the first slot `> now`. A late process never bursts. *Why:* a burst after a stall would hit the ingest `busy` gate (503) and distort the rate that the test measures.

**GF-146 — HTTP failure policy and transport.** Each emit is `POST {api}/ingest/position` with headers `X-Device-Key: <key>` and `Content-Type: application/json`, body exactly `{lat, lng, speed, heading, accuracy, ts}` (the `PositionRequest` wire shape). Per-request timeout is 5 s; the client keeps idle connections per host at N. Responses:
- `204` → `accepted`.
- `4xx` → `rejected`.
- `5xx` or transport error → `failed`.

Each failure logs one line with the vehicle index, the status (or error class), and the response `error` code when present. Never the key. No retry: the next slot sends a fresh position. The simulator does not probe the API at startup; an unreachable API shows up as failures. Failures are never fatal. *Why:* the rate test depends on skipping failures rather than stopping, and a stale retry would re-send an old position.

**GF-147 — Shutdown.** SIGINT or SIGTERM cancels the context. The scheduler stops issuing new slots, in-flight requests finish within the 5 s client timeout, then one summary line goes to stderr: `sent`, `accepted`, `rejected`, `failed`, `skipped`. No final flush: ingest is asynchronous and the last accepted POST is the last durable state. Exit 0.

**GF-148 — Test seams.**
- `Scheduler` is a pure value driven by explicit `now` arguments. The property tests step it with synthetic times; no goroutines, no sleep. That is the clock seam, and it matches the repo's `func() time.Time` pattern from `auth.NewIssuer`/`NewThrottle`.
- `Sender` is an interface with one method, `Send(ctx, key, PositionBody) (Outcome, error)`. Property tests use a recording double of this interface. The e2e test uses the real HTTP sender against the real API harness.
- `Run(ctx, Config)` uses real time. Only the e2e test exercises it end to end.
- The recording double is a test double of our own interface, not a mock of infrastructure.

**GF-149 — Config additions: none.** `FLEETOPS_PRESENCE_TTL` already exists (GF-132). The simulator reads no env vars (GF-141). No new `FLEETOPS_*` tunable. CLAUDE.md's env table and tunables table are unchanged.

**GF-150 — Property test names and contents.** Names are binding (see Validation):
- `TestPropInterpolatedPointsStayOnLeg`: for random legs and `f ∈ [0,1]`, the point is within the leg's lat/lng bounding box and its cross product with the leg vector is ≤ 1e-9 degrees².
- `TestPropLegSpeedAndHeadingWithinBounds`: every `speed_kmh ∈ [20, 90]`, every `heading_deg ∈ [0, 360)`, and realized speed is within 1% of reported.
- `TestPropEmitIntervalRespected`: for random rate, N, and step sequences with random stalls, per-vehicle emits are at most one per `Due` call, consecutive emitted slot times differ by at least `rate` and by exactly `rate` when no skip occurred, and `accepted + rejected + failed + skipped` equals the number of due slots.

**GF-151 — CLAUDE.md rows (D-40, mechanical only).** Add:
- The seed line: `SEED_ARGS="--admin-email <email> --demo-devices" make seed`. The same line notes that the flag needs `DEVICE_KEY_SALT`.
- The simulator run line: `go run ./tools/simulator --fleet=50 --rate=5s --api http://127.0.0.1:10010`.
- A map endpoint bullet: `GET /map/vehicles?bbox=minLng,minLat,maxLng,maxLat`, JWT, any role, fleet-scoped.

No prose, no README, PORTS.md, compose, or constitution edits. `CLAUDE.md` stays under 200 lines. *Why:* D-40 requires the mechanical facts to live in CLAUDE.md on the same branch.

## Validation

Runnable on this host (Docker available for testcontainers; compose is not started).

1. `make fmt-check` exits 0.
2. `make lint` exits 0.
3. `make test` (`go test ./... -race -count=1`) exits 0.
4. `TestMapVehiclesReturnsOnlyInBboxOfCallerFleet` passes: the fixture includes in-box caller-fleet vehicles, an out-of-box caller-fleet vehicle, and an in-box other-fleet vehicle; the response contains exactly the in-box caller-fleet vehicles. **Roadmap Validation item.**
5. `TestSimulatorSustainsConfiguredRate` passes: 4 vehicles, rate 250 ms, window 3 s; per-vehicle accepted count within ±2 of `floor(3s/250ms) = 12`; aggregate ≥ 95%; no skipped slots. **Roadmap Validation item.**
6. `TestDeriveStatusOfflineEdge` passes, with subtests `fresh`, `exactly_120s_not_offline`, `120s_plus_1ns_offline`. **120 s edge test.**
7. `TestPropInterpolatedPointsStayOnLeg`, `TestPropLegSpeedAndHeadingWithinBounds`, `TestPropEmitIntervalRespected` pass (rapid, default check count).
8. `TestBBoxIndexScan` passes (index scan on `vehicles_latest_geom_planar_gix` in `EXPLAIN`).
9. `TestDemoDevicesRerunIdempotent` and `TestDemoKeysAuthenticate` pass.
10. Scope fence: `git diff --name-only main...HEAD` contains only paths under `internal/mapview/`, `internal/simulator/`, `internal/devicesfile/`, `internal/seed/`, `tools/seed/`, `tools/simulator/`, `services/api/`, `db/migrations/`, `CLAUDE.md`, and `specs/features/08-map-api-simulator.md`. It must not touch `README.md`, `deploy/docker-compose.yml`, `deploy/PORTS.md`, or `specs/constitution/`. (`prompts/` pointer files are committed as provenance per the ratified workflow and are not a fence violation.)
11. Migration 008 exists and is exactly the planar index: `ls db/migrations | grep -c '^008'` prints `2` (one `.up.sql`, one `.down.sql`); `git diff main...HEAD -- db/migrations/008_vehicles_latest_planar_gix.up.sql` shows only the `CREATE INDEX` from GF-134; a migration round-trip (`008` down then up, or the round-trip test) passes.
12. No go.mod change: `git diff main...HEAD -- go.mod go.sum` is empty.
13. No secret committed: `git grep -nE 'fo_dev_[0-9]+_[A-Za-z0-9_-]{43}'` prints nothing; `git ls-files deploy/.local` prints nothing; `git check-ignore -v deploy/.local/devices.json` names `.gitignore:23`.
14. CLAUDE.md scope: `wc -l CLAUDE.md` < 200; `git diff --numstat main...HEAD -- CLAUDE.md` shows zero deletions.

## Testing depth

| Layer | Depth | Notes |
|---|---|---|
| Go unit | stdlib `testing`; table tests for bbox, status, flags, file validation | no mocks of infrastructure |
| Property (rapid) | bbox parse, leg geometry and bounds, emit interval | `rapid.Check`, labeled `Draw`, helpers take `*rapid.T` (repo idiom) |
| Integration (PostGIS testcontainers) | bbox query fixture, `EXPLAIN` index test, `vehicles_latest` writes, demo seed | real Postgres, no mocks; `testinfra.NewMigratedDB` |
| Integration (services/api harness) | HTTP auth matrix, response shape, cross-fleet leak, truncation, e2e simulator | real loopback listener, real JWT, real device keys |
| Race | `make test` runs `-race` on everything | |
| Role matrix | `/map/vehicles` added to existing §8 test | R-6 |
| Frontend, E2E, load | not applicable in P8 | P9 and P23 |

The 120 s edge uses a fake clock (`DeriveStatus` takes `now` as an argument); every other test uses real PostGIS.

## SPEC-AMEND repeat

No new SPEC-AMEND in P8. mission §7 is unchanged. Repeated as binding: GF-12 (PostGIS image tag) and D-40 (CLAUDE.md update in the same branch). GF-85 is a P5 spec-local decision (REAL storage floor in `specs/features/05-batch-writer.md`), not a tech-stack §11 entry; P8 does not depend on it and no amendment is recorded for it.

## Out of scope

- The dispatcher or driver UI, MapLibre, clustering, follow-vehicle (P9).
- The jobs table, job endpoints, job state, and the real `JobLookup` (P10).
- Any change to WS hub, fanout, or presence behavior (P7 is merged).
- MinIO, OSRM, and any route-service call.
- Load or k6 tests (P23).
- Demo drivers, demo alerts, and seeded positions history.
- Adding demo devices to an existing DB (GF-135).
- Antimeridian-crossing bbox support (GF-127).
- Any README, PORTS.md, compose, or constitution edit. (Migration 008 is in scope per GF-134 as amended.)
