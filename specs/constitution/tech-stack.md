# Tech stack — FleetOps

Status: approved constitution, 2026-10-25. Pins from README §4 are used exactly. Additions never replace a pin. Decision IDs D-01…D-41 (plan) and GF-1…GF-3 (self-gate additions, see mission §7) are defined in §11.

## 1. Pinned stack (README §4)

| Layer | Choice | Why | Rejected alternatives |
|---|---|---|---|
| API / ingest / WS hub | Go + Echo | Goroutines fit per-connection fanout; single-binary deploys; strong `time`/`context` tooling for batching | Node (fine, but Go holds 10k WS conns more predictably); Java Spring (heavier than needed) |
| Message backbone | NATS (`nats:2`) | Lightweight pub/sub, subject wildcards, optional JetStream; runs in one container | Kafka (operational overkill); Redis Streams (workable; NATS subjects map better to fleet topics) |
| OLTP + spatial | PostgreSQL 16 + PostGIS (`postgis/postgis:16`) | GIST-indexed geometry, `ST_Covers`/`ST_DWithin`, mature partitioning | MongoDB geo (weaker queries); Elasticsearch (not source of truth) |
| Fanout / presence | Redis 7 (`redis:7`) | Pub/sub for position batches, TTL keys for presence | NATS-only (viable; Redis simpler for room semantics) |
| Frontend | React 18 + Vite + MapLibre GL + TanStack Query | Largest map ecosystem; Query handles polling and revalidation | Leaflet (struggles with thousands of animated markers) |
| Routing | OSRM (`osrm/osrm-backend`, small region extract) | Free, no API key, containerized; `/route` (+ `/trip` in backlog) | Google Directions (cost and ToS on stored results) |
| Driver app | React PWA, same codebase, separate Vite build | One language, installable, offline via service worker + IndexedDB queue | React Native (extra toolchain for zero MVP benefit) |

Rule: the driver PWA is `apps/driver-pwa`, a separate build. It is not a native app.

## 2. Added libraries per layer

Each line is an addition, not a substitute. Exact versions are pinned in the phase that introduces them (D-13).

| Layer | Library | Reason | Decision |
|---|---|---|---|
| Go | golang-migrate | Schema migrations (README §10 Phase 0) | D-13 |
| Go | golangci-lint | Lint gate in CI | D-13 |
| Go | argon2id implementation (x/crypto) | Password hashing | D-04 |
| Go | JWT library, HS256 only | Access tokens; fixed algorithm | D-03 |
| Go | pgregory.net/rapid | Property tests | D-37 |
| Go | testcontainers-go (PostGIS, Redis, NATS, MinIO modules) | Integration tests, no mocks of infra | D-37 |
| Go | Prometheus client | `/metrics` | D-36 |
| Go | OpenTelemetry SDK, OTLP HTTP exporter | Assign-flow spans; no-op when endpoint unset | D-36 |
| Go | k6 (separate, JS scripts) | Load tests in `tools/loadtest/k6/` | D-37 |
| Frontend | React Router | Routing | D-38 |
| Frontend | `idb` | IndexedDB queue | D-38 |
| Frontend | `vite-plugin-pwa` (Workbox) | Service worker and manifest | D-38 |
| Frontend | MapLibre GeoJSON circle layers with native clustering | Map markers (not DOM markers) | D-38 |
| Frontend tests | Vitest, Testing Library, `fake-indexeddb` | Unit and component tests | D-37 |
| E2E | Playwright (one spec) | Happy path only | D-37 |

## 3. Repo layout

README §11 plus additions (D-39):

| Path | Purpose |
|---|---|
| `apps/web/` | Dispatcher UI (README §11) |
| `apps/driver-pwa/` | Driver app (README §11) |
| `services/api/`, `services/ingest/`, `services/ws-hub/` | Go services (README §11) |
| `packages/shared-types/` | OpenAPI-generated TS types (README §11) |
| `tools/simulator/`, `tools/seed/` | Simulator; first-fleet seed (D-05, D-12) |
| `tools/loadtest/k6/` | k6 scripts (D-37, P23) |
| `db/migrations/` | Shared migrations for api and ingest (D-13) |
| `deploy/docker-compose.yml`, `deploy/PORTS.md` | Compose and port map (README §11, CLAUDE.md) |
| `deploy/grafana/` | Dashboards as code (README §11, D-36) |
| `deploy/osrm/` | `prepare.sh`; data in `deploy/osrm/data/` (gitignored) (D-10) |
| `deploy/.local/` | Local generated files, e.g. `devices.json` (gitignored) (D-12) |
| `e2e/` | Playwright spec (D-37) |
| `docs/` | ADRs, API notes, `docs/demo.md` (README §11, D-39) |
| `specs/constitution/`, `specs/features/` | Constitution (this folder) and per-phase feature specs (D-40) |
| `Makefile` | Targets `migrate`, `test`, `lint`, `seed`, `seed-demo`; `make migrate` never runs at app boot (D-13) |

Gitignored: `deploy/.local/`, `deploy/osrm/data/`, `.env`, `.env.*` (CLAUDE.md, AGENTS.md).

## 4. Local infrastructure & ports

Compose file: `deploy/docker-compose.yml`. Host ports in 10000–10019 only (`deploy/PORTS.md`). Infrastructure binds `127.0.0.1` only.

| Service | Container | Host | Added in | Note |
|---|---|---|---|---|
| postgres | 5432 | 10002 | P0 | Active |
| redis | 6379 | 10003 | P0 | Active |
| nats | 4222 | 10004 | P0 | `-js` added in P4 (D-16) |
| osrm | 5000 | 10005 | P0 | Data not bundled (D-10) |
| minio | 9000 / 9001 | 10006 / 10007 | P12 | Volume `minio_data` (D-07) |
| api | 8080 | 10010 | local `go run` | Reserved (PORTS.md) |
| ws-hub | 8090 | 10011 | local `go run` | Reserved (PORTS.md) |
| web | 5173 | 10012 | local Vite dev | Reserved (PORTS.md); `/stream` proxied to 10011 |
| driver-pwa | 5174 | 10013 | local Vite dev | Reserved (PORTS.md) |
| ingest metrics | `/metrics` | 10008 | P22 | Local `go run`, not compose (D-36) |
| prometheus | 9090 | 10014 | P22 | Compose (D-36) |
| grafana | 3000 | 10015 | P22 | Compose (D-36) |

Unassigned after this plan: 10000, 10001, 10009, 10016–10019. PORTS.md is updated in the branch that binds each port (D-36).

## 5. Environment variables

Names only. Values come from the environment or `deploy/.env` (gitignored). Never commit values (CLAUDE.md, AGENTS.md).

**Required (README §12, CLAUDE.md):**

- `DATABASE_URL`, `NATS_URL`, `REDIS_URL`, `OSRM_URL`
- `MINIO_ENDPOINT`, `MINIO_KEY`, `MINIO_SECRET`
- `JWT_SECRET` (startup refuses < 32 bytes, D-03)
- `DEVICE_KEY_SALT` (device-key HMAC, license hash, D-29, D-31)

**Compose-only:**

- `POSTGRES_PASSWORD` (local-dev default applies if unset; CLAUDE.md)

**Optional, standard:**

- `OTEL_EXPORTER_OTLP_ENDPOINT` (unset = no-op, D-36)

**Optional, additive (SPEC-AMEND, D-22; defaults in brackets):**

- `FLEETOPS_FLUSH_INTERVAL` [1s]
- `FLEETOPS_FLUSH_MAX` [500]
- `FLEETOPS_WS_ROOM_CAP` [2000]
- `FLEETOPS_PRESENCE_TTL` [120s]
- `FLEETOPS_ACCURACY_MAX_M` [50]

Rule: any env name change updates CLAUDE.md in the same branch (D-40).

## 6. Data model

### 6.1 README §5 tables (verbatim intent)

- `fleets(id, name)`
- `vehicles(id, fleet_id, plate, model, status, paired_device_id)`
- `drivers(id, fleet_id, name, license_no_hash, phone, shift_start, shift_end)`
- `devices(id, fleet_id, kind [pwa|tracker], api_key_hash, last_seen_at)`
- `jobs`, `job_events(id, job_id, from_state, to_state, actor_id, at)` — append-only
- `geofences(id, fleet_id, name, kind [polygon|circle], geom geography, schedule jsonb)`
- `alerts(id, fleet_id, kind, severity, payload jsonb, raised_at, acked_by)`
- `vehicles_latest` — DDL unchanged from README §5 (one row per vehicle, GIST on `geom`, index on `fleet_id`)
- `positions_YYYY_MM` — monthly partitions, append-only, 12-month retention

`jobs` columns per README §5 (`id, fleet_id, state, vehicle_id, driver_id, pickup, dropoff, window_start, window_end, eta_minutes, proof_photo, created_at, delivered_at`), index `(fleet_id, state)`.

`devices.api_key_hash` holds `HMAC-SHA256(DEVICE_KEY_SALT, secret)` (D-29). `drivers.license_no_hash` holds the D-31 hash.

### 6.2 `positions` (derived, no D-ID beyond those cited)

Parent table `positions`, `PARTITION BY RANGE (server_ts)` (D-14). Children `positions_YYYY_MM`. Columns: `msg_id` (UUIDv7, D-15), `fleet_id`, `vehicle_id`, `geom geography(Point,4326)`, `speed_kmh`, `heading_deg`, `accuracy_m`, `device_ts`, `server_ts`. Unique index `(server_ts, msg_id)` per partition; inserts `ON CONFLICT DO NOTHING` (D-15). Source: README §2.1.3 fields `lat, lng, speed, heading, accuracy, ts` mapped to the `vehicles_latest` column shape.

### 6.3 Additive schema (SPEC-AMEND)

| Object | Change | Decision |
|---|---|---|
| `users(id, fleet_id, email CITEXT UNIQUE, password_hash, role, driver_id NULL, disabled_at)` | New table | D-01 |
| `refresh_tokens` | New table; SHA-256 of opaque 32-byte token; rotation family; 7-day TTL | D-02 |
| `admin_audit(id, fleet_id, actor_id, action, target, at)` | New append-only table | GF-2 |
| `job_transitions` | New reference table mirroring `internal/jobs/transitions.go`; read by `BEFORE UPDATE` trigger | D-23 |
| `jobs`: `notes`, `size_class`, `weight_kg`, `delivery_geom geography`, `failure_reason` | New columns | D-26 |
| `vehicle_pairings(id, fleet_id, vehicle_id, driver_id NULL, device_id, started_at, ended_at)` | New table; partial unique on current rows per vehicle and per device | D-28 |
| `devices.revoked_at` | New column | D-29 |
| `alert_rules(id, fleet_id, kind, params jsonb, enabled, updated_at)` | New table; three kinds seeded per fleet | D-08 |
| `alerts.acked_at` | New column; `acked_by` / `acked_at` one-time `NULL → value` | D-09 |
| `geofences`: `radius_m NULL`, `bbox geometry`, `dwell_min NULL` | New columns; `schedule` shape `{days:[1..7], from:"HH:MM", to:"HH:MM", tz}` | D-26, D-33 |
| `geofence_events` | New append-only table | D-33 |
| `daily_driver_stats(fleet_id, driver_id, day, jobs_completed, on_time_count, on_time_denom, distance_m, idle_s)` | New rollup table | D-32 |
| `GPS_RAW` JetStream stream | Not a table; see §8 | D-16 |

Append-only enforcement (trigger): `job_events` (P10), `alerts` except the ack transition (P19), `geofence_events` (P18), `admin_audit` (P2) (README §8, D-09, D-24, GF-2). `positions` is append-only by code path only (inserts; retention via DETACH + DROP, D-14); no UPDATE/DELETE trigger is planned.

Partition lifecycle: ensure current + next 2 months at ingest boot and daily; no DEFAULT partition; daily retention detaches and drops children whose upper bound ≤ start of (now − 12 months) (D-14).

## 7. API surface

### 7.1 README §6 (verbatim intent)

| Method | Path | Auth | Phase |
|---|---|---|---|
| POST | `/auth/login` | none | P2 |
| GET/POST/PATCH | `/vehicles`, `/drivers` | JWT | P3 |
| POST | `/ingest/position` | device key | P4 |
| GET | `/map/vehicles?bbox=` | JWT | P8 |
| GET/POST | `/jobs` (filters: state, driver, day) | JWT | P10 |
| POST | `/jobs/:id/assign` | JWT | P10 |
| POST | `/driver/jobs/:id/accept`, `/start`, `/pickup`, `/deliver` (multipart) | JWT driver | P11, P12 |
| GET | `/driver/jobs/today` | JWT driver | P11 |
| GET/POST/DELETE | `/geofences` (GeoJSON) | JWT | P17 |
| GET | `/alerts?since=`; PATCH `/alerts/:id/ack` | JWT | P19 |
| GET | `/reports/daily?date=&driver=`; `?format=csv` | JWT | P21 |
| WS | `/stream?fleet=<id>` | first-message auth (D-21) | P7 |

### 7.2 Additive endpoints (SPEC-AMEND)

| Endpoint | Purpose | Phase | Decision |
|---|---|---|---|
| `POST /auth/refresh`, `POST /auth/logout` | Refresh rotation; logout | P2 | D-02 |
| `GET/POST/PATCH /users` | Admin user management | P2 | D-06 |
| `GET/POST /vehicles/:id/pairings` | Pairing history | P3 | D-28 |
| `POST /devices` (issue), `POST /devices/:id/rotate`, `POST /devices/:id/revoke` | Device keys, admin only | P3 | D-29, D-30, GF-3 |
| `POST /jobs/:id/cancel` | Dispatcher/admin cancel | P10 | D-23 |
| `POST /driver/jobs/:id/fail` | Driver fail, `reason` required | P11 | D-23 |
| `GET/PATCH /alert-rules` (admin, manager) | Rule parameters | P19 | D-08 |

Every endpoint above has a phase; the role matrix (§8 of README) is tested cell by cell in P3 and extended in P11 (roadmap rule R-6).

### 7.3 WS protocol

- Auth: first message `{"type":"auth","token":…}` within 5 s, else close 4401 (D-21). Token `fleet_id` must match `?fleet=`.
- Events (README §6): `position.batch` (≤ 200 points), `job.updated`, `alert.raised`, `presence.changed`.
- Drivers may connect and receive only `job.updated` for own jobs (D-21, README §8).
- Room cap 2,000 (`FLEETOPS_WS_ROOM_CAP`); new connection at cap → 503 (D-21).
- Ping every 20 s; evict after 60 s without pong (D-21).
- Close at token expiry (D-03, D-21).

## 8. Messaging

### 8.1 Transport split (D-17)

| Path | Transport | Subject / channel | Producer → consumer |
|---|---|---|---|
| Position ingest | NATS JetStream | `gps.raw.<fleet_id>` (stream `GPS_RAW`, subjects `gps.raw.>`) | gateway → ingest |
| Position fanout | Redis pub/sub | `positions.batch.<fleet_id>` | ingest → ws-hub |
| Presence | Redis | key `device:<id>`; channel `presence.changed.<fleet_id>` | ingest → ws-hub |
| Job events | NATS core | `jobs.events.<fleet_id>` | API → ws-hub |
| Alerts | NATS core | `alerts.<fleet_id>` | alert evaluator → ws-hub |

The README §7 row putting `positions.batch.<fleet_id>` on NATS is superseded (D-17). Hub subscribes to NATS for jobs and alerts and to Redis for positions and presence. `Fanout` interface with a Redis implementation keeps the seam (D-41).

### 8.2 JetStream config (D-16)

- Stream `GPS_RAW`: file storage; `MaxAge` 60 s (matches stale rule, D-18); `MaxBytes` bounded (value set in P5, tuned in P23).
- Durable consumer: explicit ack after DB commit (D-16).
- On DB failure: NAK with delay. After `MaxDeliver`: terminate and count (D-16, D-18).
- Gateway publishes async; returns 503 when the pending buffer is full (backpressure); returns 204 before publish completes on accept (README §3, D-15).

### 8.3 Non-JetStream subjects (GF-4)

`jobs.events.*` and `alerts.*` are core NATS. Durable record is `job_events` and `alerts`; WS delivery is best-effort, and the driver falls back to polling (README §3).

## 9. Testing preferences

| Layer | Tools | Depth rule | Source |
|---|---|---|---|
| Go unit | stdlib `testing` | Business logic, parsers, state machines | D-37 |
| Go property | `pgregory.net/rapid` | Signature problems only (see list below) | D-37 |
| Go integration | testcontainers: PostGIS, Redis, NATS (JetStream), MinIO | No mocks of infra; real DB, real NATS | D-37, README §9 |
| Role matrix | integration table test | 4 roles × every resource × verbs; new endpoints extend it | D-37, README §8 |
| Race | `go test -race` | Ingest and hub packages in CI | D-37, README §13 |
| Frontend unit | Vitest | Status derivation, timers, reducers, state↔button mapping | D-37, D-38 |
| Frontend component | Vitest + Testing Library | Details card, forms, report page | D-37 |
| Offline | Vitest + `fake-indexeddb` | Queue ordering, replay, conflicts | D-37 |
| E2E | Playwright, exactly one spec `e2e/happy-path.spec.ts` | Happy path (P16); no second E2E in MVP | D-37 |
| Load | k6 in `tools/loadtest/k6/` | On demand and nightly, not per PR; P23 only | D-37 |

**Required property tests** (plan self-gate): batch upsert (permutation and replay), flush semantics, idempotent transitions, debounce, pairing invariant, silence episodes, chunking.

**CI gates (P1):** fmt, lint, test, `-race` on ingest and hub. `make migrate` up → down → up round trip on PostGIS. Playwright job brings compose up (P16). k6 excluded from PR CI.

## 10. Deploy

- MVP is local compose only (D-41). Cloud profile (README §15) is backlog.
- Migrations run through `make migrate` as an explicit step, never at app boot (D-13).
- `make seed` creates the first fleet and admin only when `fleets` is empty; no default credentials (D-05).
- OSRM: operator downloads a regional PBF into `deploy/osrm/data/`; `deploy/osrm/prepare.sh` runs extract/partition/customize in a one-shot container into `osrm_data`; not run in CI (D-10).
- MinIO bucket `proofs` is created idempotently at API boot (D-07).

## 11. Decision log

Plan decisions D-01…D-41 are copied from the approved plan. Gap-fills GF-1…GF-4 are self-gate additions that the plan did not number.

| ID | Decision | Rationale |
|---|---|---|
| D-01 | Users table `users(id, fleet_id, email CITEXT UNIQUE, password_hash, role, driver_id NULL, disabled_at)`; login by email only. | §5 has no users table; §2.1.2 needs one. Global email uniqueness lets login skip fleet selection. **SPEC-AMEND** (additive table). |
| D-02 | Refresh tokens: opaque 32-byte random, stored as SHA-256 in `refresh_tokens`; rotated on every use; reuse of a rotated token revokes all of that user's tokens; 7-day TTL; httpOnly, Secure, SameSite=Strict cookie on path `/auth`. | §8 requires refresh tokens; rotation + reuse detection limits theft; Postgres storage keeps revocation durable. **SPEC-AMEND** (additive table and endpoints). |
| D-03 | Access JWT: HS256 signed with `JWT_SECRET` (startup refuses < 32 bytes); claims `sub, fleet_id, role, exp` (15 min). WS closes at `exp`. | §8 JWT 15 min; fixed algorithm avoids alg confusion. |
| D-04 | Password hashing argon2id (64 MiB, t=3, p=2); minimum 12 characters; in-memory login throttle 5 failures per 15 min per (email, IP). | Modern KDF; spec silent on policy; single-instance throttle is enough for MVP and is noted as such. |
| D-05 | First fleet: `make seed` runs `tools/seed`, which creates one fleet and one admin only when `fleets` is empty; password read from a no-echo TTY prompt or stdin; no default credentials; `--timezone` (default UTC). | §12 requires `make seed`; no committed credentials. |
| D-06 | `GET/POST/PATCH /users` (admin only) added to §6. **SPEC-AMEND.** | §1 admin needs user management; §6 omits it. |
| D-07 | MinIO added to compose in Phase 12 (not Phase 0), ports 10006/10007, bound to 127.0.0.1; bucket created idempotently at API boot; cloud photo storage deferred. | Photo proof is MVP (§2.1.5), so MinIO is needed in MVP; Phase 0 deliberately excluded it; PORTS.md already reserves the ports. |
| D-08 | `alert_rules(id, fleet_id, kind, params jsonb, enabled, updated_at)`; three fixed kinds seeded per fleet with spec defaults (silent 10 min, ETA slip 15 min, schedule window); `GET/PATCH /alert-rules` (admin, manager). **SPEC-AMEND.** | §5 has `alerts` but no rules table; §2.1.7 says "configurable" but lists fixed kinds, so parameters are configurable and kinds are not. |
| D-09 | `alerts` is append-only except one trigger-enforced `NULL → value` transition on `acked_by` / `acked_at`; DELETE forbidden. | Keeps §5 column shape and the §8 audit rule; ack is the one permitted state change. **SPEC-AMEND** (additive column). |
| D-10 | OSRM provenance: operator downloads a regional OSM PBF (Geofabrik extract, 200–500 MB output target) into `deploy/osrm/data/` (gitignored); `deploy/osrm/prepare.sh` runs extract/partition/customize in a one-shot `osrm/osrm-backend` container into `osrm_data` as `region.osrm`; not run in CI; cloud uses the public demo server, where ETA rules degrade. | §12 and compose skeleton say data is not bundled; preprocessing is heavy and must stay out of CI. |
| D-11 | Email stub: `notify.Notifier` interface; `LogNotifier` logs event type and recipient user id (never the address) for critical alerts to managers. | §2.1.7 allows a stub; logging addresses would leak PII. |
| D-12 | Simulator stays in Phase 8 (§10). Reads device keys from `deploy/.local/devices.json` (gitignored), written by `tools/seed --demo-devices`. Straight-line legs, no OSRM dependency. | §10 places it in phase 2; keeping it OSRM-free keeps phase small. |
| D-13 | CI, golangci-lint, golang-migrate, Makefile land in Phase 1 (first implementation phase). Migrations in `db/migrations/` (repo root, shared by api and ingest). `make migrate` is an explicit step, never run at app boot. Tool versions pinned. | §10 P0 lists them; the Phase 0 scaffold excluded them; shared migrations avoid two schema owners. |
| D-14 | `positions` parent `PARTITION BY RANGE (server_ts)`; children `positions_YYYY_MM`; ensure current + next 2 months at ingest boot and daily; no DEFAULT partition; daily retention detaches and drops children whose upper bound ≤ start of (now − 12 months); insert into missing partition → metric + NAK. | §5 monthly partitions, 12-month retention; DEFAULT partition blocks future partition creation; `server_ts` is the ordering key (§13). |
| D-15 | Gateway assigns `msg_id` (UUIDv7) and `server_ts`, both carried in the NATS payload. Unique index `(server_ts, msg_id)` on each partition; inserts `ON CONFLICT DO NOTHING`. | Makes at-least-once redelivery safe and gives the property test a real invariant. |
| D-16 | JetStream stream `GPS_RAW` (subjects `gps.raw.>`, file storage, `MaxAge` 60 s to match the stale rule, bounded `MaxBytes`); durable consumer with explicit ack after commit; NAK with delay on failure; `MaxDeliver` then terminate + metric. Gateway uses async publish and returns 503 when the pending buffer is full (backpressure). NATS container gets `-js`. **SPEC-AMEND.** | §7 "fire-and-forget" + "at-least-once + idempotent upserts" (§14) need a durable stream; async publish keeps 204 immediate. |
| D-17 | Transport split: Redis pub/sub carries `position.batch` and `presence.changed` (per §3, §4, §12). NATS carries `jobs.events` and `alerts` (per §7). The §7 row putting `positions.batch.<fleet_id>` on NATS is superseded by §3 and §4. **SPEC-AMEND.** | §3, §4 and §7 disagree; §3 and §4 are the architecture and stack, so they win. |
| D-18 | Flush: 1 s after the first buffered message or 500 messages, whichever comes first; flush on shutdown. Dropped before buffering: `accuracy > 50 m`; dropped at flush: `server_ts` older than 60 s (ack + count). | §3, §7, §13 numbers; drop points chosen so stale messages do not hold buffer slots. |
| D-19 | `vehicles_latest` upsert guarded by `EXCLUDED.server_ts > vehicles_latest.server_ts`; device → vehicle resolved per flush through `vehicles.paired_device_id`; unpaired devices acked and counted, not stored. | §13 says order by `server_ts`; flush-time resolution avoids a stale cache. |
| D-20 | Presence: Redis `device:<id>` = `online` with `EX 120`, refreshed per message; `SET … GET` detects offline → online and publishes `presence.changed`; offline transitions come from an ingest sweeper every 15 s over `vehicles_latest` (`server_ts < now − 120 s`) with in-memory last-emitted state. Presence is WS-only, not persisted. | §7 presence TTL; DB remains the source of truth for offline. |
| D-21 | WS hub: auth by first message `{"type":"auth","token":…}` within 5 s, else close 4401; token `fleet_id` must match `?fleet=`; drivers may connect but receive only `job.updated` for own jobs; room cap 2,000 (new connection → 503 at cap); ping 20 s, evict after 60 s without pong; close at token expiry. | Keeps tokens out of URLs and logs; §7 cap and eviction; §8 driver scope. |
| D-22 | Tunables are optional env vars with defaults, prefix `FLEETOPS_`: `FLUSH_INTERVAL=1s`, `FLUSH_MAX=500`, `WS_ROOM_CAP=2000`, `PRESENCE_TTL=120s`, `ACCURACY_MAX_M=50`. The §12 names stay required. **SPEC-AMEND** (additive). | §7 and §13 values should be tunable for Phase 23 load tuning without code edits; §12 list stays intact. |
| D-23 | Job transitions: `created→assigned` (assign), `assigned→assigned` (reassign), `assigned→accepted`, `accepted→en_route`, `en_route→picked_up`, `picked_up→delivered`, `{accepted,en_route,picked_up}→failed`, `{created,assigned,accepted}→cancelled`. Actors: dispatcher/admin assign and cancel; driver accept, start, pickup, deliver, fail on own jobs. Single source is `internal/jobs/transitions.go`; `job_transitions` mirrors it for the trigger. | §2.1.5 lists the states but not every edge; reassign and cancel are needed by dispatch. **SPEC-AMEND** (additive table and endpoints). |
| D-24 | Idempotent transitions: `UPDATE … WHERE id=$1 AND state=$from`; zero rows and current state == target → 200 with no new event; otherwise 409. Only the trigger writes `job_events`; actor passed by `SET LOCAL app.actor_id`. | §14 idempotency talking point; one writer for audit rows. |
| D-25 | Delivery proof: `deliver` requires multipart photo (JPEG/PNG/WebP, ≤ 5 MB) plus lat/lng; object key `proofs/<fleet_id>/<job_id>/<uuid>.<ext>`; EXIF is not stripped in MVP (accepted risk, listed in backlog). | §2.1.5 photo + coordinates; re-encoding EXIF stripping is extra scope. |
| D-26 | Additive job columns: `notes`, `size_class`, `weight_kg`, `delivery_geom geography`, `failure_reason`, `dwell_min` on geofences. Additive vehicle column: none. **SPEC-AMEND** (additive, schema only). | §2.1.5 asks for notes, size/weight, and coordinates at delivery; §5 omits them. |
| D-27 | `vehicles.status` = registry status `{active, maintenance, retired}`. Map display status `{idle, en_route, offline}` is derived server-side: `offline` if `server_ts` older than 120 s; `en_route` if the vehicle's job is `en_route` or `picked_up`; otherwise `idle`. | §2.1.1 and §2.1.4 both say "status"; separating them removes the clash. |
| D-28 | `vehicle_pairings(id, fleet_id, vehicle_id, driver_id NULL, device_id, started_at, ended_at)`; partial unique indexes on current rows (`ended_at IS NULL`) per vehicle and per device; `vehicles.paired_device_id` mirrors the current pairing in the same transaction. **SPEC-AMEND** (additive). | §2.1.1 needs pairing history; §5 has only the current pointer. |
| D-29 | Device keys: `fo_dev_<device_id>_<secret>`, secret = 32 random bytes base64url; stored as `HMAC-SHA256(DEVICE_KEY_SALT, secret)`; constant-time compare; rotate replaces the hash immediately (no grace); revoke sets `devices.revoked_at` (additive); key shown once. | §8 hashed, rotatable, revocable; the device id in the key avoids a scan. **SPEC-AMEND** (additive column and endpoints). |
| D-30 | PWA identity: the phone is a `devices` row with `kind=pwa` bound to a driver; the admin issues the key on a pairing screen, shown once, and the driver enters it on first run. The PWA posts positions with that key. | §2.1.3 has the PWA posting positions; §8 gives devices API keys only. **SPEC-AMEND** (device key endpoints). |
| D-31 | License hash: `HMAC-SHA256(DEVICE_KEY_SALT, "license:" + fleet_id + ":" + normalized_no)`. | Deterministic (needed for per-fleet uniqueness), domain-separated, and adds no new secret. |
| D-32 | Reports: `daily_driver_stats(fleet_id, driver_id, day, jobs_completed, on_time_count, on_time_denom, distance_m, idle_s)` refreshed every 15 min for today and once after midnight for yesterday (fleet timezone). Distance = sum of haversine between consecutive samples with gap ≤ 5 min. Idle = time between consecutive samples with speed < 1 km/h. On-time = delivered_at ≤ window_end over jobs that have a window. Driver attribution through `vehicle_pairings` active at sample time. **SPEC-AMEND** (table). | §9 API p95 ≤ 300 ms rules out on-demand scans over 8.6M rows/day. |
| D-33 | Geofences: `geofences(id, fleet_id, name, kind, geom geography, radius_m NULL, bbox geometry, dwell_min NULL, schedule jsonb)`; schedule `{days:[1..7], from:"HH:MM", to:"HH:MM", tz}`; `geofence_events` append-only; evaluation runs in ingest after flush for moved vehicles; prefilter `bbox && point`, then `ST_Covers` (polygon) or `ST_DWithin` (circle); debounce 30 s per pair; dwell evaluated on next position after `dwell_min` (no sweeper; accepted). **SPEC-AMEND.** | §7 bounding-box prefilter + 30 s debounce; §13 cost control. |
| D-34 | Alert semantics: `device_silent` = last `server_ts` older than 10 min → one alert per silence episode, re-armed when the device reports again (warning); `geofence_exit_outside_schedule` = exit event outside the schedule window (critical); `eta_slip` = projected arrival > `window_end` + 15 min (warning), evaluated every 5 min per active job and on assign. | §2.1.7 thresholds; episode semantics stop alert storms. |
| D-35 | ETA: OSRM `/route` from the vehicle's latest position to the job's next target (pickup if state ∈ {assigned, accepted}, else dropoff); result to `jobs.eta_minutes`; at most one call per job per 5 min. | §3 `/route`; rate limit protects the public demo server fallback. |
| D-36 | Observability: Prometheus `/metrics` on api, ingest (host 10008, local), hub; Prometheus host 10014 → 9090 and Grafana host 10015 → 3000 in compose (Phase 22); OTel via OTLP HTTP with standard `OTEL_EXPORTER_OTLP_ENDPOINT` (unset = no-op). PORTS.md updated in the branch that binds each port. | §9 observability; 10008, 10014, 10015 are unassigned in the range. |
| D-37 | Testing tools: stdlib `testing`; `pgregory.net/rapid` for property tests; testcontainers-go modules for PostGIS, Redis, NATS (JetStream), MinIO; Vitest + Testing Library + `fake-indexeddb`; one Playwright spec; k6 in `tools/loadtest/k6/`. Race detector on ingest and hub packages. | §9 testing row; property tests target the signature problems. |
| D-38 | Frontend libraries: React Router, `idb`, `vite-plugin-pwa` (Workbox), MapLibre GeoJSON circle layers with clustering (not DOM markers, avoids Leaflet-style cost in §4). Vite dev proxy: REST → 10010, `/stream` → 10011; no CORS in dev. | §4 frontend pins; §9 freshness at thousands of markers. |
| D-39 | Repo layout additions: `db/migrations/`, `Makefile`, `deploy/osrm/`, `deploy/.local/` (gitignored), `e2e/`, `tools/loadtest/`, `docs/` (demo script in `docs/demo.md`). | README cannot be hand-edited (AGENTS.md), so the demo script lives in `docs/`. |
| D-40 | Spec process: every **SPEC-AMEND** item is listed in mission.md §7 and in the tech-stack decision log; each feature spec repeats the amendments it needs. Changes to env names update CLAUDE.md in the same branch. | AGENTS.md: spec is source of truth; README is not edited directly. |
| D-41 | Cloud profile (§15) deferred to backlog. The `Fanout` interface (D-17) is kept as the seam; no in-process fanout or embedded NATS in MVP. | §15 is not §2.1; MVP is local compose; avoids a second code path now. |

Gap-fills (self-gate additions, not in the plan's numbering; flagged in mission §7):

| ID | Decision | Rationale |
|---|---|---|
| GF-1 | Driver push notifications deferred to backlog; MVP uses WS `job.updated` (D-21) and polling fallback. **SPEC-AMEND** (deferral). | README §3 says "push when online"; no plan phase owned it; WS + polling covers MVP. |
| GF-2 | `admin_audit(id, fleet_id, actor_id, action, target, at)` append-only table; every admin write records the actor (P2, P3). **SPEC-AMEND.** | README §8 "admin actions logged with actor" had no owning table or phase. |
| GF-3 | Device key endpoints named `POST /devices`, `POST /devices/:id/rotate`, `POST /devices/:id/revoke` (P3). **SPEC-AMEND.** | D-29 and D-30 define the operations; paths needed for P3 role matrix. |
| GF-4 | `jobs.events.*` and `alerts.*` use core NATS (no JetStream stream). Durable record is in PG; WS delivery is best-effort with polling fallback. | D-17 names the transport; durability need is met by `job_events` and `alerts` (README §3, §8). |
| GF-5 | Migrations: sequential zero-padded `NNN_snake_name.up.sql` / `.down.sql` in `db/migrations/`, starting `001`, no timestamps; single transaction-friendly scripts (no `BEGIN`/`COMMIT`, no `CREATE INDEX CONCURRENTLY`); forward-only once merged to `main`; `make migrate` exits non-zero when `schema_migrations.dirty = true`, recovery via `make migrate MIGRATE_ARGS="force <version>"` after manual schema inspection, never by SQL edits to `schema_migrations`. | One feature branch per phase makes sequential numbers safe; golang-migrate's dirty flag is the only safe signal. |
| GF-6 | Makefile wraps the `migrate` CLI directly (no Go wrapper); `DATABASE_URL` required, referenced as `$$DATABASE_URL` (never echoed), not read from `deploy/.env`; host tool paths live in gitignored `deploy/.local/tools.mk`, pulled in with `-include`. | Committing host paths leaks layout; `.env` parsing in make is fragile and risks secrets. |
| GF-7 | `seed` and `seed-demo` are reserved stub targets in P1 that exit 1 with a message naming their phase (P2 and P23); no fake behavior shipped. | Roadmap P1 names `seed`; tech-stack §3 binds `seed-demo`; a failing stub keeps `make help` complete and honest. |
| GF-8 | Testcontainers helper at `internal/testinfra/` (test-only library); embedded migrations in package `db` at `db/migrations.go` (`//go:embed`), import path `fleetops/db`. | `internal/` blocks outside import; `tools/` is for `cmd` binaries (D-39); embedding does not run migrations, so D-13 holds. |
| GF-9 | golangci-lint v2 (`version: "2"`): `standard` linters + `errorlint`, `gosec`, `misspell`; formatter `gofmt`. `revive`, `gocritic`, `unparam`, `goimports` not enabled in P1. | Deterministic signal for a near-empty module; security and error-wrapping coverage before auth. |
| GF-10 | Go module path `fleetops` (bare); `go 1.27`, `toolchain go1.27.3`. | No remote or GitHub owner exists; renaming later is mechanical. |
| GF-11 | One CI workflow `.github/workflows/ci.yml`, one job `gates` on `ubuntu-latest`, `contents: read`, `timeout-minutes: 20`; gates run the make targets (`tools`, `fmt-check`, `lint`, `test` with `-race ./...`, then Postgres via `docker run` with a generated password masked and passed through `GITHUB_ENV`, then `migrate`, `migrate MIGRATE_ARGS="down 1"`, `migrate`); `if: always()` container cleanup. No static password in repo. | Local and CI gates share targets; runtime-generated secret respects AGENTS.md. |
| GF-12 | Test and CI PostGIS image `postgis/postgis:16-3.4` (`testinfra.PostgisImage`). README §12 and compose keep `16` until P4. **SPEC-AMEND** (image tag correction; mission §7). | `16` is not a published tag; the verified tag is recorded, compose change deferred. |
| GF-13 | Testcontainers use OS-assigned ephemeral host ports; PORTS.md "no other host port" rule applies to long-running dev and compose services only. | Parallel test runs cannot practically use fixed ports; PORTS.md intent preserved. |
| GF-14 | `fleets` baseline is exactly README §5 (`id BIGSERIAL PK`, `name TEXT NOT NULL`); fleet timezone arrives as an additive migration in P2. | Baseline stays within roadmap P1 scope. |
| GF-15 | P1 repo hygiene: `.gitignore` gains `deploy/.local/`; D-39 layout gains `.github/workflows/`; stale "not yet present" text in CLAUDE.md and MinIO `phase 3` → `phase 12` in CLAUDE.md and PORTS.md. No port value changes. | Mechanical docs must match the repo; text corrections only. |
| GF-16 | Tool pins: `go1.27.3`; `GOLANGCI_LINT_VERSION := v2.15.0` and `MIGRATE_VERSION := v4.18.3` in the Makefile, installed by `make tools` via `go install …@version` (`migrate` with `-tags postgres`); library pins recorded in `go.mod` at implement time. Host `migrate` reports `dev` (built from source); the round-trip is the acceptance check. | D-13 requires pinned tools; `go install @vX` keeps CI deterministic. |
| GF-17 | `pgregory.net/rapid` is not added in P1 (no property test in the P1 roadmap); it enters with the first property test in P3. | Avoids an unused dependency. |
| GF-18 | Test and round-trip driver is `github.com/lib/pq` via golang-migrate's `database/postgres`; the application driver is chosen in P2. | One driver needed in P1; migrate already brings lib/pq. |

## 12. Risks

| Risk (README §13) | Mitigation | Phase |
|---|---|---|
| Unbounded position history | Monthly partitions, retention job from day one | P6 (scheme in P5) |
| WS hub memory growth | Room cap, ping/pong eviction, race tests, 10k synthetic conns | P7, P23 |
| Geofence evaluation cost | Bbox prefilter, 30 s debounce, exact geometry on candidates only | P18 |
| OSRM RAM | Region extract ~200–500 MB; no continent extract | P20 (D-10) |
| GPS jitter | Filter `accuracy > 50 m` (D-18); heading smoothing (EMA per vehicle, applied at flush); snap-to-road in backlog | P5 |
| Clock skew on devices | Store `device_ts` and `server_ts`; order by `server_ts` (D-15, D-19) | P4, P5 |
| Exactly-once is a lie (§14) | At-least-once + idempotent upserts (D-15, D-24) | P5, P10 |
