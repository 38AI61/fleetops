# Roadmap — FleetOps

Status: approved constitution, 2026-10-25. Ordered small phases; each is one feature branch. Scope comes from README §1–§9; decisions are in `tech-stack.md` (D-IDs; GF-n for self-gate gap-fills). Deviations are in `mission.md` §7.

## 1. Working rules

| # | Rule | Source |
|---|---|---|
| R-1 | One feature loop per phase: spec → implement → validate → test. | AGENTS.md (SDD) |
| R-2 | One branch per phase; one feature spec per phase at `specs/features/NN-<slug>.md`, on that branch. | AGENTS.md; D-40 |
| R-3 | A phase is done when its Validation holds, its Testing depth exists, CI is green, and `deploy/PORTS.md` / CLAUDE.md are updated in the same branch whenever a port or env name changes. | D-36, D-40 |
| R-4 | Each feature spec repeats the SPEC-AMEND items it needs (mission §7). | D-40 |
| R-5 | Do not change behavior the phase spec does not describe. If the spec is silent or wrong, stop and ask. | AGENTS.md |
| R-6 | Any phase that adds an endpoint extends the §8 role-matrix test (tech-stack §9). | README §8, D-37 |
| R-7 | Append-only tables are enforced by trigger in the phase that creates them: `admin_audit` (P2), `job_events` (P10), `geofence_events` (P18), `alerts` (P19). | README §8, D-09, D-24, D-33, GF-2 |
| R-8 | `README.md` is never edited. Spec changes go through the spec process (mission §7). | AGENTS.md |
| R-9 | Security defaults in every phase: no secrets in repo; config from env; hashed device keys and license numbers. | AGENTS.md, README §8 |

README §10 phase numbers are status only. Mapping to plan phases: README P0 → P0 + P1 · README P1 (foundation) → P2–P3 · README P2 (telemetry, simulator, map) → P4–P9 · README P3 (jobs, driver PWA, offline) → P10–P16 · README P4 (geofences, alerts, reports) → P17–P21 · README P5 (harden, demo) → P22–P23.

## 2. Phase 0 — done

- Scaffold: monorepo layout, `deploy/docker-compose.yml` (postgres+postgis, redis, nats, osrm), `deploy/PORTS.md`, Phase 0 config.
- Commit: `b74201d` "Scaffold Phase 0 config". Plan commit `e3e472b` "Add constitution plan prompt". Product spec `bc9373f`.
- Not in Phase 0: CI and golang-migrate (moved to P1, D-13); MinIO (moved to P12, D-07).

## 3. Phases 1–23

Format per phase: **Scope** (one sentence in plan terms) · **Validation** (must hold for done) · **Testing** (minimum depth).

### Foundation

**P1 — Toolchain, CI, migrations baseline** · D-13 · README §10 P0 remainder
- Scope: Go module; Makefile (`migrate`, `test`, `lint`, `seed`); golangci-lint; GitHub Actions (fmt, lint, test, `-race` on ingest/hub); golang-migrate baseline (`postgis` extension, `fleets`); shared testcontainers helper; `db/migrations/`.
- Validation: CI green on an empty-feature PR; `make migrate` up → down → up on a PostGIS container.
- Testing: smoke — migration round-trip test.

**P2 — Auth core** · D-01–D-06, D-02/D-03 endpoints, GF-2 · README §2.1.2, §8
- Scope: `users`, `refresh_tokens`, `admin_audit`; `POST /auth/login`, `/auth/refresh`, `/auth/logout`; JWT middleware (15 min, `fleet_id` + `role` claims); login throttle (5 per 15 min per (email, IP)); `make seed` bootstrap; admin-only `GET/POST/PATCH /users` (SPEC-AMEND); admin writes record actor.
- Validation: login → call → refresh → old refresh rejected; bootstrap idempotent; disabled user cannot refresh.
- Testing: unit (password policy, JWT claims and expiry, throttle) + integration (rotation, reuse detection revokes the family, expiry) + fleet-scoping test (token for fleet A gets 404 on fleet B resources).

**P3 — Fleet registry & pairing** · D-27–D-29, D-31, D-28, GF-2, GF-3 · README §2.1.1, §8
- Scope: vehicles (registry status `{active, maintenance, retired}`), drivers (license hash), devices (`kind` pwa|tracker; key issue, rotate, revoke), `vehicle_pairings` history, `vehicles.paired_device_id` mirror; CRUD per §8 role matrix; `/vehicles/:id/pairings`, device key endpoints (SPEC-AMEND).
- Validation: each role × resource cell of §8 verified; pairing swaps keep history; revoked device is refused at ingest (verified in P4).
- Testing: integration role-matrix table test (4 roles × every resource × verbs) + property test: random pair/unpair/rotate/revoke sequences never leave two current pairings on one device or one vehicle, and `vehicles.paired_device_id` always mirrors the current pairing.

### Telemetry

**P4 — Position gateway** · D-15, D-16, D-29 · README §3, §6 `/ingest/position`
- Scope: `POST /ingest/position` with device-key auth (constant-time compare); validates `lat, lng, speed, heading, accuracy, ts`; stamps `msg_id` (UUIDv7) and `server_ts`; async publish to `GPS_RAW` (`gps.raw.<fleet_id>`) with 204 on accept, 503 when the pending buffer is full; NATS container gets `-js` (compose change in this branch).
- Validation: valid → 204 and message in stream; invalid → 400 and nothing published; revoked key → 401; full buffer → 503.
- Testing: integration on testcontainers NATS with JetStream (the four cases above; no mocks of NATS).

**P5 — Batch writer core** · D-14 (parent table), D-15, D-18, D-19, D-22 · README §3, §7, §13
- Scope: buffer flushes at 1 s after first message or 500 messages, and drains on shutdown; `accuracy > 50 m` dropped before buffer and counted; at flush, `server_ts` older than 60 s acked and counted; one `UNNEST` batch upsert into `vehicles_latest` guarded by `EXCLUDED.server_ts > vehicles_latest.server_ts`; device → vehicle resolved per flush through `vehicles.paired_device_id`; unpaired devices acked and counted, not stored; `positions` insert `ON CONFLICT DO NOTHING`; heading smoothing (§13, EMA per vehicle at flush); tunables read from `FLEETOPS_*` with defaults (D-22). Partitions for tests are created by the test helper; runtime ensure-ahead is P6.
- Validation: throughput smoke at 100 msg/s on local PG; no row locks held across flushes.
- Testing: **property tests on the signature problems**: (a) flush semantics — triggers fire at 1 s or 500, batch never exceeds 500, no message lost or duplicated across flushes; (b) batch upsert — any permutation of a batch yields the same `vehicles_latest`, and replaying a batch is a no-op; both against a PostGIS testcontainer. Go unit tests for the buffer state machine.

**P6 — Ingest consumer & partitions** · D-14, D-16 · README §5, §7, §13
- Scope: durable JetStream consumer; ack after commit; NAK with delay on DB failure; `MaxDeliver` then terminate + metric; partition ensure-ahead (current + next 2 months at boot and daily); no DEFAULT partition; missing partition → metric + NAK; daily retention (detach + drop children whose upper bound ≤ start of (now − 12 months)); ingest metrics registered (rate, flush size; exposed in P22).
- Validation: kill PG mid-flush → redelivery → exactly one row per message; retention drops only expired partitions.
- Testing: integration on the full ingest path (gateway → JetStream → worker → PG) incl. injected DB failure; retention with injected clock; `-race` on ingest.

**P7 — Fanout, presence, WS hub** · D-17, D-20, D-21, D-22, D-41 · README §3, §6 `WS /stream`, §7
- Scope: `Fanout` interface with Redis implementation (D-41 seam); `position.batch` ≤ 200 points per message to `positions.batch.<fleet_id>`; presence (`SET device:<id> online EX 120 GET`) and `presence.changed`; offline sweeper every 15 s over `vehicles_latest`; WS hub with first-message auth (5 s, close 4401), fleet room, cap 2,000 (503 at cap), ping 20 s / evict after 60 s without pong, close at token expiry; hub subscribes NATS `jobs.events.*` and `alerts.*` (GF-4).
- Validation: browser-free client receives coalesced batches; connection at cap refused; silent device yields `presence.changed offline` once.
- Testing: **property test on chunking** (every point appears exactly once, order preserved, each chunk ≤ 200); integration with real Redis and real WS clients; `-race` on hub; 1,000 synthetic connections in CI (10k in P23).

**P8 — Map read API & GPS simulator** · D-12, D-27 · README §6 `/map/vehicles`, §2.1.3, §10 P2
- Scope: `GET /map/vehicles?bbox=` returns latest positions in fleet with display status (`idle` / `en_route` / `offline`, D-27); `tools/seed --demo-devices` writes `deploy/.local/devices.json` (gitignored); `tools/simulator --fleet=50 --rate=5s` drives straight-line legs between seeded waypoints; no OSRM dependency.
- Validation: bbox returns only in-bbox vehicles of the caller's fleet; simulator sustains configured rate.
- Testing: integration (bbox query vs fixture; status derivation edge at exactly 120 s) + simulator unit property tests (interpolated points stay on their leg; speed within bounds; emit interval respected).

**P9 — Operations map UI** · D-38 · README §2.1.4, §4 frontend pins
- Scope: `apps/web` (Vite + React 18 + TS, TanStack Query, React Router); login; MapLibre circle layer with native GeoJSON clustering (not DOM markers); status colors; gray-out after 2 min without update; click-for-details card; follow-vehicle mode; WS merge into query cache; Vite dev proxy (REST → 10010, `/stream` → 10011).
- Validation: manual run against compose + api + hub + simulator at 50 vehicles.
- Testing: Vitest unit (status derivation, gray-out timer, WS reducer, follow-camera logic) + component test for details card. No E2E here.

### Jobs

**P10 — Job core & state machine** · D-23, D-24, D-26, D-17 (`jobs.events`) · README §2.1.5, §5
- Scope: `jobs` additive columns (`notes`, `size_class`, `weight_kg`, `delivery_geom`, `failure_reason`); `job_events` append-only (trigger); `job_transitions` reference table + `BEFORE UPDATE` trigger (writes events, rejects invalid transitions; actor via `SET LOCAL app.actor_id`); dispatcher `POST /jobs`, `GET /jobs` (state, driver, day filters), `POST /jobs/:id/assign` (also reassign while `created|assigned`), `POST /jobs/:id/cancel` (SPEC-AMEND); publish `jobs.events.<fleet_id>`.
- Validation: illegal transitions return 409 and persist nothing; every successful transition has exactly one event.
- Testing: **property test on the state machine**: random transition sequences — persisted transitions ⊆ table; event count equals successful transitions; replaying the last transition returns 200 with no new event. Integration: direct invalid `UPDATE` on `jobs` rejected by trigger; direct `UPDATE`/`DELETE` on `job_events` rejected.

**P11 — Driver job API** · D-23 · README §6 driver endpoints, §8 driver row
- Scope: `GET /driver/jobs/today`; `accept`, `start`, `pickup`, `fail` (reason required; `fail` is SPEC-AMEND); driver scoped to own jobs; driver may open `/stream` and receive only `job.updated` for own jobs.
- Validation: driver cannot touch another driver's job; repeated accept over HTTP is idempotent.
- Testing: integration role-matrix extension (R-6) + cross-driver isolation + idempotent-replay HTTP tests.

**P12 — Proof of delivery** · D-07, D-25 · README §2.1.5, §8 signed URLs
- Scope: MinIO compose service (container 9000/9001 → host 10006/10007, bound to 127.0.0.1, volume `minio_data`); idempotent `proofs` bucket creation at API boot; `POST /driver/jobs/:id/deliver` multipart (photo + lat/lng; JPEG/PNG/WebP ≤ 5 MB); object key `proofs/<fleet_id>/<job_id>/<uuid>.<ext>`; `jobs.proof_photo` holds the key; job responses carry a presigned GET valid 15 min.
- Validation: deliver stores object and coordinates; oversize or wrong MIME rejected; URL works, then expires.
- Testing: integration on testcontainers MinIO (upload, presigned fetch, expiry via short-TTL test hook, rejection cases).

**P13 — Dispatcher assignment UI & nearest-van suggestion** · README §2.1.5, §10 demo, §4 PostGIS
- Scope: jobs list and create form (pickup/dropoff picked on map, window, notes, size, weight); assignment panel showing nearest active vehicles via `ST_Distance` on `vehicles_latest` within fleet; assign action.
- Validation: nearest-van ordering matches a hand-computed fixture.
- Testing: integration (PostGIS nearest query on fixture, fleet-scoped) + Vitest component tests for form and assignment panel.

**P14 — Driver PWA, online** · D-30, D-38 · README §2.1.5, §4 driver app
- Scope: `apps/driver-pwa` (React PWA, separate Vite build, port 5174 dev); manifest + service-worker app shell (`vite-plugin-pwa`); key setup from admin-issued one-time key (D-30); today's jobs; one-tap transitions when online; photo capture for delivery; position posting with device key while shift active.
- Validation: manual install and one-tap flow against local API at 5174.
- Testing: Vitest unit for transition button ↔ state mapping and key-setup validation.

**P15 — Driver PWA offline queue** · D-38 · README §1 driver, §4 IndexedDB
- Scope: `idb` queue for transitions and photos, FIFO per job, replay on reconnect; cached snapshot for `/driver/jobs/today`; 409 conflicts surfaced to the driver, not dropped.
- Validation: airplane-mode run; reconnect replays in order; conflicts visible.
- Testing: Vitest with `fake-indexeddb`: FIFO across jobs; replaying the same queued transition twice produces one server event; conflict surfacing.

**P16 — Playwright happy path** · D-37 · README §9 testing
- Scope: the single `e2e/happy-path.spec.ts`: dispatcher login → create job → assign → driver accepts → en_route → picked_up → deliver with photo → job shows delivered with proof. Runs against compose + local api, hub, web, driver-pwa; CI job brings compose up.
- Validation: green in CI on this branch.
- Testing: exactly one Playwright spec in MVP. No second E2E is added later; the demo script stays manual.

### Geofences & alerts

**P17 — Geofence registry & drawing** · D-26, D-33 · README §2.1.6, §5, §8
- Scope: `geofences` additive columns (`radius_m`, `bbox`, `dwell_min`); `GET/POST/DELETE /geofences` in GeoJSON; polygon and circle; schedule and dwell validation; `geofence_events` table (append-only trigger); manager read-only (§8); MapLibre draw UI for polygons and circles.
- Validation: GeoJSON round-trip; invalid or self-intersecting polygon rejected; map draw creates the fence.
- Testing: integration (round-trip, invalid geometry, `EXPLAIN` shows GIST bbox index used for prefilter) + Vitest for draw-to-GeoJSON.

**P18 — Geofence evaluator** · D-33, D-34 · README §2.1.6, §7, §13
- Scope: after each flush, evaluate moved vehicles against candidate fences (bbox prefilter → `ST_Covers` for polygons, `ST_DWithin` for circles); emit `enter` / `exit` / `dwell` to `geofence_events`; debounce 30 s per vehicle-fence pair; dwell fires on the first position after `dwell_min`; evaluation latency metric.
- Validation: a vehicle crossing a depot produces one enter and one exit; boundary jitter within 30 s produces at most one event.
- Testing: **property test on debounce** (random streams: ≤ 1 event per pair per 30 s; every recorded event matches the exact-geometry oracle computed in PostGIS) + integration of the flush hook.

**P19 — Alerts core** · D-08, D-09, D-11, D-34 · README §2.1.7, §5, §6, §8
- Scope: `alert_rules` (three fixed kinds seeded per fleet with spec defaults); `alerts` append-only with one-time ack transition (trigger); `GET /alerts?since=`; `PATCH /alerts/:id/ack`; `GET/PATCH /alert-rules` (admin, manager); `device_silent` evaluator (10 min, once per silence episode, re-arms on report, warning); `geofence_exit_outside_schedule` (critical); `alert.raised` on WS via `alerts.<fleet_id>`; `Notifier` stub (critical → managers; logs event type and user id only, never the address); notification bell UI.
- Validation: silence raises once; ack once; second ack rejected; bell shows unacked count.
- Testing: integration (episode semantics; ack exactly once; DELETE and extra UPDATE rejected) + **property test on silence episodes** (random arrival/silence sequences: alerts = episodes) + unit tests for schedule windows across fleet timezone (DST boundaries).

**P20 — ETA slip & OSRM** · D-10, D-34, D-35 · README §2.1.7 ETA, §3, §4 routing, §13 OSRM RAM
- Scope: `deploy/osrm/prepare.sh` one-shot extract/partition/customize into `osrm_data` (not in CI); OSRM client for `/route`; ETA recomputed per active job at most once per 5 min and on assign; `eta_slip` rule (projected arrival > `window_end` + 15 min → warning); `jobs.eta_minutes` updated; OSRM unreachable → rule skipped with metric, no alert.
- Validation: manual prepare run on a region extract; slip alert fires on a delayed fixture job.
- Testing: contract tests with recorded OSRM JSON via `httptest` (parse, duration, error codes) + degradation test (OSRM down → no alert, metric incremented) + rate-limit unit test.

### Reports & hardening

**P21 — Daily reports & CSV** · D-32 · README §2.1.8, §9 API p95
- Scope: `daily_driver_stats` rollup, refreshed every 15 min for today and once after midnight for the previous day (fleet timezone); `GET /reports/daily?date=&driver=` JSON and `?format=csv` (streamed); driver sees own stats; report page in `apps/web`.
- Validation: report values match a hand-computed fixture; re-running the rollup changes nothing.
- Testing: integration golden tests (distance, idle, on-time % from fixture) + idempotent-rollup test + CSV parse round-trip + Vitest component test for the report page.

**P22 — Observability** · D-36 · README §9 observability
- Scope: Prometheus `/metrics` on api, ingest (host 10008, local) and hub with the full §9 metric set; `prometheus` (host 10014 → 9090) and `grafana` (host 10015 → 3000) compose services, bound to 127.0.0.1; Grafana dashboard JSON in `deploy/grafana/`; OpenTelemetry spans on the assign flow via OTLP HTTP (`OTEL_EXPORTER_OTLP_ENDPOINT`, unset = no-op); `deploy/PORTS.md` updated in this branch.
- Validation: dashboard shows ingest rate, flush sizes, WS connections, geofence latency; assign trace visible in the collector.
- Testing: unit test that metric names and labels are registered; test that assign emits a span with the job id attribute (in-memory exporter).

**P23 — Load, tuning & demo** · D-22, D-37 · README §9 targets, §13, §10 demo
- Scope: k6 scripts in `tools/loadtest/k6/` for WS fanout, HTTP assign and report p95, and ingest at 100 msg/s steady and 2,000 msg/s burst (10×); tune flush and channel sizes (`FLEETOPS_*`); `-race` with 10k synthetic WS connections; `make seed-demo` (50 vehicles, drivers, devices); `docs/demo.md` 5-minute script.
- Validation: thresholds met on the local stack: API p95 ≤ 300 ms; end-to-end freshness p95 ≤ 2 s (from simulator timestamps); burst handled without unbounded memory.
- Testing: k6 thresholds (on demand and nightly, not per PR); race run with 10k connections.

## 4. Unscheduled backlog

Not in MVP. Each item is named in the source shown. Nothing here is started without a new phase.

| Item | Source | Note |
|---|---|---|
| Multi-stop route optimization (OSRM `/trip`, drag-to-reorder) | README §2.2 | Needs OSRM `/trip` in the extract |
| Public "track my delivery" link (signed, expiring, map-only) | README §2.2 | |
| Driver scoring (on-time %, harsh-braking proxy) | README §2.2 | |
| Fuel / odometer logs; maintenance reminders | README §2.2 | |
| Webhooks out (job events → customer systems); i18n of dispatch UI | README §2.2 | `jobs.events` is the feed (§7) |
| Cloud deployment profile: Neon, Render, Cloudflare Pages, embedded NATS, in-process hub, public OSRM | README §15, D-41 | Single-instance note applies |
| Driver push notifications | README §3, GF-1 | MVP uses WS + polling |
| Snap-to-road for GPS positions | README §13 V2 | |
| Real email delivery for notifications | D-11 | Stub only in MVP |
| EXIF stripping on delivery photos | D-25 (accepted risk) | Photos stored as received |
| Dwell evaluated only on next position (no sweeper) | D-33 (accepted risk) | Silent vehicle never reaches dwell |
| In-memory login throttle (single instance) | D-04 (accepted risk) | Not shared across instances |
| Presence is WS-only, not persisted | D-20 (accepted risk) | DB remains source for offline |
| Public OSRM demo server: ETA rules degrade in cloud | D-10 (accepted risk) | Rule skipped with metric |

## 5. Cross-phase NFR gates

README §9 targets, when each is first measured, and the phase that enforces it.

| Gate | First measured | Enforced / final | Threshold |
|---|---|---|---|
| Ingest steady | P5 (smoke, local PG) | P23 (k6) | 100 msg/s |
| Ingest burst | — | P23 (k6) | 2,000 msg/s, no unbounded memory |
| Freshness | P9 (manual, 50 vehicles) | P23 (simulator timestamps) | p95 ≤ 2 s |
| API latency (assign, report) | P13 (manual) | P21 (report), P23 (k6) | p95 ≤ 300 ms |
| WS memory and conns | P7 (1,000 synthetic in CI) | P23 (10k with `-race`) | Cap 2,000 per room; no leak |
| Observability metrics | P6 (registered) | P22 (dashboard, assign trace) | Set in §9 |
| Geofence latency | P18 (metric) | P22 (dashboard) | Metric exists and is exported |
| Role matrix | P3 | Every endpoint phase (R-6) | 4 roles × resources × verbs |
| Append-only | P2 (`admin_audit`), P10 (`job_events`) | P19 (`alerts`), P18 (`geofence_events`) | Trigger rejects UPDATE/DELETE (ack exception in P19) |
| Testing depth | Every phase | P16 (one E2E), P23 (k6 only) | Per §3 |
