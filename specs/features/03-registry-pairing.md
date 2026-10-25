# Feature 03 — Registry & pairing (P3)

Status: approved plan, persisted 2026-10-25. Branch `phase-3-registry-pairing`.
Source plan: `/root/.claude/plans/p3-plan-resilient-horizon.md` (approved). Binding constitution: `specs/constitution/roadmap.md` §1 (R-1…R-9) and §3 P3, `specs/constitution/tech-stack.md` §3, §6, §7, §9, §11 (D-27, D-28, D-29, D-30, D-31, GF-2, GF-3, GF-5, GF-17), `specs/constitution/mission.md` §7 (SPEC-AMEND rows), `AGENTS.md`, `CLAUDE.md`.

This spec is written before any P3 code (R-1, SDD). Implementation starts at Plan task group 1.

## Scope

Roadmap P3 (`specs/constitution/roadmap.md` §3), verbatim:

> **P3 — Fleet registry & pairing** · D-27–D-29, D-31, D-28, GF-2, GF-3 · README §2.1.1, §8
> - Scope: vehicles (registry status `{active, maintenance, retired}`), drivers (license hash), devices (`kind` pwa|tracker; key issue, rotate, revoke), `vehicle_pairings` history, `vehicles.paired_device_id` mirror; CRUD per §8 role matrix; `/vehicles/:id/pairings`, device key endpoints (SPEC-AMEND).
> - Validation: each role × resource cell of §8 verified; pairing swaps keep history; revoked device is refused at ingest (verified in P4).
> - Testing: integration role-matrix table test (4 roles × every resource × verbs) + property test: random pair/unpair/rotate/revoke sequences never leave two current pairings on one device or one vehicle, and `vehicles.paired_device_id` always mirrors the current pairing.

P3 delivers: migrations `005`–`006` (`vehicles`, `drivers`, `devices` with `revoked_at`, `vehicle_pairings`, the `users.driver_id` foreign key, and the `vehicles.paired_device_id` foreign key); config for `DEVICE_KEY_SALT`; `internal/devicekey` (key format, HMAC, verify); `internal/registry` (license normalization and hash, stores, pairing service, device check functions, HTTP handlers); CRUD for `/vehicles` and `/drivers`; `GET/POST /vehicles/:id/pairings`; `POST /devices`, `POST /devices/:id/rotate`, `POST /devices/:id/revoke`; a role-matrix extension (R-6); a fleet-scoping extension; and the pairing-invariant property test (`pgregory.net/rapid`, GF-17 closed).

Nothing from P4+ is built in P3: no ingest endpoint, no device-key HTTP authentication, no NATS, no positions, no map, no WS hub, no jobs, no geofences, no alerts. P3 adds **no append-only table** and therefore no trigger (R-7).

## Plan

Task groups in build order. Each group ends in a commit on `phase-3-registry-pairing`. Tests live beside code. The `CLAUDE.md` update (group 14) stays in implement scope.

### 1. Migrations `005`–`006`

- **`db/migrations/005_fleet_registry.up.sql`**
  - `vehicles(id BIGSERIAL PK, fleet_id BIGINT NOT NULL REFERENCES fleets(id), plate TEXT NOT NULL, model TEXT NULL, status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','maintenance','retired')), paired_device_id BIGINT NULL)`; `UNIQUE (fleet_id, plate)`; index `(fleet_id)`.
  - `drivers(id BIGSERIAL PK, fleet_id BIGINT NOT NULL REFERENCES fleets(id), name TEXT NOT NULL, license_no_hash BYTEA NOT NULL, phone TEXT NULL, shift_start TIME NULL, shift_end TIME NULL)`; `UNIQUE (fleet_id, license_no_hash)`; index `(fleet_id)`.
  - `devices(id BIGSERIAL PK, fleet_id BIGINT NOT NULL REFERENCES fleets(id), kind TEXT NOT NULL CHECK (kind IN ('pwa','tracker')), api_key_hash BYTEA NOT NULL, last_seen_at TIMESTAMPTZ NULL, revoked_at TIMESTAMPTZ NULL)`; index `(fleet_id)`.
  - `ALTER TABLE users ADD CONSTRAINT users_driver_id_fkey FOREIGN KEY (driver_id) REFERENCES drivers(id);` (P2 GF-27 deferred this FK to P3; `driver_id` is always NULL before this migration).
  - No `created_at` or other extra columns: the column list matches tech-stack §6.1 and §6.3 exactly. No BEGIN/COMMIT.
  - **`005_fleet_registry.down.sql`** — drop the `users` constraint, then `devices`, `drivers`, `vehicles`.
- **`db/migrations/006_vehicle_pairings.up.sql`**
  - `vehicle_pairings(id BIGSERIAL PK, fleet_id BIGINT NOT NULL REFERENCES fleets(id), vehicle_id BIGINT NOT NULL REFERENCES vehicles(id), driver_id BIGINT NULL REFERENCES drivers(id), device_id BIGINT NOT NULL REFERENCES devices(id), started_at TIMESTAMPTZ NOT NULL DEFAULT now(), ended_at TIMESTAMPTZ NULL, CHECK (ended_at IS NULL OR ended_at >= started_at))`.
  - Partial unique indexes (D-28): `UNIQUE (vehicle_id) WHERE ended_at IS NULL` and `UNIQUE (device_id) WHERE ended_at IS NULL`. Index `(vehicle_id, started_at)`.
  - `ALTER TABLE vehicles ADD CONSTRAINT vehicles_paired_device_fkey FOREIGN KEY (paired_device_id) REFERENCES devices(id);`
- **`006_vehicle_pairings.down.sql`** — drop the `vehicles` constraint, then the table.
- Test: `db/roundtrip_test.go` extended so that down-to-zero then up succeeds with 002–006 applied (GF-22 library).

### 2. Config and dependencies

- `go get pgregory.net/rapid@v1.2.0` (pinned in `go.mod` as a direct require; `go.sum` currently has only a stale entry; GF-17 closed). `go mod tidy`.
- **`internal/config/config.go`** — `Load()` additionally requires `DEVICE_KEY_SALT` with `len ≥ 32` bytes (GF-30). Error text names the variable, never its value.
- Tests (`internal/config/config_test.go`, unit): 31-byte salt refused; 32-byte salt accepted; missing salt refused; error text never contains the salt value; existing `JWT_SECRET` cases still pass.

### 3. Device key format, HMAC, verify (`internal/devicekey/key.go`)

- `Generate() (key string, secret []byte, err error)` — 32 bytes from `crypto/rand`, base64url raw, key = `fo_dev_<device_id>_<secret>`. The device id is inserted by the caller, so `Format(deviceID int64, secret []byte) string` is the pure function and `Generate` is the wrapper.
- `Parse(key string) (deviceID int64, secret string, err error)` — strict pattern `^fo_dev_([1-9][0-9]*)_([A-Za-z0-9_-]{43})$`; returns `ErrMalformed` (no detail).
- `Hash(salt []byte, secret string) []byte` — HMAC-SHA256 over the secret only.
- `Verify(salt []byte, secret string, stored []byte) bool` — `hmac.Equal`.
- Tests (unit): format/parse round trip; secret containing `_` and `-` parses correctly; leading zero id, empty secret, wrong length, wrong prefix, extra segment → `ErrMalformed`; HMAC known-answer vector; `Verify` rejects a one-byte-flipped secret and a wrong salt; `Hash` output length 32.

### 4. License normalization and hash (`internal/registry/license.go`)

- `NormalizeLicense(raw string) (string, error)` — trim, ASCII-uppercase, remove spaces and hyphens, then require `[A-Z0-9]{4,32}`; otherwise `ErrValidation` (GF-34).
- `LicenseHash(salt []byte, fleetID int64, normalized string) []byte` — `HMAC-SHA256(salt, "license:" + strconv.FormatInt(fleetID,10) + ":" + normalized)` (D-31).
- Tests (unit): normalization table (`" ab-12 cd "` → `AB12CD`; lowercase; too short; too long; punctuation); determinism (same input twice → same bytes); known-answer vector for a fixed salt, fleet 1, and `AB12CD`; different salt → different hash.
- Tests (integration): same license in fleet 1 and fleet 2 hashes differently (domain separation) and both insert; same license twice in fleet 1 → unique violation mapped to `conflict`.

### 5. Registry stores (`internal/registry/vehicles.go`, `drivers.go`, `devices.go`)

- Stores on `*sql.DB` / `*sql.Tx`, following `internal/users/store.go`. Every query carries `fleet_id`. Every timestamp comes from the injected clock (GF-45).
- `CreateVehicle`, `ListVehicles(fleetID)`, `GetVehicle(fleetID, id)`, `UpdateVehicle(fleetID, id, patch)`; `CreateDriver`, `ListDrivers`, `GetDriver`, `UpdateDriver` (license is re-hashed on change); `CreateDevice(fleetID, kind)` (returns the plaintext key once), `RotateDevice(fleetID, id)`, `RevokeDevice(fleetID, id)`.
- Writes run in a transaction that also calls `audit.Write` (GF-44).
- `CheckDeviceActive(ctx, q, deviceID) error` → `nil`, `ErrDeviceRevoked`, `ErrDeviceNotFound` (GF-41; used by P4 ingest).
- `AuthenticateDeviceKey(ctx, db, salt, key) (Device, error)` → `ErrDeviceKeyMalformed`, `ErrDeviceKeyInvalid`, `ErrDeviceRevoked` (GF-41). Unknown id still runs one HMAC compare against a fixed zero hash to equalize timing.
- Tests (integration, PostGIS): create/list/get/update vehicle and driver scoped to fleet; status CHECK rejects `"broken"`; plate duplicate in one fleet → `conflict`; `CreateDevice` stores only the hash (raw secret absent from `devices`); `RotateDevice` changes the hash and the old key fails `AuthenticateDeviceKey`; `RevokeDevice` sets `revoked_at` once and a second revoke keeps the first timestamp; `CheckDeviceActive` on revoked → `ErrDeviceRevoked`; `AuthenticateDeviceKey` on a valid key returns the device; on a revoked device returns `ErrDeviceRevoked`.

### 6. Pairing service (`internal/registry/pairing.go`)

- `SetPairing(ctx, db, fleetID, vehicleID, in PairingInput{DeviceID *int64, DriverID *int64}, clock) (Pairing, Outcome, error)`; `Outcome ∈ {Created, NoOp}`; `PairingInput.DeviceID == nil` means unpair (GF-38).
- Transaction, in this order (GF-39):
  1. `SELECT … FROM vehicles WHERE id=$1 AND fleet_id=$2 FOR UPDATE`; missing → `ErrNotFound`.
  2. If `DeviceID` set: `SELECT … FROM devices WHERE id=$1 AND fleet_id=$2 FOR UPDATE`; missing → `ErrValidation`; `revoked_at` set → `ErrDeviceRevoked`.
  3. If `DriverID` set: fleet-scoped existence check; missing → `ErrValidation`.
  4. Vehicle `status != 'active'` and `DeviceID` set → `ErrVehicleNotActive`.
  5. Load the open pairing for the vehicle and for the device. If the device has an open row on a different vehicle → `ErrDeviceInUse`.
  6. Same device and same driver as the open row → `NoOp`. Otherwise end the open row (`ended_at = clock`), insert the new row (`started_at = clock`) when `DeviceID` is set, and set `vehicles.paired_device_id` to the new device or NULL, all in this transaction.
  7. `audit.Write` with action `vehicle.pair` (set or swap) or `vehicle.unpair` (`DeviceID == nil` with an open row). A no-op writes no audit row.
- Ended rows are changed only by setting `ended_at`. No DELETE and no other UPDATE path exists.
- Concurrency backstop: the partial unique indexes. A SQLSTATE `23505` on either index maps to `ErrDeviceInUse`, or to `conflict` when it is on the vehicle index.
- Tests (integration):
  - Pair A→X creates one row and mirrors `paired_device_id`.
  - Swap A→Y: row 1 ended with its original `started_at`, row 2 open, mirror = Y.
  - Re-pair A→X: row 3 open, row 2 ended, row 1 unchanged; history has three rows.
  - Same device and driver again → `NoOp`, no new row, no audit row.
  - Unpair → `ended_at` set, mirror NULL, no open row.
  - Device already open on vehicle B, pairing it to A → `ErrDeviceInUse`, nothing written.
  - Retired or maintenance vehicle → `ErrVehicleNotActive`.
  - Revoked device → `ErrDeviceRevoked`.
  - Cross-fleet vehicle, device, or driver → not found or validation error, nothing written.
  - Two goroutines pair the same device to different vehicles → exactly one succeeds, the other gets `ErrDeviceInUse`.
  - Unpair with nothing open → `NoOp`.

### 7. Pairing-invariant property test (`internal/registry/pairing_property_test.go`)

- `TestPairingInvariantProperty` uses `pgregory.net/rapid` with `rapid.Check`. Each check gets a fresh migrated database. `testinfra.NewMigratedDB(t testing.TB)` (`internal/testinfra/database.go:103`) takes a `testing.TB`, and `*rapid.T` is not one, so group 7 adds to testinfra `testinfra.NewMigratedDBFrom(t testing.TB) func() *sql.DB` (or equivalent): the outer `*testing.T` captures the shared container and returns a constructor that each rapid check calls. Fixture: 3 vehicles, 3 devices, and 2 drivers in one fleet.
- Generated op sequences of length 1–40 drawn from: `pair(vehicle, device|nil, driver|nil)`, `rotate(device)`, `revoke(device)`, `issue()`. The clock advances 1 s per op from a fixed start (`2026-10-25T00:00:00Z`).
- After every op, check via SQL:
  - at most one row with `ended_at IS NULL` per vehicle;
  - at most one row with `ended_at IS NULL` per device;
  - `vehicles.paired_device_id` equals the device of the open row, or NULL when there is none;
  - ended rows have `ended_at >= started_at`;
  - rejected ops (`ErrDeviceInUse`, `ErrDeviceRevoked`, `ErrVehicleNotActive`) leave row counts and the mirror unchanged.
- Run: `go test ./internal/registry -run TestPairingInvariantProperty -v` (rapid default check count; the test must pass).

### 8. Registry HTTP handlers (`internal/registry/http.go`)

- Routes (GF-37):
  - `GET /vehicles`, `GET /vehicles/:id`, `POST /vehicles`, `PATCH /vehicles/:id`
  - `GET /drivers`, `GET /drivers/:id`, `POST /drivers`, `PATCH /drivers/:id`
  - `GET /vehicles/:id/pairings`, `POST /vehicles/:id/pairings` (GF-38)
  - `POST /devices`, `POST /devices/:id/rotate`, `POST /devices/:id/revoke` (GF-32, GF-47)
- Role checks run before body parsing (GF-28). The driver role sees only its own driver row (GF-50).
- Bodies: unknown JSON fields → 400 `validation_failed`. Responses never include `license_no`, `license_no_hash`, `api_key_hash`, or any plaintext secret except `api_key` in the issue and rotate responses.
- Tests (integration, real HTTP): each route's happy path and expected body; validation 400 cases (bad plate, bad status, bad license, bad kind, unknown field); a POST with a malformed body from a driver gets 403, not 400; a cross-fleet id gets 404.

### 9. `users.driver_id` linkage (`internal/users/http.go`) — GF-43

- `PATCH /users/:id` accepts an optional `driver_id` (integer or null), admin only. A driver id outside the fleet → 400 `validation_failed`. The write is audited as `user.update`.
- Tests (integration): admin links user to own-fleet driver → `GET /drivers/:id` for that user's token returns 200; unlink → driver's `GET /drivers` returns `[]`; foreign driver id → 400; non-admin → 403.

### 10. Error codes (`internal/apierr`) — GF-42

- Add `vehicle_not_active` (409), `device_revoked` (409), `device_in_use` (409) beside the P2 codes.
- Tests (unit): table test mapping each new and existing code to its HTTP status.

### 11. Role-matrix extension (`services/api/rolematrix_test.go`) — R-6

- Rows (4 roles + unauthenticated columns; expected value per cell):
  - `GET /vehicles`: admin 200, manager 200, dispatcher 200, driver 403, none 401.
  - `GET /vehicles/:id`: admin 200, manager 200, dispatcher 200, driver 403, none 401.
  - `POST /vehicles`: admin 201, manager 201, dispatcher 201, driver 403, none 401.
  - `PATCH /vehicles/:id`: admin 200, manager 200, dispatcher 403, driver 403, none 401.
  - `GET /drivers`: admin 200, manager 200, dispatcher 200, driver 200 (own list), none 401.
  - `GET /drivers/:id` (own id for driver): admin 200, manager 200, dispatcher 200, driver 200, none 401.
  - `POST /drivers`: admin 201, manager 201, dispatcher 201, driver 403, none 401.
  - `PATCH /drivers/:id`: admin 200, manager 200, dispatcher 403, driver 403, none 401.
  - `GET /vehicles/:id/pairings`: admin 200, manager 200, dispatcher 200, driver 403, none 401.
  - `POST /vehicles/:id/pairings` (pair): admin 201, manager 201, dispatcher 403, driver 403, none 401.
  - `POST /devices`: admin 201, manager 403, dispatcher 403, driver 403, none 401.
  - `POST /devices/:id/rotate`: admin 200, manager 403, dispatcher 403, driver 403, none 401.
  - `POST /devices/:id/revoke`: admin 200, manager 403, dispatcher 403, driver 403, none 401.
  - P2 rows (`GET/POST/PATCH /users`) unchanged.
- `go test ./services/api -run RoleMatrix -v` lists every cell.

### 12. Fleet-scoping extension (`services/api/scope_test.go`)

- Fixture: fleet A (admin A, vehicle A, driver A, device A) and fleet B (vehicle B, driver B, device B).
- Admin A: `GET /vehicles` excludes B; `GET /vehicles/<B>` → 404; `PATCH /drivers/<B>` → 404; `POST /devices/<B>/rotate` → 404; `POST /vehicles/<A>/pairings` with `device_id=<B>` → 400 `validation_failed`; `POST /vehicles/<A>/pairings` with `driver_id=<B>` → 400; a token with forged `fleet_id=B` → 401.
- Driver A: `GET /drivers/<B>` → 404; `GET /drivers` returns only A's row.

### 13. Audit tests (`internal/registry/audit_test.go`) — GF-44

- Each successful admin write (vehicle create/update, driver create/update, device issue/rotate/revoke, pair, swap, unpair) inserts one `admin_audit` row with the actor id, `target` in `vehicle:<id>` / `driver:<id>` / `device:<id>`, and the expected action.
- Failed writes (400, 403, 404, 409) insert no audit row.
- No audit row contains a license number, a key, or a secret.

### 14. API wiring, env docs

- `services/api/server.go`: `Deps` gains `DeviceKeySalt []byte`; routes from groups 8–9 are registered.
- `services/api/main.go`: `config.Load()` now refuses a short or missing `DEVICE_KEY_SALT` before opening the DB.
- `CLAUDE.md` (implement scope, stays under 200 lines): `DEVICE_KEY_SALT` is required and must be ≥ 32 bytes; the local-run line includes it; the device endpoints are admin only; `DEVICE_KEY_SALT` rotation invalidates all device keys and license hashes.
- `deploy/PORTS.md`, `README.md`, compose: unchanged.

### 15. Gates and validation run

- Run the Validation items below on the host. Record outputs in the step report. Fix any failure before the branch is ready.

## Requirements

Binding and carried verbatim from `tech-stack.md` §11: D-27, D-28, D-29, D-30, D-31, GF-2, GF-3, GF-5, GF-17. New decisions GF-30 … GF-51 are recorded here only (no new mission §7 or tech-stack §11 rows are required). Zero open decisions.

### Reused — binding (tech-stack §11, verbatim)

- **D-27** — `vehicles.status` = registry status `{active, maintenance, retired}`. Map display status `{idle, en_route, offline}` is derived server-side: `offline` if `server_ts` older than 120 s; `en_route` if the vehicle's job is `en_route` or `picked_up`; otherwise `idle`. *Rationale (tech-stack): §2.1.1 and §2.1.4 both say "status"; separating them removes the clash.* (Display status is P8; P3 stores only the registry status.)
- **D-28** — `vehicle_pairings(id, fleet_id, vehicle_id, driver_id NULL, device_id, started_at, ended_at)`; partial unique indexes on current rows (`ended_at IS NULL`) per vehicle and per device; `vehicles.paired_device_id` mirrors the current pairing in the same transaction. **SPEC-AMEND** (additive). *Rationale (tech-stack): §2.1.1 needs pairing history; §5 has only the current pointer.*
- **D-29** — Device keys: `fo_dev_<device_id>_<secret>`, secret = 32 random bytes base64url; stored as `HMAC-SHA256(DEVICE_KEY_SALT, secret)`; constant-time compare; rotate replaces the hash immediately (no grace); revoke sets `devices.revoked_at` (additive); key shown once. **SPEC-AMEND** (additive column and endpoints). *Rationale (tech-stack): §8 hashed, rotatable, revocable; the device id in the key avoids a scan.*
- **D-30** — PWA identity: the phone is a `devices` row with `kind=pwa` bound to a driver; the admin issues the key on a pairing screen, shown once, and the driver enters it on first run. The PWA posts positions with that key. **SPEC-AMEND** (device key endpoints). *Rationale (tech-stack): §2.1.3 has the PWA posting positions; §8 gives devices API keys only.* (P3 realises "bound to a driver" through `vehicle_pairings.driver_id`; the devices table has no driver column — GF-33.)
- **D-31** — License hash: `HMAC-SHA256(DEVICE_KEY_SALT, "license:" + fleet_id + ":" + normalized_no)`. *Rationale (tech-stack): Deterministic (needed for per-fleet uniqueness), domain-separated, and adds no new secret.*
- **GF-2** — `admin_audit(id, fleet_id, actor_id, action, target, at)` append-only table; every admin write records the actor (P2, P3). **SPEC-AMEND.** *Rationale (tech-stack): README §8 "admin actions logged with actor" had no owning table or phase.* (P3 writes use `audit.Write`; the table and its trigger came in P2.)
- **GF-3** — Device key endpoints named `POST /devices`, `POST /devices/:id/rotate`, `POST /devices/:id/revoke` (P3). **SPEC-AMEND.** *Rationale (tech-stack): D-29 and D-30 define the operations; paths needed for P3 role matrix.*
- **GF-5** — Migrations: sequential zero-padded `NNN_snake_name.up.sql` / `.down.sql` in `db/migrations/`, starting `001`, no timestamps; single transaction-friendly scripts (no `BEGIN`/`COMMIT`, no `CREATE INDEX CONCURRENTLY`); forward-only once merged to `main`; `make migrate` exits non-zero when `schema_migrations.dirty = true`, recovery via `make migrate MIGRATE_ARGS="force <version>"` after manual schema inspection, never by SQL edits to `schema_migrations`. *Rationale (tech-stack): One feature branch per phase makes sequential numbers safe; golang-migrate's dirty flag is the only safe signal.*
- **GF-17** — `pgregory.net/rapid` is not added in P1 (no property test in the P1 roadmap); it enters with the first property test in P3. *Rationale (tech-stack): Avoids an unused dependency.* → **Closed in P3** by group 2 and group 7.

### Closed in P3

- **GF-17 — closed.** `pgregory.net/rapid` is pinned in `go.mod` in group 2 and first used by the pairing-invariant property test (group 7).
- **P2 GF-27 deferral — closed.** `users.driver_id` gets its foreign key to `drivers(id)` in migration `005` (group 1), and the P2 "no FK until P3" note no longer applies.

### New in P3 (GF-30 … GF-51)

- **GF-30 — `DEVICE_KEY_SALT` config.** Required, `len ≥ 32` bytes, checked in `config.Load()` before any DB open. Error names the variable, never its value. *Rationale: same bar as `JWT_SECRET` (D-03); the salt is the HMAC key for device keys and license hashes, so it carries the same entropy requirement. Refusing at start stops a fleet from running with an unkeyed or weak hash.*
- **GF-31 — device key format and parser.** Key = `fo_dev_<device_id>_<secret>`; `device_id` is a decimal integer without leading zeros; `secret` = 32 random bytes encoded base64url without padding (43 characters; alphabet `A–Z a–z 0–9 - _`). Parser regex: `^fo_dev_([1-9][0-9]*)_([A-Za-z0-9_-]{43})$`. Because the secret alphabet contains `_`, the parser splits at the first `_` after the digits, which is unambiguous because the id contains no `_`. Any mismatch → `ErrDeviceKeyMalformed`, with no detail returned to callers. *Rationale: a fixed-length secret and an anchored regex make the parse total and make a truncated or pasted key fail before the database is touched.*
- **GF-32 — key lifecycle.** `api_key_hash = HMAC-SHA256(DEVICE_KEY_SALT, secret)` (HMAC over the secret only, D-29). Issue (`POST /devices`) and rotate return `api_key` in the response body once; no other endpoint or column returns it. Rotate replaces the hash immediately (no grace, D-29); the old key then fails with `ErrDeviceKeyInvalid`. Revoke sets `revoked_at` on the first call; a repeat call returns 200 with the original timestamp. Revoked is terminal in P3 (no un-revoke). Rotating a revoked device → 409 `device_revoked`. Revoke leaves existing pairing rows untouched; pairing a revoked device is refused (GF-39), and P4 refuses its ingest through `CheckDeviceActive`. *Rationale: one-shot plaintext and a stored hash satisfy R-9; leaving pairings alone keeps the mirror rules in one place and the refusal point in P4.*
- **GF-33 — device kinds and binding.** `kind` ∈ `{pwa, tracker}`, else 400 `validation_failed`. The `devices` table has no driver column (tech-stack §6.1 exact); the D-30 binding is expressed through `vehicle_pairings.driver_id` on the open pairing. *Rationale: keeps the schema exactly as specified and reuses the pairing history for the driver link.*
- **GF-34 — license normalization.** `NormalizeLicense`: trim surrounding whitespace, ASCII-uppercase, delete all spaces and hyphens, then require `[A-Z0-9]{4,32}`; otherwise 400 `validation_failed`. The hash input is `"license:" + decimal(fleet_id) + ":" + normalized` (D-31). Stored as `BYTEA`. Unique `(fleet_id, license_no_hash)` → 409 `conflict` on duplicates within one fleet. Plaintext license numbers are never stored, returned, or logged; responses omit the field entirely. *Rationale: normalization makes the hash deterministic across typed variants; the per-fleet unique index makes duplicates a registry error without leaking the number.*
- **GF-35 — plate normalization.** Trim, ASCII-uppercase, then match `^[A-Z0-9][A-Z0-9 -]{0,14}[A-Z0-9]$` (2–16 characters) else 400 `validation_failed`; `UNIQUE (fleet_id, plate)` → 409 `conflict`. *Rationale: a fixed grammar rejects junk while accepting real plates from several countries.*
- **GF-36 — vehicle status.** Enum exactly `{active, maintenance, retired}`: DB `CHECK` plus service validation (400 `validation_failed` otherwise). Default `active` on create. `PATCH` may set any of the three. Pairing a vehicle whose status is not `active` → 409 `vehicle_not_active`. A status change never alters pairings (GF-39). *Rationale: the status is a registry flag, not a pairing action; keeping them separate means the mirror has one writer.*
- **GF-37 — registry CRUD surface.** `GET` list, `GET` by id, `POST`, `PATCH` for `/vehicles` and `/drivers`. No `DELETE`: the tech-stack §7.1 surface is `GET/POST/PATCH` and vehicles retire by status (D-27). Drivers have no status column (§6.1), so a driver is never removed in P3; this is an accepted gap listed under Out of scope. Lists return every row of the fleet ordered by `id` ASC, with no pagination (fleets are 100–500 vans, mission §1). *Rationale: the roadmap's "CRUD" resolves to the endpoint set already named in §7.1; a hard delete would orphan pairings and audit targets.*
- **GF-38 — pairing endpoints.** `GET /vehicles/:id/pairings` → 200 array of `{id, vehicle_id, driver_id, device_id, started_at, ended_at}` ordered by `id` DESC, no pagination. `POST /vehicles/:id/pairings` body `{"device_id": int|null, "driver_id": int|null}`: `device_id` non-null pairs or swaps; `device_id: null` unpairs (ends the open row). `driver_id` without a `device_id` → 400. Responses: a created row → 201 with the new row; a no-op (same device and driver already open, or unpair with nothing open) → 200 with the current row or JSON `null`; an unpair that ends a row → 200 with the ended row. *Rationale: `device_id: null` keeps the endpoint set exactly `GET/POST /vehicles/:id/pairings` while still allowing unpair, which the property test needs.*
- **GF-39 — pairing transaction rules.** One transaction per call. Lock order: `vehicles` row `FOR UPDATE`, then `devices` row `FOR UPDATE` (fixed order prevents deadlock). Checks in order: vehicle in fleet (404 otherwise); device in fleet (400 otherwise); device not revoked (409 `device_revoked`); driver in fleet (400 otherwise); vehicle active (409 `vehicle_not_active`); device not open on another vehicle (409 `device_in_use`). Then: same device and driver open → no-op; else end the open row (`ended_at = clock`), insert the new row (`started_at = clock`), set `vehicles.paired_device_id`, and `audit.Write` (`vehicle.pair` or `vehicle.unpair`), all in the same transaction. A no-op writes no audit row. Only `ended_at` is ever set on an existing pairing row; no DELETE. Partial unique indexes are the backstop; SQLSTATE `23505` maps to 409 `device_in_use` or `conflict` by index. *Rationale: the locks make the check-then-write race-free within a single database, and the indexes make the invariant hold even if a code path is missed.*
- **GF-40 — pairing invariant.** At most one open row per vehicle and per device (DB, D-28). `vehicles.paired_device_id` equals the open row's device or NULL, and is written only by the pairing service in the pairing transaction. Enforced in code and verified by the property test (group 7); no trigger (R-7 does not apply to P3). *Rationale: one writer for the mirror keeps the invariant local to one function.*
- **GF-41 — device check functions for P4.** `CheckDeviceActive(ctx, q, deviceID) error` → `nil`, `ErrDeviceRevoked`, `ErrDeviceNotFound`. `AuthenticateDeviceKey(ctx, db, salt, key) (Device, error)` → `ErrDeviceKeyMalformed`, `ErrDeviceKeyInvalid`, `ErrDeviceRevoked`, with a timing-equalized miss. Neither updates `last_seen_at` (P4). No HTTP route in P3 calls them. *Rationale: P3 defines the semantics; P4 only wires the HTTP path to them.*
- **GF-42 — error codes.** Added to `internal/apierr`: `vehicle_not_active` 409, `device_revoked` 409, `device_in_use` 409. The GF-28 table extends with these three. *Rationale: each refusal has its own code so clients and the role matrix can assert which rule fired without reading messages.*
- **GF-43 — user driver link.** `PATCH /users/:id` (admin only, P2 endpoint) accepts an optional `driver_id` (integer or null). A driver id outside the fleet → 400 `validation_failed`. A linked user with role `driver` sees that driver under `GET /drivers`. The FK from migration `005` enforces existence. *Rationale: "own profile" for the driver role has no other source of truth; the link must exist before the driver-scope rules can be tested.*
- **GF-44 — registry audit.** Every P3 write records the actor through `audit.Write` in the same transaction: `vehicle.create`, `vehicle.update`, `driver.create`, `driver.update`, `device.issue`, `device.rotate`, `device.revoke`, `vehicle.pair`, `vehicle.unpair`. Targets are `vehicle:<id>`, `driver:<id>`, `device:<id>`. Writes by dispatcher or manager are audited too (stricter than GF-2's "admin writes"). Failed writes write nothing. `admin_audit` has no payload column, so audit rows cannot hold a secret. *Rationale: the audit trail is the only record of who rotated a key or swapped a van.*
- **GF-45 — clock.** Every P3 timestamp (`started_at`, `ended_at`, `revoked_at`, `created`/`rotated` markers) comes from the injected `Clock func() time.Time`. Column defaults are a fallback only. *Rationale: the property test and the swap-history test need deterministic, monotone times, and `ended_at >= started_at` is then checkable.*
- **GF-46 — property test shape.** `rapid.Check`, default check count, op sequences of length 1–40, one fresh migrated PostGIS database per check through `testinfra`. The package shares one PostGIS container through `sync.Once` (GF-29). *Rationale: a fresh database per check keeps counterexamples reproducible; the container is shared to keep the run time bounded.*
- **GF-47 — device endpoints auth.** `POST /devices`, `POST /devices/:id/rotate`, `POST /devices/:id/revoke` require an admin JWT (README §8) and no device key. The body of `POST /devices` carries only `kind`; `fleet_id` comes from the principal. *Rationale: device keys authenticate devices, not admins; the admin path is the one P3 owns.*
- **GF-48 — list and ordering.** All P3 lists return rows ordered by `id`; pairing history by `id` DESC. No pagination in P3. *Rationale: bounded fleet sizes (mission §1); pagination is backlog.*
- **GF-49 — driver scope on registry reads.** A driver with a linked `driver_id` sees `GET /drivers` as a one-element array of its own row, and `GET /drivers/:id` for another id → 404 (not 403), so existence is not revealed. A driver with no link sees `[]`. A driver on any `/vehicles` route → 403. Driver writes on `/drivers` → 403. *Rationale: GF-28's order puts role before scope, but for the driver's own row the role is allowed and the scope check decides, so 404 is the right answer for another driver.*
- **GF-50 — key and salt rotation.** Changing `DEVICE_KEY_SALT` invalidates every device key and every license hash. P3 ships no salt-rotation tooling; the operator runbook (CLAUDE.md, group 14) states the consequence. *Rationale: the salt is the HMAC key by design (D-29, D-31); a re-keying tool is a separate phase.*
- **GF-51 — empty-body and unknown-field rules.** Unknown JSON fields → 400 `validation_failed` on P3 bodies (`DisallowUnknownFields`). `PATCH` bodies with no recognized field → 400 `validation_failed`. *Rationale: silent field drops in a registry hide typos in dispatcher tooling.*

## Validation

Runnable on this host. Host tools: `/root/.local/go/bin/go`, `/root/.local/bin/golangci-lint`, `/root/go/bin/migrate`, wired through `deploy/.local/tools.mk`. Docker is available for testcontainers. For manual checks, a local DB runs with `docker compose -f deploy/docker-compose.yml up -d postgres` (127.0.0.1:10002). Secrets are generated per shell and never committed.

1. **Gates.** `make fmt-check` → exit 0, no files listed. `make lint` → exit 0. `make test` (`-race -count=1`) → exit 0 for all packages. **Pass:** all three exit 0.
2. **Migrations.** `DATABASE_URL=… make migrate` → exit 0, version 6. `make migrate MIGRATE_ARGS="down 2"` then `make migrate` → exit 0 both; `schema_migrations.dirty = false`. `db/roundtrip_test.go` passes down-to-zero then up. **Pass:** all exit 0, and the round-trip test passes.
3. **Schema shape.** `psql -c '\d vehicle_pairings'` lists both partial unique indexes (`WHERE (ended_at IS NULL)`). `\d devices` lists `revoked_at` and no other extra columns. **Pass:** both present; no `created_at` on the new tables.
4. **Role matrix (R-6).** `go test ./services/api -run RoleMatrix -v` lists every cell of group 11 and each passes. **Pass:** every cell listed and matching its expected value.
5. **Pairing history across swaps.** `go test ./internal/registry -run TestPairingSwapKeepsHistory -v`: open A→X, swap A→Y, re-pair A→X. **Pass:** three rows; row 1 `ended_at` set and `started_at` unchanged; row 2 ended; row 3 open; mirror = X; `GET /vehicles/:id/pairings` returns the three rows newest first.
6. **Pairing invariant property test.** `go test ./internal/registry -run TestPairingInvariantProperty -v` → PASS with no counterexample. **Pass:** exit 0, and the test name appears in the output.
7. **Hash verification.** `go test ./internal/devicekey -run 'Test(Format|Parse|Hash|Verify)' -v` and `go test ./internal/registry -run 'TestLicense' -v`. **Pass:** known-answer vectors match; determinism passes; cross-fleet inputs produce different hashes; a flipped secret fails `Verify`.
8. **Key shown once.** `go test ./internal/registry -run TestDeviceKeyLifecycle -v` (issue, rotate, revoke over HTTP). **Pass:** the issue and rotate responses each contain one `fo_dev_` key matching the format; no other response in that test contains `fo_dev_`; `SELECT count(*) FROM devices WHERE api_key_hash = convert_to($key,'UTF8')` = 0; the old key fails after rotate; `api_key_hash` is 32 bytes.
9. **Audit rows (GF-44).** `go test ./internal/registry -run TestRegistryAudit -v`. Manual: after one `POST /devices` as admin, `SELECT actor_id, target, action FROM admin_audit` → one row with the admin id, `device:<id>`, `device.issue`. **Pass:** the test passes and the manual row matches.
10. **Revoked device check (GF-41).** `go test ./internal/registry -run 'TestCheckDeviceActive|TestAuthenticateDeviceKey' -v`. **Pass:** revoked → `ErrDeviceRevoked`; malformed → `ErrDeviceKeyMalformed`; wrong secret → `ErrDeviceKeyInvalid`; valid → device.
11. **Config refusal (GF-30).** `DATABASE_URL=… JWT_SECRET=$(openssl rand -hex 32) go run ./services/api` (no `DEVICE_KEY_SALT`) → exits non-zero, message names `DEVICE_KEY_SALT`, no value printed. With a 31-byte salt → refused. **Pass:** both refused; the config test passes.
12. **No secret leakage (R-9).** `grep -R "fo_dev_[A-Za-z0-9]" --include=*.go --include=*.sql --include=*.md .` finds only test fixtures that use a made-up key (no real value). `git diff --stat` touches no `.env`. **Pass:** no real-looking key committed; no `.env` change.
13. **Scope fence.** `git diff --name-only main…HEAD` contains no `ingest`, `ws`, `hub`, `positions`, `geofence`, `alert`, `jobs`, `nats`, or `map` paths. `grep -R "ingest/position" services/` → no matches. **Pass:** zero matches.
14. **Fleet scoping.** `go test ./services/api -run FleetScope -v` passes. Manual: `PATCH /drivers/<fleet-B id>` with the fleet-A admin token → 404. **Pass:** both.

## Testing depth

Roadmap P3 + tech-stack §9.

- **Unit (stdlib `testing`, no DB):** device key format, parse, HMAC, verify; license normalization and hash; apierr table; config salt rules; plate and status validation.
- **Integration (testcontainers PostGIS, real migrations, real HTTP):** registry stores; pairing lifecycle (swap, unpair, no-op, device in use, not active, revoked, cross-fleet, concurrent pair race); device key lifecycle; registry audit; users driver link; CRUD over HTTP with validation cases.
- **Property (`pgregory.net/rapid`):** pairing invariants over random pair/unpair/rotate/revoke/issue sequences with an injected clock, each check on a fresh migrated database (roadmap P3 and tech-stack §9 "pairing invariant").
- **Role matrix (R-6):** integration table test, every resource × verb × role (4 roles + unauthenticated).
- **Fleet scoping:** cross-fleet 404 and forged-claim 401; driver own-row scope.
- **Migrations:** round trip `up → down → up` in `db/roundtrip_test.go`; partial unique index presence is asserted by an integration test.
- **Race:** `go test -race` runs everywhere (`make test`). The CI race gate for ingest and hub is P4+.
- **No mocks** of Postgres, HTTP, or the transport; the clock is injected only through `func() time.Time`.

## SPEC-AMEND repeat (R-4)

Each item is an additive change recorded in `mission.md` §7 under the rows named; no new mission row is needed beyond those already present.

- **D-28** — `vehicle_pairings` table, partial unique indexes, and `vehicles.paired_device_id` mirror (mission §7: P3, additive table). Endpoint `GET/POST /vehicles/:id/pairings` (mission §7: P3, additive endpoints).
- **D-29** — `devices.revoked_at` column (mission §7: P3, additive column) and key rotate and revoke semantics.
- **D-29 / D-30** — device key endpoints `POST /devices` (issue), `POST /devices/:id/rotate`, `POST /devices/:id/revoke`, admin only (mission §7: P3, additive endpoints).
- **GF-3** — `POST /devices/:id/rotate` and `POST /devices/:id/revoke` paths (mission §7: P3).
- **GF-2** — every P3 admin write records the actor (mission §7: P2, P3).
- **D-27** — `vehicles.status` enum stored (mission §7 does not list D-27; it is a tech-stack §11 decision, no new row).

Additive field on a P2 endpoint, recorded in this spec only (GF-43): `PATCH /users/:id` accepts `driver_id`. No mission row is needed because the P2 endpoint is unchanged in path and verbs.

## Out of scope

- Any P4+ behavior: `POST /ingest/position`, device-key HTTP authentication, `last_seen_at` updates, NATS (`-js`), `GPS_RAW`, positions, `vehicles_latest`, fanout, presence, WS hub, map, jobs, job transitions, geofences, alerts, reports, OSRM, simulator.
- Driver PWA key setup UI (P14); driver job matrix rows (P11).
- `DELETE` on any registry resource; driver status or driver removal; pagination on lists or pairing history.
- Un-revoke, key grace periods, salt rotation tooling (GF-50), `GET /devices`, device-to-driver columns.
- Compose edits; README edits (R-8); `deploy/PORTS.md` edits (no port change); MinIO; Redis.
- Implementation of any of the above in this run: the persist step writes `specs/features/03-registry-pairing.md` only; code, migrations, Makefile, and CLAUDE.md changes happen in the implement step, starting at group 1.
