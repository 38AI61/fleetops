# Feature 04 — Position gateway (P4)

Status: approved plan, persisted 2026-10-25. Branch `phase-4-position-gateway`.
Source plan: `/root/.claude/plans/p4-plan-distributed-quail.md` (approved). Binding constitution: `specs/constitution/roadmap.md` §1 (R-1…R-9) and §3 P4, `specs/constitution/tech-stack.md` §3, §4, §5, §6, §7, §8, §9, §11 (D-15, D-16, D-18, D-29, GF-12), `specs/constitution/mission.md` §7 (SPEC-AMEND rows), `AGENTS.md`, `CLAUDE.md`.

This spec is written before any P4 code (R-1, SDD). Implementation starts at Plan task group 1.

## Scope

Roadmap P4 (`specs/constitution/roadmap.md` §3), verbatim:

> **P4 — Position gateway** · D-15, D-16, D-29 · README §3, §6 `/ingest/position`
> - Scope: `POST /ingest/position` with device-key auth (constant-time compare); validates `lat, lng, speed, heading, accuracy, ts`; stamps `msg_id` (UUIDv7) and `server_ts`; async publish to `GPS_RAW` (`gps.raw.<fleet_id>`) with 204 on accept, 503 when the pending buffer is full; NATS container gets `-js` (compose change in this branch).
> - Validation: valid → 204 and message in stream; invalid → 400 and nothing published; revoked key → 401; full buffer → 503.
> - Testing: integration on testcontainers NATS with JetStream (the four cases above; no mocks of NATS).

P4 delivers: the `-js` compose change (plus the GF-12 Postgres tag correction, see GF-62); `config` gains `NATS_URL` (name already in §12; GF-63); `internal/ingest` (validation, stamping, stream ensure, publish gate, HTTP handler); the device-key HTTP path built on `registry.AuthenticateDeviceKey`; `apierr` code `busy`; a NATS JetStream testcontainer helper; the R-6 role-matrix extension; four named integration cases.

Nothing from P5+ is built in P4: no buffer, no flush, no DB writes, no `positions`, no `vehicles_latest`, no consumer, no partitions, no presence, no WS, no map, no simulator, no MinIO, no metrics, no README or `deploy/PORTS.md` edits. **P4 adds no migration** (schema untouched). **No new env names.** No port change.

## Plan

Task groups in build order. Each group ends in a commit on `phase-4-position-gateway`. Tests live beside code.

### 1. Compose and config

- `deploy/docker-compose.yml`, nats service: add `command: ["-js"]` (GF-62). Postgres image `postgis/postgis:16` → `postgis/postgis:16-3.4` (GF-12 correction, same file, same commit).
  - Test: assertion in Validation 3 (grep, no `docker compose up`).
- `internal/config/config.go`: add `NATSURL` read from `NATS_URL`, required, error names the variable and never prints a value (GF-63).
  - Tests (`internal/config/config_test.go`, unit): missing `NATS_URL` refused; set `NATS_URL` accepted; existing `JWT_SECRET` and `DEVICE_KEY_SALT` cases still pass; error text contains no secret values.

### 2. Dependencies

- `go get github.com/nats-io/nats.go` (the `jetstream` package) and `github.com/testcontainers/testcontainers-go/modules/nats` at the same testcontainers-go version already pinned in `go.mod`; `github.com/google/uuid` promoted from indirect to direct (GF-66). `go mod tidy`. Versions pinned in `go.mod`.
  - Test: `make test` compiles every package (no new test; gate in group 12).

### 3. Error codes (`internal/apierr`)

- Add `busy` → 503 (GF-60). `unauthorized` (existing, P2) is the 401 code for every device-key failure (GF-53). `validation_failed` (existing) is the 400 code (GF-56).
  - Tests (unit): table test mapping `busy` → 503 beside the existing and P3 codes.

### 4. Validation (`internal/ingest/validate.go`)

- `type PositionRequest struct` with `*float64` for `lat, lng, speed, heading, accuracy` and `*string` for `ts`, so a missing or `null` field is detected. Decoder: `json.Decoder` with `DisallowUnknownFields`, body capped by `http.MaxBytesReader` at 4096 bytes, one JSON value only (GF-54).
- `func Validate(req PositionRequest, now time.Time) (Position, error)`: bounds in GF-55 and ts window in GF-56. Returns a typed `FieldError{Field string}` that wraps `registry.ErrValidation`-style sentinel `ErrInvalidPosition`; no values echoed.
  - Tests (unit, table): each field at its inclusive min and max accepted; one unit outside rejected for each field (`lat` 90.0001, `lng` -180.0001, `speed` 300.5, `heading` 360.1, `accuracy` 10000.5); missing field, `null`, string-typed number, `ts` not RFC3339, unknown field, trailing JSON value, body over 4096 bytes → each 400 with the field name; `ts` at `now+5m` accepted, `now+5m+1s` rejected, `now-24h` accepted, `now-24h-1s` rejected.
  - Test name: `TestValidatePosition`.

### 5. Stamping and payload (`internal/ingest/stamp.go`)

- `type Clock func() time.Time` (injected; GF-57). `func Stamp(dev registry.Device, p Position, clock Clock, newID func() (uuid.UUID, error)) (Message, error)`: `msg_id = uuid.NewV7()`, `server_ts = clock().UTC()`, both set after auth and validation succeed (D-15). `Message.Subject() = "gps.raw." + strconv.FormatInt(dev.FleetID, 10)`. `Message.Payload()` JSON per GF-58.
  - Tests (unit): `TestStampMessageIDIsUUIDv7` (version nibble 7, variant RFC 4122, two stamps differ); `TestStampServerTSFromClock` (server_ts equals the injected instant, UTC); `TestStampSubjectUsesDeviceFleet` (fleet 42 → `gps.raw.42`); `TestStampPayloadKeys` (exact key set and RFC3339Nano UTC timestamps, no extra keys).

### 6. Stream ensure (`internal/ingest/stream.go`)

- `func EnsureStream(ctx, js jetstream.JetStream) error` — `CreateOrUpdateStream` with the GF-59 config. Idempotent. Called once at API boot after NATS connect (group 9) and by the test helper (group 8).
  - Tests: covered by integration group 10 (`TestIngestPositionValid204`) and by `TestEnsureStreamIdempotent` in `internal/ingest` (integration, real NATS: call twice, config unchanged, `MaxAge` 60 s, `MaxBytes` 268435456, subjects `gps.raw.>`).

### 7. Publish gate (`internal/ingest/gate.go`)

- `type Gate struct` with a buffered channel of capacity `MaxInFlight = 1024` (GF-60). The JetStream context is created with `jetstream.WithPublishAsyncMaxPending(MaxInFlight)` so the library never stalls: in nats.go that option blocks `PublishAsync` at the cap and only errors after a 200 ms stall timeout (`ErrTooManyStalledMsgs`), so the gate's own non-blocking slot is the sole bound. `func (g *Gate) Publish(ctx, js jetstream.JetStream, subject string, payload []byte) error`: non-blocking acquire of a slot (`select` with `default`); no slot → `ErrBusy`; else `js.PublishAsync`; a synchronous error from `PublishAsync` releases the slot and returns `ErrBusy`; otherwise a goroutine waits on the `PubAckFuture` `Ok()`/`Err()` and releases the slot. Accepted publishes return nil immediately (204 before the ack, README §3).
  - Tests (unit, no NATS): `TestGateRejectsWhenFull` (fill 1024 slots through the internal acquire, next acquire → `ErrBusy`, release one → acquire succeeds).
  - Tests (integration, real NATS): `TestGatePublishReleasesSlotOnAck` (publish 2048 messages sequentially with `Ok()` waits; slots return to 1024; every message present in the stream).

### 8. NATS JetStream test helper (`internal/testinfra/nats.go`)

- `NewJetStream(t testing.TB) jetstream.JetStream`: one NATS container per test package via `sync.Once` (GF-65), started with JetStream enabled (`-js`), image `nats:2`, OS-assigned host port (GF-13). Each call deletes stream `GPS_RAW` if present (ignore not-found) so tests start empty. Container terminated at `TestMain` exit.
- `FetchSubject(t testing.TB, js jetstream.JetStream, stream, subject string, want int, timeout time.Duration) [][]byte`: ordered consumer with `DeliverAll` and subject filter; fetches up to `want`; fails the test if fewer arrive before `timeout`.
  - Tests: `TestNATSHelperFetchesPublished` (publish 3 messages, `FetchSubject` returns them in order).

### 9. Device-key route and wiring (`internal/ingest/http.go`, `services/api/server.go`, `services/api/main.go`)

- `POST /ingest/position` (README §6, tech-stack §7.1). Handler order: (1) read `X-Device-Key` (missing or repeated → 401); (2) `registry.AuthenticateDeviceKey(ctx, db, salt, key)`: `ErrDeviceKeyMalformed`, `ErrDeviceKeyInvalid`, `ErrDeviceRevoked`, and any `ErrDeviceNotFound` → 401 `unauthorized`; (3) decode and validate (400 `validation_failed`); (4) `Stamp`; (5) `Gate.Publish` (`ErrBusy` or a publish error → 503 `busy`); (6) 204. No DB write on any path (GF-67). No JWT is accepted on this route (GF-64).
- `services/api/server.go`: `Deps` gains `NATS jetstream.JetStream`, `Gate *ingest.Gate`, `Clock`; route registered without the JWT group. `DeviceKeySalt` and `DB` already exist from P3.
- `services/api/main.go`: after `config.Load()`, connect NATS from `NATSURL`, call `ingest.EnsureStream`, then serve. Startup refuses on NATS failure.
  - Tests: covered by groups 10 and 11.

### 10. Ingest integration cases (`services/api/ingest_test.go`)

Real HTTP through `NewServer(Deps)`, real PostGIS (`testinfra`), real NATS JetStream (`testinfra.NewJetStream`). Keys come from `devicekey.Generate`; devices are created through the P3 registry stores (no mocks).

- `TestIngestPositionValid204`: a valid body with an active device key → 204; `FetchSubject(js, "GPS_RAW", "gps.raw.<fleet_id>", 1, 5s)` returns one message whose payload has `msg_id` (UUIDv7), `server_ts` within the test window, `device_id`, `fleet_id`, and the six validated fields.
- `TestIngestPositionInvalidPublishesNothing`: table of invalid bodies (out-of-range lat, missing `ts`, unknown field, string-typed speed, body over 4096 bytes, `ts` 25 h old) → each 400 `validation_failed`; after all of them, the stream has 0 messages (`js.Stream(...).Info` `State.Msgs == 0`).
- `TestIngestPositionRevokedKey401`: revoke the device through `RevokeDevice`; the previously valid key → 401 `unauthorized`; also a wrong-secret key → 401 and a malformed key → 401 (same body); nothing published.
- `TestIngestPositionFullBuffer503`: the test acquires all 1024 gate slots through the package's test hook, a valid request → 503 `busy`; after releasing the slots, the same request → 204.

Run: `go test ./services/api -run 'TestIngestPosition' -v` (all four named tests appear in the output).

### 11. Role-matrix extension (`services/api/rolematrix_test.go`) — R-6

- New route `POST /ingest/position` with columns: admin JWT, manager JWT, dispatcher JWT, driver JWT, no auth, valid device key. Expected: JWT columns 401, none 401, valid device key 204 (for a valid body). Malformed device key 401.
- Existing JWT rows unchanged.
- Test: `go test ./services/api -run RoleMatrix -v` lists every new cell.

### 12. CLAUDE.md (implement scope, stays under 200 lines — currently 78)

- Local-run line adds `NATS_URL=nats://127.0.0.1:10004`.
- `/ingest/position` is device-key authenticated (`X-Device-Key` header), not JWT.
- Compose `nats` runs with `-js`.

### 13. Gates and validation run

- Run the Validation items on the host and record the output in the step report. Fix any failure before the branch is ready.

## Requirements

Binding and carried verbatim from `tech-stack.md` §11 and `mission.md` §7: D-15, D-16, D-29, GF-12. D-18 is context (P5 applies the stale drop; P4 only sets the stream retention that makes it possible). R-6 applies to the new route. New decisions GF-52 … GF-67 are recorded here only. Zero open decisions.

### Reused — binding

- **D-15** — Gateway assigns `msg_id` (UUIDv7) and `server_ts`, both carried in the NATS payload. Unique index `(server_ts, msg_id)` on each partition; inserts `ON CONFLICT DO NOTHING`. *Rationale (tech-stack): Makes at-least-once redelivery safe and gives the property test a real invariant.* (The index is P5/P6; P4 supplies the fields.)
- **D-16** — JetStream stream `GPS_RAW` (subjects `gps.raw.>`, file storage, `MaxAge` 60 s to match the stale rule, bounded `MaxBytes`); durable consumer with explicit ack after commit; NAK with delay on failure; `MaxDeliver` then terminate + metric. Gateway uses async publish and returns 503 when the pending buffer is full (backpressure). NATS container gets `-js`. **SPEC-AMEND.** *Rationale (tech-stack): §7 "fire-and-forget" + "at-least-once + idempotent upserts" (§14) need a durable stream; async publish keeps 204 immediate.* (P4 delivers the stream and gateway side; consumer and NAK/terminate are P6.)
- **D-18** — Context: flush at 1 s or 500; drop `accuracy > 50 m` before buffering; drop `server_ts` older than 60 s at flush. P4 does not implement the drops; GF-56 and GF-59 keep the data needed for them.
- **D-29** — Device keys: `fo_dev_<device_id>_<secret>`, secret 32 random bytes base64url, stored as `HMAC-SHA256(DEVICE_KEY_SALT, secret)`, constant-time compare, rotate replaces immediately, revoke sets `devices.revoked_at`, key shown once. **SPEC-AMEND.** *Rationale (tech-stack): §8 hashed, rotatable, revocable; the device id in the key avoids a scan.* (P4 verifies only, through `registry.AuthenticateDeviceKey` (GF-41), reusing P3's `devicekey.Verify`.)
- **GF-12** — Test and CI PostGIS image `postgis/postgis:16-3.4` (`testinfra.PostgisImage`). README §12 and compose keep `16` until P4. **SPEC-AMEND** (image tag correction; mission §7). *Rationale (tech-stack): `16` is not a published tag; the verified tag is recorded, compose change deferred.* → **Closed in P4** by GF-62 (compose tag corrected in the same change as `-js`). README §12 stays unedited (R-8).
- **R-6** — the new endpoint extends the role-matrix test (group 11).

### New in P4 (GF-52 … GF-67)

- **GF-52 — device-key transport.** The key is sent in the `X-Device-Key` request header. Not in the body (the body is strict and the key must not reach request logs with a payload); not in `Authorization` (`Bearer` is the JWT scheme and would route into the JWT middleware; a custom `Device` scheme is non-standard). Missing or repeated header → 401 `unauthorized`. *Rationale: one header, one value, no collision with the JWT path, and no key in the payload.*
- **GF-53 — device-key auth path.** The handler calls `registry.AuthenticateDeviceKey` (GF-41) and does not duplicate the parse, HMAC, or revoke logic. `ErrDeviceKeyMalformed`, `ErrDeviceKeyInvalid`, `ErrDeviceRevoked`, and `ErrDeviceNotFound` all → 401 `unauthorized` with the same body, so a revoked device is not distinguishable from a wrong key. Revoked → 401 (not `device_revoked` 409), per the roadmap. The route accepts no JWT. *Rationale: GF-41 already refuses revoked devices, so P4 only maps errors; one response body means no oracle for key state.* (Timing: `AuthenticateDeviceKey` already equalizes the unknown-id miss.)
- **GF-54 — request body rules.** `Content-Type` is not checked (device firmware varies). Body capped at 4096 bytes via `http.MaxBytesReader`; over-cap → 400 `validation_failed`. `json.Decoder.DisallowUnknownFields`; exactly one JSON value (trailing data → 400). *Rationale: a position is under 200 bytes; a 4 KiB cap blocks abuse with no effect on valid traffic; strict decoding follows GF-51.*
- **GF-55 — validation bounds (exact).**

  | Field | JSON type | Accepted range (inclusive) | Unit |
  |---|---|---|---|
  | `lat` | number | −90 … 90 | degrees WGS84 |
  | `lng` | number | −180 … 180 | degrees WGS84 |
  | `speed` | number | 0 … 300 | km/h (matches `vehicles_latest.speed_kmh`) |
  | `heading` | number | 0 … 360 | degrees clockwise from north (matches `heading_deg`) |
  | `accuracy` | number | 0 … 10000 | metres (matches `accuracy_m`) |
  | `ts` | string | RFC3339, window in GF-56 | device clock |

  All six fields are required; missing or `null` → 400. Strings are not coerced to numbers. Non-finite values cannot occur in JSON. `accuracy` upper bound is 10000, not 50: the 50 m drop is P5's D-18 filter, and P4 must not drop data that P5 counts. *Rationale: bounds catch garbage and unit mistakes; the accuracy ceiling is loose on purpose so the P5 counter sees the real distribution.*
- **GF-56 — device timestamp window.** The constitution is silent on a device-ts skew window (README §13 says only "store both"). Policy: accept `ts` when `server_now − 24 h ≤ ts ≤ server_now + 5 min`, else 400 `validation_failed`. The stale 60 s drop is on `server_ts` at flush (D-18, P5), so it does not need a device-ts rule. The 24 h lower bound lets a phone that was offline for a shift still deliver its backlog to the stream, and P5 decides staleness by `server_ts`. The 5 min upper bound rejects devices whose clocks run ahead. *Rationale: an explicit, bounded window with a named owner for each side of staleness; no silent acceptance of any `ts`.*
- **GF-57 — stamping semantics (D-15).** `msg_id` = `uuid.NewV7()`. `server_ts` = injected `Clock()` in UTC, read once per accepted request, after auth and validation succeed, and before publish. Rejected requests get no `msg_id` and no `server_ts` and publish nothing. `device_ts` is the device's `ts` converted to UTC, unchanged otherwise. *Rationale: stamping after validation keeps the stream free of rejects; a single clock read per request makes ordering reproducible in tests.*
- **GF-58 — payload and subject.** Subject `gps.raw.<fleet_id>`, where `fleet_id` is the authenticated device's `FleetID` (never from the body). Payload JSON keys, exactly: `msg_id` (UUID string), `fleet_id`, `device_id`, `lat`, `lng`, `speed_kmh`, `heading_deg`, `accuracy_m`, `device_ts`, `server_ts` (both RFC3339Nano UTC). No secret, no key, no license data. *Rationale: the consumer (P5, P6) needs the device id to resolve a vehicle per flush (D-19); the fleet comes from the principal so a device cannot publish into another fleet's subject.*
- **GF-59 — stream parameters (exact).** Name `GPS_RAW`; subjects `["gps.raw.>"]`; storage `File`; `MaxAge` 60 s; `MaxBytes` 268435456 (256 MiB); `Discard` `DiscardOld`; retention `Limits`; replicas 1; all other fields default. `CreateOrUpdateStream` at boot (idempotent). *Rationale: 2,000 msg/s × 60 s × ~350 B ≈ 42 MB at the top of the §9 burst, so 256 MiB is about six times headroom; `DiscardOld` keeps the newest data under overload, which the 60 s stale rule would discard anyway; the value is tuned in P23 (D-16).* Accepted risk: under overload above the bound, the oldest messages drop silently in P4 (no metric until P6).
- **GF-60 — pending buffer and 503.** Maximum in-flight async publishes `MaxInFlight = 1024`, a code constant (no env; FLEETOPS_* tunables are P5). The gate acquires a slot without waiting; no slot → 503 `busy` with no publish. The nats.go context is set to the same bound (`WithPublishAsyncMaxPending(1024)`) so the library's blocking stall path is never reached. A synchronous `PublishAsync` error also → 503 `busy`. A slot is released when the ack (`Ok()`) or error (`Err()`) arrives. *Rationale: a non-blocking gate gives an immediate 503 instead of holding the device connection; a fixed bound keeps memory flat under the §9 burst; `busy` is the roadmap's "busy-class" code.*
- **GF-61 — accepted risk after 204.** A publish that fails after the 204 is returned is logged with the msg id and the fleet and is not retried in P4. *Rationale: the roadmap and README §3 require 204 on accept before the ack; the durable retry path is the P6 consumer side, and P6 adds the metric.* Listed in Out of scope as a P6 follow-up.
- **GF-62 — compose changes (only these).** `nats` service: `command: ["-js"]`. `postgres` image `postgis/postgis:16` → `postgis/postgis:16-3.4` (GF-12 closed here). No volume, port, or env change. JetStream store uses the image default directory (ephemeral in dev; no stream data is kept across `docker compose down`, which matches the 60 s retention). *Rationale: `-js` is the only change the roadmap names; the tag fix is the GF-12 requirement that names P4, so it ships with the file it touches.*
- **GF-63 — `NATS_URL`.** An existing §12 name, not a new env. Read by `config.Load()` into `NATSURL`, required, error names the variable and never prints the value. Local-run line in CLAUDE.md gains `NATS_URL=nats://127.0.0.1:10004`. *Rationale: the API cannot publish without it; keeping the read in `config` keeps the R-9 "config from env" rule in one place.*
- **GF-64 — role matrix for the device route (R-6).** The row set is in group 11: JWT of any role → 401 on `/ingest/position`; no auth → 401; valid device key → 204. The JWT matrix is not weakened: JWT middleware is not applied to this route, and no JWT role gains access. *Rationale: a device key is a different principal; the matrix records that the route refuses every JWT.*
- **GF-65 — test wiring.** One NATS JetStream container per test package, shared through `sync.Once` (same pattern as GF-29 for PostGIS, GF-46); per test, `GPS_RAW` is deleted and re-ensured for a clean stream; container removed at `TestMain` exit; OS-assigned host ports (GF-13). `FetchSubject` reads through an ordered consumer, so assertions see the full stream in order. No NATS mocks (tech-stack §9). *Rationale: a fresh container per test is slow; a per-test stream reset keeps counts exact.*
- **GF-66 — UUID library.** `github.com/google/uuid` (already an indirect dependency, v1.6.0) becomes direct; `uuid.NewV7()` produces `msg_id`. Pinned in `go.mod`. *Rationale: an existing, widely used library avoids a hand-rolled UUIDv7 generator; the dependency was already in the graph.*
- **GF-67 — P4 boundaries.** P4 performs no DB write on the ingest path, so `devices.last_seen_at` is not touched in P4 (P5 decides whether the flush touches it). P4 does not call `CheckDeviceActive`; `AuthenticateDeviceKey` already refuses revoked devices (GF-41). Not built in P4: buffer, flush, drops, consumer, partitions, `vehicles_latest`, metrics. *Rationale: keeps the hot path a pure accept-and-publish; the write-side decisions belong to P5 with the batch writer.*

## Validation

Runnable on this host. Host tools: `/root/.local/go/bin/go`, `/root/.local/bin/golangci-lint`, `/root/go/bin/migrate`, wired through `deploy/.local/tools.mk`. Docker is available for testcontainers. **Do not run `docker compose up` in the validation run**: it is used only in the compose assertion below, and it is a text check. Secrets are generated per shell and never committed.

1. **Gates.** `make fmt-check` → exit 0, no files listed. `make lint` → exit 0. `make test` (`-race -count=1`) → exit 0 for all packages. **Pass:** all three exit 0.
2. **The four integration cases (named tests).** `go test ./services/api -run 'TestIngestPosition' -v` lists and passes `TestIngestPositionValid204`, `TestIngestPositionInvalidPublishesNothing`, `TestIngestPositionRevokedKey401`, `TestIngestPositionFullBuffer503`. Each can run alone: `go test ./services/api -run '^TestIngestPositionValid204$' -v`, and likewise for the other three. **Pass:** four PASS lines; the stream-message count and the empty-stream check are asserted inside each test.
3. **Compose assertion (no `docker compose up`).** `awk '/^  nats:/{f=1} /^  [a-z]/&&!/^  nats:/{f=0} f' deploy/docker-compose.yml | grep -F '"-js"'` → one line. `grep -F 'postgis/postgis:16-3.4' deploy/docker-compose.yml` → one line. **Pass:** both print one match.
4. **Endpoint smoke over real HTTP.** The four cases in step 2 run `NewServer(Deps)` on an `httptest` listener with real HTTP clients. **Pass:** the four status codes 204, 400, 401, 503 observed in the test output.
5. **Unit: validation and stamping.** `go test ./internal/ingest -run 'TestValidatePosition|TestStamp|TestGate' -v`. **Pass:** all listed tests PASS, including the bound edges in GF-55 and the window edges in GF-56.
6. **Role matrix (R-6).** `go test ./services/api -run RoleMatrix -v` → the ingest rows (JWT columns 401, none 401, device key 204). **Pass:** every new cell listed and matching.
7. **Config (GF-63).** `go test ./internal/config -v` → `NATS_URL` missing refused; error text contains no value. **Pass:** exit 0.
8. **Scope fence and schema untouched.** `git diff --name-only main...HEAD -- db/ README.md deploy/PORTS.md` → empty output. `git diff --name-only main...HEAD` contains no `.env`. **Pass:** empty output for the first; no `.env` path for the second.
9. **No secret leakage (R-9).** `grep -R "fo_dev_[A-Za-z0-9_-]\{43\}" --include=*.go --include=*.sql --include=*.md .` → only test code that builds keys with `devicekey.Generate`; no literal key string. **Pass:** no literal 43-character secret in any file.
10. **Salt and JWT unchanged.** `go test ./internal/config -run 'Test(DeviceKeySalt|JWTSecret)' -v` → PASS (P2/P3 cases unchanged). **Pass:** exit 0.

## Testing depth

Roadmap P4 + tech-stack §9.

- **Unit (stdlib `testing`, no infra):** `Validate` bounds and window (GF-55, GF-56); `Stamp` (UUIDv7, `server_ts`, subject, payload keys); `Gate` slot logic; apierr table; config `NATS_URL` rules.
- **Integration (testcontainers PostGIS and NATS with JetStream; real HTTP; no mocks of infra):** the four named cases (group 10); `TestEnsureStreamIdempotent`; `TestGatePublishReleasesSlotOnAck`; `TestNATSHelperFetchesPublished`; role-matrix ingest rows (group 11).
- **Property tests:** none in P4. The roadmap names none for P4; the pairing invariant (P3) and the flush and batch properties (P5) cover their own areas.
- **Migrations:** none. `db/` is untouched; the existing round-trip test covers the schema.
- **Race:** `go test -race` runs everywhere via `make test`. The CI race gate for ingest is the first P4 CI requirement, because `internal/ingest` is an ingest package (tech-stack §9).
- **No mocks** of Postgres, NATS, or HTTP. Clock is injected only through `func() time.Time`.

## SPEC-AMEND repeat (R-4)

- **D-16** — JetStream stream `GPS_RAW` and the NATS container `-js` (mission §7: P4, additive infra; `mission.md` row exists). Stream parameters in GF-59 are the exact values.
- **GF-12** — Postgres image correction `postgis/postgis:16-3.4` in compose, closed in this phase (mission §7: P1 row exists; the compose part is closed here).
- No new mission §7 row is needed: the device-key HTTP path is covered by D-29 (mission §7: P3) and the endpoint `POST /ingest/position` is already in tech-stack §7.1 (P4).
- `apierr` code `busy` (GF-60) and the `NATS_URL` config requirement (GF-63) are additive and recorded here only.

## Out of scope

- P5+ behavior: the buffer and its 1 s / 500 flush; the `accuracy > 50 m` drop and the 60 s stale drop; batch upserts; `vehicles_latest`; `positions` writes; vehicle resolution per flush; heading smoothing; the consumer and its ack/NAK/MaxDeliver; partitions and retention; presence; fanout; WS hub; map; jobs; geofences; alerts; reports; OSRM; simulator; MinIO; metrics and OTel (P6, P22).
- `devices.last_seen_at` writes (GF-67).
- `FLEETOPS_*` tunables and any new env name (D-22 is P5).
- Durable retry of a publish that fails after 204 (GF-61, P6 consumer side).
- Compose changes beyond `-js` and the GF-12 tag (GF-62); volume or port changes for NATS.
- README and `deploy/PORTS.md` edits (R-8, no port change).
- Schema or migration changes (no migration in P4).
- Un-revoke, key grace periods, and the device-key rotation UI (P14).
