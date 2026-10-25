# Feature 07 — Fanout, presence & WS hub (P7)

Status: approved plan, persisted 2026-10-25. Branch `phase-7-fanout-ws`.
Source plan: `/root/.claude/plans/p7-plan-nifty-tide.md` (approved). Binding constitution: `specs/constitution/roadmap.md` §1 (R-1…R-9), §3 P7, §5, `specs/constitution/tech-stack.md` §1, §2, §3, §4, §5, §7.3, §8, §9, §11 (D-03, D-17, D-20, D-21, D-22, D-41, GF-4, GF-5, GF-8, GF-13), `specs/constitution/mission.md` §7 (SPEC-AMEND rows), `specs/features/06-consumer-partitions.md` (P6 decisions your code builds on), `AGENTS.md`, `CLAUDE.md`.

This spec is written before any P7 code (R-1, SDD). Implementation starts at Plan task group G0.

## Scope

Roadmap phase P7 (`specs/constitution/roadmap.md` §3): D-17, D-20, D-21, D-22, D-41 · README §3, §6 `WS /stream`, §7.

In scope:

- `Fanout` interface with a Redis implementation (D-41 seam). No in-process fanout, no embedded NATS.
- `position.batch` publish after each successful flush, ≤ 200 points per message, to `positions.batch.<fleet_id>`.
- Presence: `SET device:<id> online EX <ttl> GET` on the ingest accept path, `presence.changed` on transitions, offline sweeper every 15 s over `vehicles_latest`.
- `services/ws-hub` (pinned path, tech-stack §3): `WS /stream?fleet=<id>` with first-message auth (5 s, close 4401), fleet rooms, cap 2,000 (503 at cap), ping 20 s / evict after 60 s without pong, close at token expiry, NATS core subscriptions to `jobs.events.*` and `alerts.*` (GF-4).
- Config: `REDIS_URL` read and validated in `config.Load`; `FLEETOPS_WS_ROOM_CAP`, `FLEETOPS_PRESENCE_TTL`.
- Tests: chunking property test, Redis and WS integration tests, `-race` on hub and ingest, 1,000 synthetic connections in CI.
- CLAUDE.md: env and tunable rows, `go run` lines for ws-hub and for the API (which now needs `REDIS_URL`).

Out of scope: see the final section. No migrations (presence is not persisted). No compose, README, PORTS.md, or constitution edits.

## Plan

Build order. Each group lists its tests. Test names are binding.

**G0 — Preflight and dependencies.**
- Confirm a Go 1.27.3 toolchain runs here. The sandbox has no `go` on PATH, so set `GO` in `deploy/.local/tools.mk` (gitignored) or put Go on PATH. Confirm Docker is up for testcontainers.
- Confirm the sweeper's data source: `vehicles_latest` has `fleet_id` and `server_ts` but **no `device_id` column** (migration 007) — the sweeper must `JOIN vehicles v ON v.id = vehicles_latest.vehicle_id` and read `v.paired_device_id`, filtering `IS NOT NULL` (GF-106). Confirm the ingest JetStream consumer's `AckWait` (30 s, GF-86) is well above the 2 s publish budget (GF-103). If either fails, stop and report (R-5).
- Add the three pinned deps (GF-123). `go.mod` diff is those requires only.
- Baseline: `make fmt-check lint test` green before any change.
- Tests: none new. This group is the baseline gate.

**G1 — `internal/fanout`: seam, pure chunker, Redis implementation.**
- `fanout.go`: `Fanout` interface, `Point`, `PresenceEvent`, `Nop` (GF-100).
- `chunk.go`: pure `PlanPositionBatches(points []Point, max int) []Batch` (GF-102).
- `redis.go`: `RedisFanout` on `*redis.Client`, JSON bodies per GF-101.
- Tests:
  - `TestChunkPropertyEveryPointOnceOrderMax200` (rapid, property; GF-102).
  - `TestChunkBoundary200And201` (example).
  - `TestRedisFanoutRoundTrip` (real Redis via testinfra: publish, then PSUBSCRIBE receives the exact body).
  - `TestRedisFanoutDownReturnsWithinBudget` (client pointed at `127.0.0.1:1`; returns an error within 2 s).

**G2 — `internal/presence`: Tracker and sweeper.**
- `tracker.go`: `Tracker.Observe(ctx, []Observation, now)`. Pipelined `SET … EX ttl GET`, one per distinct device, in-memory state under one mutex (GF-104, GF-106).
- `sweeper.go`: `Sweeper.Sweep(ctx, now)` runs the `vehicles_latest` query and emits offline once per episode. `Sweeper.Run(ctx)` ticks every 15 s (GF-106).
- Tests:
  - `TestPresenceSetGetTransitions` (real Redis: absent → online publish; present and online → no publish; present but offline in memory → online publish).
  - `TestSweepEmitsOfflineOncePerEpisode` (real PostGIS `vehicles_latest` with a stale row, real Redis; two sweeps → one offline event; a new observe → online; the next stale sweep → offline again).
  - `TestSweepRaceGuardKeepsFreshDevice` (stale row snapshot, then an observe with a newer `ServerTS` → no offline).
  - `TestSweepBootEmitsOncePerStaleDevice` (fresh tracker, two stale devices → one offline each, once).

**G3 — ingest hook.**
- `internal/ingest/publish.go`: `Publisher{Fanout, Tracker, Metrics}` with `Publish(ctx, stored []Message)`. It converts `Message` → `fanout.Point` / `presence.Observation`, calls `PublishPositions`, then `Tracker.Observe`, then records counters. It never returns an error (GF-103, GF-121).
- `consume.go` `flush`: call `Publish` after the GF-79 count check and before `beforeAck` / `ackAfterCommit`, using `context.WithoutCancel` with a 1 s budget per stage (GF-103).
- `services/ingest/main.go`: build the Redis client, the Tracker, the Publisher, and the sweeper goroutine (GF-118).
- Tests:
  - `TestFlushPublishesStoredOnly` (real PostGIS, Redis, NATS: accuracy-dropped, stale, and unpaired messages produce no `position.batch`).
  - `TestPublishChunksPerFleet` (a flush mixing two fleets → one batch per fleet, each ≤ 200).
  - `TestFlushAcksWhenRedisDown` (Redis at `127.0.0.1:1`; the flush acks, the batch is committed, the failure counter increments, and the flush returns within 2.5 s).
  - Existing ingest tests pass `fanout.Nop{}` and a no-op tracker.

**G4 — `internal/config`.**
- `REDIS_URL` required: non-empty, scheme `redis` or `rediss`, host present (GF-119).
- `FLEETOPS_WS_ROOM_CAP` and `FLEETOPS_PRESENCE_TTL` via the GF-80 `loadFlush` style (GF-119).
- Update `services/api/main_test.go` env if it sets env.
- Tests: `TestLoadRedisURLRequired`, `TestLoadRedisURLRejectsScheme`, `TestLoadWSRoomCap` (table: default, 1, 10000 accepted; 0, 10001, `abc` rejected), `TestLoadPresenceTTL` (default, 30s, 600s accepted; 29s, 601s rejected), `TestConfigErrorsNeverContainValues`.

**G5 — `internal/hub`: core lifecycle.**
- `hub.go`: `Options` (addr-independent: `RoomCap`, `AuthWindow`, `PingInterval`, `PongEvictAfter`, `Verifier` = `*auth.Issuer`, `OriginPatterns`), `Hub`, `Counters` (atomic, GF-121).
- `room.go`: room registry with reserve-before-Accept cap (GF-112).
- `conn.go`: lifecycle: pre-Accept checks → Accept → auth frame → join → `CloseRead` → writer goroutine → ping ticker → token-expiry timer → teardown (GF-110 to GF-115).
- Tests:
  - `TestHubAuthOK` (valid token → `auth.ok`).
  - `TestHubMissingFleetIs400` (no HTTP upgrade).
  - `TestHubAuthTimeoutClose4401` (AuthWindow 300 ms; no frame → close 4401).
  - `TestHubAuthBadTokenClose4401` (bad signature, expired, wrong type, or binary frame → 4401).
  - `TestHubFleetMismatchClose4403`.
  - `TestHubClosesAtTokenExpiry` (token exp in 2 s → close 4401 within 3 s).
  - `TestHubRefusesConnectionAtRoomCap` (cap 2: third Dial → HTTP 503 with no upgrade; after one client closes, the next Dial succeeds).
  - `TestHubEvictsSilentClientOnPongTimeout` (PingInterval 100 ms, PongEvictAfter 300 ms; client never reads → evicted; `Counters.EvictedPong` = 1).
  - `TestHubEvictsSlowConsumer` (client never reads and the producer floods → close 4408; `EvictedSlow` = 1).
  - `TestHubRoomIsolation` (fleet A broadcast never reaches fleet B).

**G6 — hub subscriptions and relay.**
- `subscribe.go`: Redis PSUBSCRIBE `positions.batch.*` and `presence.changed.*`; NATS core Subscribe `jobs.events.*` and `alerts.*` (GF-108).
- Relay rules and driver filter (GF-109, GF-116, GF-117).
- Tests:
  - `TestHubRelaysRedisBatchVerbatim` (real Redis publish → client receives identical bytes).
  - `TestHubRelaysPresenceChanged`.
  - `TestHubRelaysNATSJobAndAlert` (real NATS core synthetic publish).
  - `TestHubDropsMismatchedFleetEnvelope` (subject fleet ≠ body `fleet_id` → dropped, `Counters.DroppedEnvelope` = 1).
  - `TestHubDriverReceivesOnlyOwnJobUpdated`.

**G7 — 1,000 synthetic connections.**
- `TestThousandSyntheticConnections` in `internal/hub`: 1,000 coder clients with `SetReadLimit(1 << 20)`, all authenticated; publish one 200-point batch; every client gets it. Goroutines return to baseline +50 after close (GF-125).
- Budget: ≤ 60 s wall with `-race` on this host.

**G8 — `services/ws-hub` wiring and end-to-end.**
- `main.go`: `run(ctx) error`, flag `-addr` default `127.0.0.1:10011`, boot order and shutdown per GF-118.
- Tests:
  - `TestWSHubCoalescedBatchEndToEnd` (roadmap Validation 1). Real ingest `Consumer` on NATS `GPS_RAW` → flush → Redis → hub → browser-free coder client receives a coalesced batch with all stored points, in order, ≤ 200 per frame.
  - `TestSilentDeviceOfflineOnceEndToEnd` (roadmap Validation 3). Device goes online through ingest; the sweeper runs twice at a fake now + 121 s; the client receives exactly one `presence.changed` offline.
  - `TestWSHubShutdownClosesWith1001` (ctx cancel → clients get close 1001 and `run` returns within 5 s).
  - Roadmap Validation 2 (cap refused) is `TestHubRefusesConnectionAtRoomCap` in G5.

**G9 — CLAUDE.md, scope fences, validation run.**
- CLAUDE.md: tunables rows for `FLEETOPS_WS_ROOM_CAP` and `FLEETOPS_PRESENCE_TTL`; API run line gains `REDIS_URL=…`; ws-hub `go run` line (D-40).
- Run the Validation section in full.

## Requirements

### Binding, carried verbatim

From `specs/constitution/tech-stack.md` §11 (decision log):

- **D-03:** "Access JWT: HS256 signed with `JWT_SECRET` (startup refuses < 32 bytes); claims `sub, fleet_id, role, exp` (15 min). WS closes at `exp`. | §8 JWT 15 min; fixed algorithm avoids alg confusion."
- **D-17:** "Transport split: Redis pub/sub carries `position.batch` and `presence.changed` (per §3, §4, §12). NATS carries `jobs.events` and `alerts` (per §7). The §7 row putting `positions.batch.<fleet_id>` on NATS is superseded by §3 and §4. **SPEC-AMEND.** | §3, §4 and §7 disagree; §3 and §4 are the architecture and stack, so they win."
- **D-20:** "Presence: Redis `device:<id>` = `online` with `EX 120`, refreshed per message; `SET … GET` detects offline → online and publishes `presence.changed`; offline transitions come from an ingest sweeper every 15 s over `vehicles_latest` (`server_ts < now − 120 s`) with in-memory last-emitted state. Presence is WS-only, not persisted. | §7 presence TTL; DB remains the source of truth for offline."
- **D-21:** "WS hub: auth by first message `{"type":"auth","token":…}` within 5 s, else close 4401; token `fleet_id` must match `?fleet=`; drivers may connect but receive only `job.updated` for own jobs; room cap 2,000 (new connection → 503 at cap); ping 20 s, evict after 60 s without pong; close at token expiry. | Keeps tokens out of URLs and logs; §7 cap and eviction; §8 driver scope."
- **D-22:** "Tunables are optional env vars with defaults, prefix `FLEETOPS_`: `FLUSH_INTERVAL=1s`, `FLUSH_MAX=500`, `WS_ROOM_CAP=2000`, `PRESENCE_TTL=120s`, `ACCURACY_MAX_M=50`. The §12 names stay required. **SPEC-AMEND** (additive). | §7 and §13 values should be tunable for Phase 23 load tuning without code edits; §12 list stays intact."
- **D-40:** "Spec process: every **SPEC-AMEND** item is listed in mission.md §7 and in the tech-stack decision log; each feature spec repeats the amendments it needs. Changes to env names update CLAUDE.md in the same branch. | AGENTS.md: spec is source of truth; README is not edited directly."
- **D-41:** "Cloud profile (§15) deferred to backlog. The `Fanout` interface (D-17) is kept as the seam; no in-process fanout or embedded NATS in MVP. | §15 is not §2.1; MVP is local compose; avoids a second code path now."
- **GF-4:** "`jobs.events.*` and `alerts.*` use core NATS (no JetStream stream). Durable record is in PG; WS delivery is best-effort with polling fallback. | D-17 names the transport; durability need is met by `job_events` and `alerts` (README §3, §8)."
- **GF-5 (not exercised; P7 adds no migration):** "Migrations: sequential zero-padded `NNN_snake_name.up.sql` / `.down.sql` in `db/migrations/`, starting `001`, no timestamps; single transaction-friendly scripts (no `BEGIN`/`COMMIT`, no `CREATE INDEX CONCURRENTLY`); forward-only once merged to `main`; `make migrate` exits non-zero when `schema_migrations.dirty = true`, recovery via `make migrate MIGRATE_ARGS="force <version>"` after manual schema inspection, never by SQL edits to `schema_migrations`."
- **GF-8 (applies to the test helper):** "Testcontainers helper at `internal/testinfra/` (test-only library); embedded migrations in package `db` at `db/migrations.go` (`//go:embed`), import path `fleetops/db`."
- **GF-13 (applies to Redis in tests):** "Testcontainers use OS-assigned ephemeral host ports; PORTS.md \"no other host port\" rule applies to long-running dev and compose services only."

From `specs/constitution/mission.md` §7 (SPEC-AMEND rows):

- **D-17:** "Redis carries `position.batch` and `presence.changed`; NATS carries `jobs.events` and `alerts`; the §7 row putting `positions.batch` on NATS is superseded by §3 and §4" (P7).
- **D-22:** "Optional `FLEETOPS_*` tunables; §12 names stay required" (additive; P5, repeated here).

From `specs/constitution/roadmap.md` §3 P7 (scope and validation, verbatim):

- Scope: "`Fanout` interface with Redis implementation (D-41 seam); `position.batch` ≤ 200 points per message to `positions.batch.<fleet_id>`; presence (`SET device:<id> online EX 120 GET`) and `presence.changed`; offline sweeper every 15 s over `vehicles_latest`; WS hub with first-message auth (5 s, close 4401), fleet room, cap 2,000 (503 at cap), ping 20 s / evict after 60 s without pong, close at token expiry; hub subscribes NATS `jobs.events.*` and `alerts.*` (GF-4)."
- Validation: "browser-free client receives coalesced batches; connection at cap refused; silent device yields `presence.changed offline` once."

From `specs/constitution/tech-stack.md` §5 and §7.3, which the wire contract follows: `FLEETOPS_WS_ROOM_CAP` [2000] and `FLEETOPS_PRESENCE_TTL` [120s] are additive env names; any env name change updates CLAUDE.md in the same branch (D-40).

### New decisions (GF-100 onward)

**GF-100 — Fanout seam and Redis implementation (D-41, D-17).** Package `internal/fanout`:

```go
type Fanout interface {
    PublishPositions(ctx context.Context, points []Point) error
    PublishPresence(ctx context.Context, ev PresenceEvent) error
}
```

`RedisFanout` wraps `*redis.Client` (`github.com/redis/go-redis/v9` v9.23.0). `Nop` is the no-op implementation used by tests that do not exercise fanout. There is no in-process fanout and no embedded NATS. `PublishPositions` groups and chunks via GF-102, then publishes each batch in order. Errors from the batches are joined and returned. Redis client options are library defaults plus `redis.ParseURL(REDIS_URL)`. Reconnect is go-redis's own; a publish that fails is reported to the caller, never retried inside fanout.
*Why:* D-41 keeps the seam; go-redis is the maintained official client, supports RESP3 and `SET … GET`, and auto-resubscribes PubSub.

**GF-101 — Redis wire formats.** Channels: `positions.batch.<fleet_id>`, `presence.changed.<fleet_id>`. Key: `device:<device_id>`, value `online`.
`position.batch` body (JSON object):
`{"type":"position.batch","fleet_id":<int64>,"points":[{"msg_id":<uuid string>,"device_id":<int64>,"lat":<f64>,"lng":<f64>,"speed_kmh":<f64>,"heading_deg":<f64>,"accuracy_m":<f64>,"device_ts":<RFC3339Nano UTC>,"server_ts":<RFC3339Nano UTC>}]}`
Point field names and types match the GF-58 `wirePayload` exactly, minus nothing.
`presence.changed` body: `{"type":"presence.changed","fleet_id":<int64>,"device_id":<int64>,"state":"online"|"offline","ts":<RFC3339Nano UTC>}`.
The hub forwards Redis bodies verbatim (no re-marshal).
*Why:* the Redis body is already the client envelope, so the hub does no per-message work; the field set mirrors GF-58 so clients see one vocabulary.

**GF-102 — Chunk rule (pure function).** `const PositionBatchMax = 200` in `internal/fanout`. Not an env var: the roadmap pins ≤ 200 as a client contract, and no tuning reason exists (D-22 rule). `PlanPositionBatches(points, max)`:
1. Group by `FleetID`, fleets in first-appearance order, points in original order within a fleet.
2. Split each group into consecutive chunks of ≤ `max`.
3. A chunk never spans fleets.
Output: `[]Batch{FleetID, Points}`.
Property (`TestChunkPropertyEveryPointOnceOrderMax200`, rapid, 1,000 checks): every input index appears exactly once across all batches; for each fleet, the concatenation of its batches equals that fleet's input subsequence (order preserved); each batch has `1 ≤ len ≤ max`; each batch is single-fleet. Generator: 0–1,000 points, 1–5 fleets, duplicate `msg_id`s allowed, `max` in 1–250.
*Why:* D-22 forbids invented env names; splitting by fleet is required because the subject carries `fleet_id`.

**GF-103 — Publish site and failure semantics.** `ingest.Publisher.Publish(ctx, stored []Message)` is called from `Consumer.flush` after `Writer.Flush` returns nil, after the GF-79 count check, before `beforeAck` and `ackAfterCommit`. It consumes `FlushResult.Stored` only. Stale, unpaired, and accuracy-dropped messages are never published. The call uses `context.WithoutCancel(ctx)` with a 1 s budget per stage (positions, then presence). Errors are counted (GF-121) and logged. They never fail the flush and never delay the ack beyond the budget. The publish happens after commit, so a published point is always a durable point.
Crash between publish and ack → JetStream redelivers → the point is published again (WS duplicate). Clients dedupe by `msg_id`. A crash after ack cannot skip the publish, because publish comes first.
Residual risk (recorded): during a Redis outage each flush costs up to 2 s extra. Ingest keeps draining, and WS subscribers miss the batches. The DB is the record.
Verify in G0 that `AckWait` ≫ 2 s.
*Why:* D-17 makes WS best-effort (GF-4). Publish-before-ack gives at-least-once to WS, which clients can dedupe. Publish-after-ack would lose WS data on any crash.

**GF-104 — Presence write path.** `presence.Tracker.Observe(ctx, obs []Observation, now)`, called from `Publisher.Publish` with the `Stored` messages only. For each distinct `device_id` in the flush, one pipelined command `SET device:<id> online EX <ttl> GET`. Duplicate device rows within one flush are coalesced into one command, since the TTL refresh is identical within the flush.
- Transition rule: publish `presence.changed online` if the old value is absent (`redis.Nil`) **or** the in-memory state is `offline`. Otherwise no event.
- Every observed device updates the in-memory entry: `state=online`, `lastSeen = max(lastSeen, ServerTS)`.
- Only `Stored` (durable, paired) points refresh presence. Accuracy-dropped points do not, since they are not positions. This keeps Redis and `vehicles_latest` in agreement.
*Why:* the roadmap says "per message", but one command per device per flush gives the same TTL and transition behavior with fewer round trips. This is a deliberate refinement; the user may redirect it.

**GF-105 — `presence.changed` payload and relay.** Payload shape is fixed in GF-101. `vehicle_id` is not on the wire. The device id is the D-20 key, and resolving device→vehicle would add a pairing lookup to the hot path. P8 and P9 map device→vehicle via pairings. Relay: to the `fleet_id` room only. Drivers never receive it (GF-116).
*Why:* keeps the accept path free of extra queries. This narrows the "device id, vehicle id, fleet, state, ts" list in the roadmap prompt; the user may redirect it.

**GF-106 — Offline sweeper.** `presence.Sweeper` runs as a goroutine in the ingest process (`services/ingest/main.go`), ticking every 15 s (`const SweepInterval`). Joins shutdown after `consumer.Run` returns (GF-118). Each sweep:
1. `cutoff = now − FLEETOPS_PRESENCE_TTL`.
2. `SELECT v.paired_device_id, vehicles_latest.fleet_id, vehicles_latest.server_ts FROM vehicles_latest JOIN vehicles v ON v.id = vehicles_latest.vehicle_id WHERE vehicles_latest.server_ts < $1 AND v.paired_device_id IS NOT NULL` with `cutoff`. (`vehicles_latest` has no `device_id` column — migration 007 — so the device comes from the vehicle's current pairing; an unpaired vehicle's stale row emits nothing, matching D-20's device-keyed presence.)
3. For each row: emit `offline` iff the in-memory entry is absent or `state != offline`, **and** `entry.lastSeen < cutoff` (race guard: a flush that committed after the query snapshot wins). Set `state=offline` before publishing, so the event is emitted once per episode.
Map lifecycle: entries are created on observe and never evicted. The map is bounded by device count in the fleet. It is in-memory only and resets on restart. On restart, each already-stale device emits one `offline` at the first sweep (accepted noise; it corrects clients that were online before the restart). Lock held across the publish calls (1 s budget), so flush and sweep emissions are ordered inside the process.
*Why:* the DB is the source of truth for offline (D-20). The race guard closes a stale-snapshot window without a transaction.

**GF-107 — Presence TTL.** `FLEETOPS_PRESENCE_TTL`, default `120s`, range `30s`–`600s`, Go duration (GF-80 style). Used as the Redis `EX` and as the sweeper threshold, so offline in the DB and key expiry agree. Invalid values make `config.Load` fail, naming the var only.

**GF-108 — Hub subscriptions (GF-4, D-17).** One go-redis PubSub with PSUBSCRIBE `positions.batch.*` and `presence.changed.*`. Fleet is parsed from the channel name. NATS core `Subscribe` (not queue groups, so every hub instance sees every event) for `jobs.events.*` and `alerts.*`. Fleet is parsed from the subject. The envelope's `fleet_id` must match the subject fleet, or the message is dropped and counted (`DroppedEnvelope`). Reconnect: both libraries reconnect on their own. Messages published during a gap are lost (GF-4 best-effort). The hub has no persistence.

**GF-109 — NATS envelope contract (P10/P11 publishers).** Publishers send a JSON object. It carries `type` (`"job.updated"` on `jobs.events.<fleet>`, `"alert.raised"` on `alerts.<fleet>`) and `fleet_id` matching the subject. The hub checks the type family against the subject family, then forwards the body verbatim. `job.updated` must carry `driver_id` (the assignee's user id as a JSON number) for the driver filter (GF-116). Nothing publishes these in P7; tests publish synthetic events.

**GF-110 — Auth and upgrade.** Route: `GET /stream?fleet=<id>`, stdlib `net/http`, no Echo.
Pre-upgrade, in order:
1. `fleet` missing, non-integer, or ≤ 0 → HTTP 400, no upgrade.
2. Room at cap (GF-112) → HTTP 503 (body `room full`), no upgrade.
3. `websocket.Accept` with `OriginPatterns` = constant `localhost:10012`, `127.0.0.1:10012`, `localhost:10013`, `127.0.0.1:10013` (the reserved vite dev ports). Other origins are rejected by the library.

Post-upgrade:
4. The read limit is 4 KiB. The first frame must be a text frame within `AuthWindow` (5 s, constant) of `{"type":"auth","token":"…"}`. A timeout, a binary frame, a bad shape, or any `type` other than `auth` → close 4401 `unauthorized`.
5. `Verifier.Verify(token)` (`internal/auth`, HS256, `exp` required). Failure → close 4401 `unauthorized`.
6. Claims `fleet_id` ≠ `?fleet` → close 4403 `fleet mismatch`.
7. Role not in {`admin`, `manager`, `dispatcher`, `driver`} → close 4401 `unauthorized`.
8. Success: send control frame `{"type":"auth.ok","fleet_id":<int64>,"role":"<role>"}`, join the room, arm the token timer (GF-115).

The token never appears in the URL, in logs, or in close reasons.
*Why:* D-21 (first message, not URL); close codes follow D-21 for auth and 4403 separates fleet mismatch so operators can tell them apart. Pre-upgrade refusals must be HTTP because a 503 after 101 is impossible.

**GF-111 — Close codes and mechanics.** Every application close uses `Conn.Close(code, reason)` with reason ≤ 125 bytes:
- 4401 `unauthorized` (auth failure, role, expiry: `token expired`)
- 4403 `fleet mismatch`
- 4408 `slow consumer` (GF-113)
- 1001 `server shutdown` (GF-118)

Pong-timeout eviction uses `CloseNow()` (the peer is unresponsive, so no handshake; GF-114).
*Why:* the library accepts 3000–4999 custom codes; `CloseNow` avoids a 5 s handshake wait against a dead peer.

**GF-112 — Rooms and cap.** Rooms are keyed by `fleet_id`. Under the hub mutex: check `count < RoomCap` and increment the reservation **before** `Accept`. Pre-auth connections count toward the cap (strict bound). Release on any teardown path (deferred). Default `RoomCap` = `FLEETOPS_WS_ROOM_CAP`, default 2000. Refusal is HTTP 503 (GF-110 step 2). Rooms never cross fleets. Tests inject a small cap.
*Why:* reserve-before-Accept is race-free; counting pre-auth conns bounds memory (§13 risk) at the cost of a 5 s slot hold for silent clients.

**GF-113 — Connection send path.** After auth, the hub calls `conn.CloseRead(ctx)`, which discards client frames (the library closes with 1008 on any data frame). One writer goroutine per connection drains a bounded channel of 256 frames. Broadcast enqueues without blocking. A full queue → close 4408 `slow consumer` and counter `EvictedSlow`. Each write has a 5 s deadline. Frames are immutable `[]byte` shared across the room.
*Why:* a non-blocking broadcast keeps one slow client from stalling the fleet; the bounded queue is the §13 memory bound.

**GF-114 — Ping and pong.** The hub is the pinger. A ticker at `PingInterval` (20 s, constant) per connection. Each tick calls `conn.Ping(ctx)` with a `PingInterval` timeout. On success, `lastPong = now`. On each tick, if `now − lastPong ≥ PongEvictAfter` (60 s, constant), `CloseNow()`, counter `EvictedPong`. Clients answer pings only while reading, which the coder client does during `Read`.
*Why:* a tick-based check gives eviction at ~60 s after the last pong, and reuses the library ping/pong.

**GF-115 — Token expiry.** After auth, `time.AfterFunc(time.Until(exp), close 4401 "token expired")`. The timer is stopped on teardown. A token whose `exp` has passed fails `Verify` (GF-110 step 5).
*Why:* D-03 and D-21.

**GF-116 — Driver boundary (P7).** Drivers authenticate, occupy a slot, and join their fleet room. They receive only `job.updated` whose `driver_id` equals `claims.Subject` parsed as int64. They never receive `position.batch`, `presence.changed`, or `alert.raised`. P7 does no job-table lookup; ownership rides on the envelope (GF-109). P11 makes publishers set `driver_id`. Managers, dispatchers, and admins receive all four fleet event types.
*Why:* implements D-21 and README §8 now, without P10/P11 jobs code. The boundary is recorded so P11 knows the envelope contract.

**GF-117 — Client-facing envelopes.** All frames are JSON text. Server-to-client:
- `auth.ok`: `{"type":"auth.ok","fleet_id":N,"role":"…"}` (control, GF-110)
- `position.batch`: GF-101 body, verbatim
- `presence.changed`: GF-101 body, verbatim
- `job.updated` and `alert.raised`: publisher body, verbatim (GF-109)
- Pong and close are protocol-level frames.

Times are RFC3339Nano UTC. The four event types match README §6.

**GF-118 — Process wiring and shutdown.**
- `services/ws-hub/main.go`: `run(ctx) error`; `-addr` default `127.0.0.1:10011`; the `log.Fatalf` pattern of ingest.
- Boot order: `config.Load` → NATS connect → Redis client, `Ping` (fail-fast at boot) → `auth.NewIssuer` → Redis PSUBSCRIBE → NATS subscribe → `http.Server` listen.
- Shutdown on SIGINT/SIGTERM: `http.Server.Shutdown` stops new upgrades → cancel the base context (every conn goroutine sends close 1001 `server shutdown`) → wait up to 5 s for conns → PubSub close → NATS unsubscribe and close → Redis client close.
- The ingest process boots in this order: config → DB → NATS → Redis → Tracker → Publisher → sweeper → existing boot. On shutdown: consumer returns → sweeper cancelled and joined → Redis closed → existing closes.
*Why:* mirrors GF-94 ordering; fail-fast at boot and tolerant at runtime (GF-103).

**GF-119 — Config.** `config.Load` gains:
- `REDIS_URL`: required; non-empty; scheme `redis` or `rediss`; host present. Error names `REDIS_URL` only.
- `FLEETOPS_WS_ROOM_CAP`: integer `1`–`10000`, default `2000`.
- `FLEETOPS_PRESENCE_TTL`: Go duration `30s`–`600s`, default `120s` (GF-107).

`Load` stays one all-or-nothing call for every service (as today). Consequence: the API and the hub also need `DATABASE_URL`, `JWT_SECRET`, `DEVICE_KEY_SALT`, and `REDIS_URL`. CLAUDE.md's API run line gains `REDIS_URL`. No env for chunk size, timers, or sweep interval (GF-120).
*Why:* D-22 requires optional tunables only where there is a tuning reason; the others are constants (GF-120).

**GF-120 — Constants, not tunables.** `PositionBatchMax = 200`, `AuthWindow = 5s`, `PingInterval = 20s`, `PongEvictAfter = 60s`, `SweepInterval = 15s`, `sendQueue = 256`, write deadline `5s`, publish budget `1s` per stage, shutdown grace `5s`. The hub `Options` struct exposes `AuthWindow`, `PingInterval`, and `PongEvictAfter`, with the defaults above, so tests can shorten them. Services set the defaults; no env reads.
*Why:* the roadmap fixes these values; no D-22 tuning reason exists for them.

**GF-121 — Metrics.** Ingest adds two counters to the GF-93 `Metrics` struct, registered on its private registry with no endpoint: `fleetops_fanout_publish_failures_total{kind="position_batch"|"presence"}` and `fleetops_presence_transitions_total{state="online"|"offline"}`. `TestMetricsRegisteredNames` gains the new names. The hub keeps atomic `Counters` (`ConnsOpen`, `RefusedCap`, `AuthFailed`, `EvictedPong`, `EvictedSlow`, `RelayedFrames`, `DroppedEnvelope`) read only by tests. The `/metrics` endpoint and the Prometheus registry in the hub are deferred to P22.
*Why:* the ingest pattern exists; the hub has no metrics surface yet.

**GF-122 — Test infrastructure.** `internal/testinfra`:
- `const RedisImage = "redis:7"`
- `NewRedisURL(t testing.TB) string`: shared container per process, `sync.Once`, copies `nats.go` (`TerminateRedis()` for `TestMain`).
- `NATSURL(t testing.TB) string`: exported; core-NATS tests need it without JetStream.
- Ports are OS-assigned (GF-13). Tests use unique device and fleet ids per test, so the shared Redis is not flushed between tests.
- `TestMain` in `internal/hub`, `services/ws-hub`, and `internal/ingest` calls `TerminateRedis()` after `m.Run()`.

**GF-123 — Dependencies.** Added to `go.mod`, nothing else:
- `github.com/redis/go-redis/v9` v9.23.0
- `github.com/coder/websocket` v1.8.15
- `github.com/testcontainers/testcontainers-go/modules/redis` v0.44.0

Rejected: `gorilla/websocket` (v1.5.3 is not the latest in its module; manual close and control-frame handling). Rejected: NATS for position fanout (D-17).
*Why:* coder/websocket has ctx-aware Read/Write/Ping, `Close(code, reason)` for 4401, a documented concurrency model, and `Dial` for tests.

**GF-124 — Residual risks (accepted in P7).**
- Redis outage: ≤ 2 s extra per flush; WS misses batches (DB is the record). Mitigation: P22 metric.
- Online transitions missed while a hub is disconnected are not replayed. No presence snapshot on connect. Closed by the P8 `/map/vehicles` snapshot.
- Presence is WS-only and not persisted (accepted in roadmap §4).
- Restart noise: one offline per already-stale device (GF-106).

**GF-125 — Frame size.** A 200-point `position.batch` ≈ 52 KB (≈ 260 B per point). Client libraries default to a 32 KiB read limit, so test clients call `SetReadLimit(1 << 20)`. The hub's read limit (4 KiB) applies to inbound auth frames only; the hub write path has no limit. Browsers have no such limit.
*Why:* prevents a false test failure and documents the size budget.

## Validation

Runnable on this host with explicit pass conditions. Preconditions: Go 1.27.3 and Docker available (G0).

1. `make fmt-check` exits 0.
2. `make lint` exits 0 (golangci-lint v2.15.0).
3. `make test` exits 0; `go test -race -count=1 ./internal/fanout/... ./internal/presence/... ./internal/hub/... ./internal/ingest/... ./services/ws-hub/...` exits 0.
4. Roadmap Validation 1 (browser-free client receives coalesced batches): `TestWSHubCoalescedBatchEndToEnd` passes.
5. Roadmap Validation 2 (connection at cap refused): `TestHubRefusesConnectionAtRoomCap` passes; the refused dial returns HTTP 503.
6. Roadmap Validation 3 (silent device → `presence.changed offline` once): `TestSilentDeviceOfflineOnceEndToEnd` passes; exactly one offline event observed.
7. Chunking property: `TestChunkPropertyEveryPointOnceOrderMax200` passes with `-rapid.checks=1000`.
8. 1,000 connections: `TestThousandSyntheticConnections` passes under `-race` in ≤ 60 s wall (`go test -race -run TestThousandSyntheticConnections -v ./internal/hub` reports the time).
9. Redis and WS integration suites pass: `go test -race -count=1 -run 'Redis|Presence|Sweep|Hub|WSHub' ./...`.
10. Scope fences: `git diff --name-only main...HEAD` contains only paths under `internal/fanout/`, `internal/presence/`, `internal/hub/`, `internal/ingest/`, `internal/config/`, `internal/testinfra/`, `services/ingest/`, `services/api/` (test env only), `services/ws-hub/`, `CLAUDE.md`, `go.mod`, `go.sum`, `specs/features/07-fanout-ws.md`. It must not contain `README.md`, `deploy/docker-compose.yml`, `deploy/PORTS.md`, `specs/constitution/`, or anything under `db/`.
11. `go.mod` diff adds exactly the three GF-123 requires (plus `go.sum` and indirect lines).
12. No secrets: `git diff main...HEAD` has no literal `JWT_SECRET=`, `DEVICE_KEY_SALT=`, or password values; tokens appear only in test helpers.
13. CLAUDE.md: `wc -l CLAUDE.md` stays under 200; the diff only adds rows and run lines (D-40).

## Testing depth

| Layer | Depth | Notes |
|---|---|---|
| Chunker (`PlanPositionBatches`) | Property test, `pgregory.net/rapid`, 1,000 checks, no infra | GF-102 properties; plus boundary example 200/201 |
| Redis fanout | Integration, real Redis 7 (`testinfra.NewRedisURL`) | Round trip; dead-endpoint error within budget; no mocks |
| Presence Tracker | Integration, real Redis | SET … GET transitions; in-memory state rules |
| Sweeper | Integration, real PostGIS (`vehicles_latest`) + real Redis + fake clock | Once-per-episode; race guard; boot burst |
| Ingest publish hook | Integration, real NATS JetStream + PostGIS + Redis | Stored-only publish; ack not withheld on Redis down |
| Config | Unit, injected lookup (existing pattern) | Names var, never value |
| Hub lifecycle | Integration, real WS clients (`coder/websocket` `Dial`) over `httptest`-style listener, real Redis + NATS | Auth, close codes, cap, ping, slow consumer, expiry; short timers via `Options` |
| Hub relay | Integration, real Redis PUBLISH and NATS core publish | Verbatim relay; driver filter; envelope drop |
| 1,000 synthetic connections | Integration, `-race`, named test `TestThousandSyntheticConnections` | Budget ≤ 60 s; goroutines return to baseline +50 |
| End-to-end | Integration: ingest Consumer → Redis → hub → WS client | Roadmap Validation 1 and 3 |
| Race | `go test -race` on fanout, presence, ingest, hub, ws-hub | CI gate (tech-stack §9) |

Test helpers: `testinfra.NewRedisURL` (GF-122), `testinfra.NewJetStream`, `testinfra.NATSURL`, `testinfra.StartPostgis`/`NewMigratedDB`, and an in-test token helper using `auth.NewIssuer` with the test secret. No mocks of infrastructure.

## SPEC-AMEND repeat

Repeats D-17 (mission §7, P7 row: Redis carries `position.batch` and `presence.changed`; NATS carries `jobs.events` and `alerts`) and D-22 (mission §7, additive `FLEETOPS_*` tunables; §12 names stay required) as binding. No new SPEC-AMEND row: presence payload, close codes, the chunk constant, and the driver boundary are GF-level decisions, not constitution amendments. D-40 applies: CLAUDE.md is updated in the same branch for `FLEETOPS_WS_ROOM_CAP`, `FLEETOPS_PRESENCE_TTL`, and the API and ws-hub `go run` lines (which now include `REDIS_URL`).

Known conflict recorded, not edited (R-8): README §12 lists ws-hub at local port `8090`; `deploy/PORTS.md` and tech-stack §4 reserve host `10011`. P7 binds `127.0.0.1:10011` per PORTS.md. The README is changed only through the spec process.

## Out of scope

- P8: `/map/vehicles`, the simulator, and the device→vehicle map.
- P9: web UI and the driver PWA.
- P10+: jobs and alerts producers. The hub relays `job.updated` and `alert.raised` generically, tested with synthetic publishes only.
- P22: `/metrics`, Prometheus endpoint, dashboards.
- P23: k6 and the 10k-connection test.
- Migrations (none needed; presence is not persisted).
- Compose changes, README and PORTS.md edits, constitution edits.
- Per-job subscription and job-table lookups for drivers (P11).
- Presence snapshot on connect (P8).
- Cloud profile and in-process fanout (D-41).
