# Feature 05 — Batch writer core (P5)

Status: approved plan, persisted 2026-10-25. Branch `phase-5-batch-writer`.
Source plan: `/root/.claude/plans/p5-plan-spicy-taco.md` (approved). Binding constitution: `specs/constitution/roadmap.md` §1 (R-1…R-9) and §3 P5, `specs/constitution/tech-stack.md` §3, §5, §6.2, §8, §9, §11 (D-14, D-15, D-18, D-19, D-22), `specs/constitution/mission.md` §7 (SPEC-AMEND rows), `AGENTS.md`, `CLAUDE.md`.

This spec is written before any P5 code (R-1, SDD). Implementation starts at Plan task group 1.

## Scope

Roadmap P5 (`specs/constitution/roadmap.md` §3), verbatim:

> **P5 — Batch writer core** · D-14 (parent table), D-15, D-18, D-19, D-22 · README §3, §7, §13
> - Scope: buffer flushes at 1 s after first message or 500 messages, and drains on shutdown; `accuracy > 50 m` dropped before buffer and counted; at flush, `server_ts` older than 60 s acked and counted; one `UNNEST` batch upsert into `vehicles_latest` guarded by `EXCLUDED.server_ts > vehicles_latest.server_ts`; device → vehicle resolved per flush through `vehicles.paired_device_id`; unpaired devices acked and counted, not stored; `positions` insert `ON CONFLICT DO NOTHING`; heading smoothing (§13, EMA per vehicle at flush); tunables read from `FLEETOPS_*` with defaults (D-22). Partitions for tests are created by the test helper; runtime ensure-ahead is P6.
> - Validation: throughput smoke at 100 msg/s on local PG; no row locks held across flushes.
> - Testing: **property tests on the signature problems**: (a) flush semantics — triggers fire at 1 s or 500, batch never exceeds 500, no message lost or duplicated across flushes; (b) batch upsert — any permutation of a batch yields the same `vehicles_latest`, and replaying a batch is a no-op; both against a PostGIS testcontainer. Go unit tests for the buffer state machine.

P5 delivers: migration `007` (`positions` parent and `vehicles_latest`); `FLEETOPS_*` optional tunables in `internal/config`; the buffer state machine (`internal/ingest`); the flush write path (one transaction per flush: device resolution, `positions` insert, `vehicles_latest` UNNEST upsert, heading EMA); a partition test helper; property tests on the two signature problems; a throughput smoke; a CLAUDE.md env table entry for the three new names.

Not in P5: the JetStream consumer loop, ack, NAK, `MaxDeliver`, partition ensure-ahead, retention, presence, Redis fanout, `position.batch` publish (P7 seam only), WS hub, metrics, simulator, compose changes, README or `deploy/PORTS.md` edits, port changes, new infrastructure dependencies. `GPS_RAW` is unchanged (GF-59 stands; `MaxBytes` stays 256 MiB).

## Plan

Task groups in build order. Each group ends in a commit on `phase-5-batch-writer`. Tests live beside code. Test names are exact.

### 1. Migration 007 (`db/migrations/007_positions_vehicles_latest.up.sql` and `.down.sql`)

- Up: create `positions` (partitioned parent, columns and index per GF-68) and `vehicles_latest` (exact README §5 DDL, GF-69). No children, no DEFAULT partition.
- Down: drop `vehicles_latest`, then `positions`.
  - Tests: existing `TestMigrationFilesArePaired` and `TestMigrationFilesAreTransactionSafe` (db package) cover the file pair and GF-5 keywords. `TestMigrationRoundTrip` (`db/roundtrip_test.go`) is updated from version 6 to 7 (its hardcoded version at lines 41 and 59) and its `assertSchema` table list gains `positions` and `vehicles_latest`.
  - New test `TestPositionsSchemaIsPartitionedParent` (`db/positions_schema_test.go`, PostGIS testcontainer via `testinfra.NewMigratedDB`): `pg_partitioned_table` row for `positions` with `partstrat = 'r'`; primary key on `(server_ts, msg_id)`; no partition with `default` bound; `positions.vehicle_id` FK to `vehicles(id)`; `vehicles_latest` columns and both indexes exist.

### 2. Config (`internal/config/config.go`)

- Add `FlushInterval time.Duration`, `FlushMax int`, `AccuracyMaxM float64` read from `FLEETOPS_FLUSH_INTERVAL`, `FLEETOPS_FLUSH_MAX`, `FLEETOPS_ACCURACY_MAX_M` with defaults 1s, 500, 50 (GF-80). §12 required names untouched.
  - Tests (`internal/config/config_test.go`, unit, through the existing `load(lookup)`): `TestLoadFlushDefaultsWhenUnset`; `TestLoadFlushAcceptsOverrides` (`250ms`, `200`, `12.5`); `TestLoadFlushEmptyIsDefault` (each variable set to `""` yields the default); `TestLoadFlushRefusesInvalid` (table: `"abc"`, `"0s"`, `"50ms"`, `"11s"` for interval; `"0"`, `"5001"`, `"-1"`, `"1.5"` for max; `"0"`, `"-3"`, `"NaN"`, `"Inf"`, `"10000.5"` for accuracy); `TestLoadFlushErrorNeverContainsValue` (error names the variable, does not contain the offending value).

### 3. Partition test helper (`internal/testinfra/partitions.go`)

- `EnsurePositionsPartitions(t testing.TB, db *sql.DB, from, to time.Time)` creates `positions_YYYY_MM` for every UTC calendar month from `from` through `to` inclusive, using `CREATE TABLE IF NOT EXISTS ... PARTITION OF positions FOR VALUES FROM (...) TO (...)` (GF-81). Test-only; production never calls it (P6 owns runtime ensure-ahead).
  - Tests: `TestEnsurePositionsPartitionsIdempotent` (call twice over a 3-month range; three children exist with the right bounds; `pg_inherits` count = 3).

### 4. Counters and buffer state machine (`internal/ingest/counters.go`, `internal/ingest/buffer.go`)

- `Counters` struct (GF-83): atomic `Stored`, `DroppedAccuracy`, `Stale`, `Unpaired`. Shared by pointer between `Buffer` and `Writer`. No metrics registration (P6, P22).
- `Buffer` (GF-70 to GF-73): `NewBuffer(cfg config values, c *Counters) *Buffer`. Methods:
  - `Offer(m Message, now time.Time) (buffered bool, batch []Message)`: pre-buffer accuracy drop, append, max trigger.
  - `Tick(now time.Time) []Message`: interval trigger.
  - `Deadline() (time.Time, bool)`: when the interval trigger arms.
  - `Drain() []Message`: shutdown.
- Tests (unit, stdlib, no infra), named:
  - `TestBufferOfferBuffersAcceptedMessage`
  - `TestBufferAccuracyAboveMaxDroppedBeforeBuffer` (51 m dropped; `DroppedAccuracy` incremented; buffer empty; `Deadline` not armed)
  - `TestBufferAccuracyExactlyMaxKept` (50.0 kept)
  - `TestBufferDroppedMessageDoesNotArmTimer`
  - `TestBufferArmsOnFirstBufferedMessage` (second message does not move the deadline)
  - `TestBufferTickBeforeDeadlineReturnsNil`
  - `TestBufferTickAtDeadlineFlushes` (deadline inclusive: `now == armedAt + 1s` flushes)
  - `TestBufferFlushesAtMaxExactly500` (500th `Offer` returns a batch of length 500; 499 returns nil)
  - `TestBufferNeverReturnsMoreThanMax` (1,200 offers: every batch length ≤ 500; batches are 500, 500, 200 after drain)
  - `TestBufferTimerRearmsAfterFlush` (after a max flush, the next first message arms a new deadline)
  - `TestBufferDrainReturnsAllAndResets` (shutdown path; order preserved; second Drain returns nil)
  - `TestBufferDrainEmptyReturnsNil`
  - `TestBufferConcurrentOfferTickDrain` (`-race`: goroutines call Offer, Tick, Drain; total returned messages across all calls equals accepted count; no duplicate or lost entries)
- Property (see group 7): `TestFlushSemanticsProperty`.

### 5. Pure helpers: stale rule and heading EMA (`internal/ingest/smooth.go`)

- `isStale(serverTS, now time.Time) bool`: `now.Sub(serverTS) > 60*time.Second` (GF-72).
- `headingStep(prev, raw float64) float64` and the per-vehicle `smoothState` apply rule (GF-78).
  - Tests: `TestIsStaleBoundary` (age exactly 60 s is kept; 60 s + 1 µs is stale; a future `server_ts` is kept); `TestHeadingEMAWrapsAcrossNorth` (`prev = 359`, `raw = 1` → `0`; `prev = 1`, `raw = 359` → `0`; never produces a value outside `[0, 360)`); `TestHeadingEMAHalfStep` (`prev = 90`, `raw = 110` → `100`); `TestHeadingEMASeedsFromFirstMessage`.

### 6. Flush write path (`internal/ingest/writer.go`)

- `Writer` with `NewWriter(db *sql.DB, c *Counters) *Writer` and `Flush(ctx context.Context, batch []Message, now time.Time) (FlushResult, error)` (GF-74 to GF-79, GF-84). `Flush` is serialised by a mutex. One transaction per call:
  1. Stale filter with `now` (GF-72).
  2. One device resolution query for the distinct device ids of the non-stale messages (GF-75). Messages without a row, or with `FleetID` ≠ `vehicles.fleet_id`, are unpaired.
  3. Sort the paired messages by `(server_ts, msg_id)` ascending. The last message per vehicle wins the `vehicles_latest` row. Heading EMA runs over the same order (GF-78).
  4. `positions` INSERT for every paired, fresh message, `ON CONFLICT (server_ts, msg_id) DO NOTHING` (GF-77).
  5. One `vehicles_latest` UNNEST upsert, one row per vehicle, guarded by `EXCLUDED.server_ts > vehicles_latest.server_ts` (GF-76).
  6. `COMMIT`. Counters are incremented and the EMA state map is updated only after `COMMIT` succeeds.
  - On any error: `ROLLBACK`, return the error and the untouched batch, no counter change, no EMA change (GF-74).
  - Tests (integration, PostGIS testcontainer via `testinfra.NewMigratedDB`, fixtures through the P3 registry service: `CreateVehicle`, `IssueDevice`, `SetPairing`; revoke through the P3 revoke method):
    - `TestFlushStoresPairedPositions` (3 vehicles, 10 messages: `positions` has 10 rows; `vehicles_latest` holds the latest `server_ts` per vehicle; `geom` equals the lng/lat given)
    - `TestFlushDropsUnpairedRevokedAndFleetMismatch` (unpaired device, revoked paired device, message whose `FleetID` differs from the vehicle's fleet: each acked by count, `Unpaired` = 3, none in `positions`)
    - `TestFlushDropsStaleAndCountsIt` (one message 61 s old, one 60 s old: first counted in `Stale` and absent, second stored)
    - `TestFlushRollsBackOnError` (no partition for the month: `Flush` returns an error; `vehicles_latest` unchanged; counters unchanged; batch returned intact)
    - `TestFlushNoRowLocksHeldAcrossFlushes` (after each `Flush` returns: `pg_stat_activity` has zero `idle in transaction` rows for the test database, and `pg_locks` has zero rows for `vehicles_latest` or `positions` from other backends)
    - `TestPositionsDuplicateIgnored` (same batch flushed twice: row count unchanged; a duplicate inside one batch stores one row)
    - `TestVehiclesLatestOlderNotApplied` (a flush with `server_ts` older than the stored row leaves the row unchanged)
    - `TestFlushHeadingSmoothedInVehiclesLatestRawInPositions` (positions keeps raw heading; `vehicles_latest.heading_deg` equals the EMA value)

### 7. Property tests on the signature problems

- `internal/ingest/buffer_property_test.go`:
  - `TestFlushSemanticsProperty` (pure buffer, virtual clock, `pgregory.net/rapid` default checks). Generator: 1–2,000 events; each event advances virtual time by 0–1,500 ms, carries an accuracy in `[0, 100]`, and may repeat a previous message (at-least-once redelivery). Per step, call `Tick(now)`, and with a generated probability `Offer`. Asserts:
    - every batch length is in `[1, 500]`;
    - a batch returned by `Offer` has length exactly 500;
    - a batch returned by `Tick` is emitted only when `now ≥ armedAt + 1s`, where `armedAt` is the deadline read before the call;
    - after every step, if the buffer is non-empty then `Deadline() > now` (no buffered message waits past its deadline);
    - conservation: the multiset of all emitted messages (including the final `Drain`) equals the multiset of accepted offers; accuracy drops are exactly the offers with accuracy > 50.
- `internal/ingest/writer_property_test.go` (one `NewMigratedDB` per test function; per check `TRUNCATE positions, vehicles_latest` and a fresh `Writer`; fixtures created once: 1 fleet, 10 vehicles, 10 paired devices, one partition for the generated month; `server_ts` drawn from a fixed UTC base window of 90 s so some messages are stale at the flush `now`):
  - `TestFlushSemanticsDBProperty`: random streams through `Buffer` then `Writer.Flush` on PostGIS. Asserts: sum of `Stored` + `Stale` + `Unpaired` + `DroppedAccuracy` equals offers; `positions` contains exactly the distinct `msg_id`s of stored messages (no loss, no duplicate row); no `positions` row exists for a stale or unpaired message; each vehicle's `vehicles_latest.server_ts` equals the max stored `server_ts` for that vehicle.
  - `TestBatchUpsertPermutationProperty`: random batch (1–200 messages, 1–10 vehicles, unique UUIDv7 `msg_id`s, `server_ts` within the base window). Flush once in the original order with a fresh `Writer`, snapshot (`vehicles_latest` and `positions` rows ordered by primary key); TRUNCATE; flush a permutation drawn from `rand.New(rand.NewSource(seed))` with `seed` from `rapid.Uint64()` on a fresh `Writer`; snapshots must be equal.
  - `TestBatchUpsertReplayProperty`: same batch flushed twice on the same `Writer`; the second flush leaves `vehicles_latest` and `positions` byte-identical to the first (guard and DO NOTHING), and heading EMA state is unchanged (same `vehicles_latest.heading_deg`).

### 8. Throughput smoke (`internal/ingest/smoke_test.go`)

- `TestIngestThroughputSmoke100PerSecond`: single-goroutine driver (the P6 consumer role) on the PostGIS testcontainer for 10 s. A `time.Ticker` at 10 ms offers one message per tick (100 msg/s), accuracy 5 m, paired device, `server_ts` = wall clock at offer. The loop calls `Tick(now)` every iteration and flushes each batch through `Writer.Flush`. After 10 s, `Drain` and flush. Measures and logs (`t.Logf`) msgs/s, flush count, max batch, p50 and p99 flush latency.
  - Pass conditions (asserted): `Stored` equals offers (zero loss); `Stale` = 0; `Unpaired` = 0; `DroppedAccuracy` = 0; every `Flush` returns nil; max batch ≤ 500; no row-lock or idle-in-transaction rows after each flush (as in group 6). Latency is reported, not gated (P23 gates it).

### 9. CLAUDE.md (`CLAUDE.md`, one table)

- Add an "Optional tunables" table with the three names and defaults (`FLEETOPS_FLUSH_INTERVAL` 1s, `FLEETOPS_FLUSH_MAX` 500, `FLEETOPS_ACCURACY_MAX_M` 50), and the parse rule (empty = default, invalid = refused). The §12 required list stays unchanged. File stays under 200 lines (currently 81).

### 10. Gates and validation run

- Run every Validation item on the host and record the output in the step report. Fix any failure before the branch is ready.

## Requirements

Binding and carried verbatim from `tech-stack.md` §11 and `mission.md` §7: D-14, D-15, D-18, D-19, D-22. R-6 does not apply (no endpoint in P5). New decisions GF-68 … GF-84 are recorded here only. **Zero open decisions:** every free choice below is decided with rationale.

### Reused — binding

- **D-14** — `positions` parent `PARTITION BY RANGE (server_ts)`; children `positions_YYYY_MM`; no DEFAULT partition; ensure-ahead and retention are P6. Insert into a missing partition is an error (P6 maps it to metric + NAK). *Rationale (tech-stack): monthly partitions and 12-month retention; a DEFAULT partition blocks future partition creation.* **SPEC-AMEND** (mission §7: P5 row).
- **D-15** — gateway-stamped `msg_id` (UUIDv7) and `server_ts` (P4, GF-57). Unique key `(server_ts, msg_id)` on the `positions` parent (PG propagates it to every partition, GF-68); inserts `ON CONFLICT (server_ts, msg_id) DO NOTHING`. *Rationale (tech-stack): redelivery is safe and the DB-backed property has a real invariant.*
- **D-18** — flush at 1 s after the first buffered message or 500 messages, whichever comes first; flush (drain) on shutdown. Dropped before buffering: `accuracy > 50 m`. Dropped at flush: `server_ts` older than 60 s (acked and counted). **SPEC-AMEND** (mission §7: interpretation, P5). Exact comparisons in GF-71, GF-72, GF-73.
- **D-19** — one `UNNEST` batch upsert into `vehicles_latest` guarded by `EXCLUDED.server_ts > vehicles_latest.server_ts`; device → vehicle resolved per flush through `vehicles.paired_device_id`; unpaired devices acked and counted, not stored. *Rationale (tech-stack): flush-time resolution avoids a stale cache.* Exact SQL in GF-75 and GF-76.
- **D-22** — optional `FLEETOPS_*` tunables with defaults; §12 names stay required. P5 reads exactly three of the five D-22 names (`FLUSH_INTERVAL`, `FLUSH_MAX`, `ACCURACY_MAX_M`); `WS_ROOM_CAP` and `PRESENCE_TTL` are P7 and are not read in P5. **SPEC-AMEND** (mission §7: P5 row).
- **README §13** — heading smoothing (EMA per vehicle at flush, GF-78); ordering by `server_ts` (D-19). **Not a SPEC-AMEND**; it is binding README text.

### New in P5 (GF-68 … GF-84)

- **GF-68 — `positions` parent (migration 007, exact).** Table `positions`, `PARTITION BY RANGE (server_ts)`, columns in this order:
  - `msg_id UUID NOT NULL`
  - `fleet_id BIGINT NOT NULL` (no FK; see below)
  - `vehicle_id BIGINT NOT NULL REFERENCES vehicles(id)`
  - `geom geography(Point,4326) NOT NULL`
  - `speed_kmh REAL NOT NULL`
  - `heading_deg REAL NOT NULL` (raw device heading)
  - `accuracy_m REAL NOT NULL`
  - `device_ts TIMESTAMPTZ NOT NULL`
  - `server_ts TIMESTAMPTZ NOT NULL` (no DEFAULT: the gateway stamps it, GF-57; a DEFAULT would hide a missing stamp)

  Constraint: `PRIMARY KEY (server_ts, msg_id)`. A primary key on a partitioned table is a unique index that includes the partition key, and PG propagates it to every child, so this is the D-15 unique index. It is also the `ON CONFLICT` arbiter. No secondary index in P5 (reads are P8). No DEFAULT partition. No children in the migration.
  *FK policy:* `vehicle_id` references `vehicles(id)` with the default `NO ACTION`. PG 11+ supports a foreign key from a partitioned table to a regular table, and the FK check on every insert is cheap. `fleet_id` has no FK: it is always the vehicle's fleet, set by GF-75, and an FK on the hot path adds a lookup for no new guarantee. *Rationale: the roadmap requires vehicles to exist; the FK is the storage-level guard for that.*
  Down: `DROP TABLE positions`. Dev-only; forward-only once merged (GF-5).
- **GF-69 — `vehicles_latest` (migration 007, exact).** The README §5 DDL verbatim: `vehicle_id BIGINT PRIMARY KEY REFERENCES vehicles(id)`, `fleet_id BIGINT NOT NULL`, `geom geography(Point, 4326) NOT NULL`, `speed_kmh REAL`, `heading_deg REAL`, `device_ts TIMESTAMPTZ NOT NULL`, `server_ts TIMESTAMPTZ NOT NULL DEFAULT now()`; indexes `vehicles_latest_geom_gix` (GIST on `geom`) and `vehicles_latest_fleet_idx` (on `fleet_id`). *Rationale: README §5 is the shape; tech-stack §6.1 says DDL unchanged.* Both tables in one file: `007_positions_vehicles_latest.up.sql` and `.down.sql`, one transaction-friendly script each (GF-5).
- **GF-70 — buffer data structure and concurrency.** `Buffer` holds `pending []Message` and `armedAt time.Time`, guarded by one `sync.Mutex`. It owns no goroutine, no timer, and no I/O. The caller (the P6 consumer, and the shutdown path) drives it: `Offer` per message, `Tick(now)` when `Deadline()` has passed, `Drain` on shutdown. *Rationale: a mutex-guarded value is the smallest thing that is safe for the P6 consumer and the shutdown goroutine to share, and it makes the state machine testable without time or infra. Channels or a timer goroutine would hide the trigger logic behind scheduling.* Stored messages are `Message` values from `Stamp` (GF-57) with `ServerTS` and `DeviceTS` truncated to microseconds on `Offer` (`Truncate(time.Microsecond)`), because PostgreSQL `timestamptz` stores microseconds and the stale comparison must use the same value the DB stores. Caller supplies `now` (no internal clock).
- **GF-71 — pre-buffer accuracy drop.** Drop when `m.Position.Accuracy > AccuracyMaxM` (strict; `50.0` is kept, `50.0001` is dropped). A dropped message is not buffered, does not arm the timer, increments `DroppedAccuracy`, and is acked by the caller (P6). *Rationale (D-18): the drop is cheap and early; strict `>` matches the roadmap's wording "`accuracy > 50 m`".*
- **GF-72 — at-flush stale drop.** One clock read per flush (`now`). Drop when `now.Sub(m.ServerTS) > 60 * time.Second` (strict; age exactly 60 s is kept; a future `server_ts` is kept). A stale message increments `Stale`, is acked by the caller, and is not stored. *Rationale (D-18): "older than 60 s" is strict `>`; age is measured on `server_ts` (D-19, README §13), not device time.*
- **GF-73 — flush triggers.** The interval timer is armed by the first message that is *accepted into the buffer* (passed the pre-buffer drop), at that message's `now` (the caller's monotonic `Offer` time, never `server_ts` or `device_ts`). `Deadline() = armedAt + FlushInterval`. `Tick(now)` flushes when the buffer is non-empty and `!now.Before(deadline)` (inclusive). `Offer` flushes when `len(pending)` reaches `FlushMax` (equal, so a batch is never longer than `FlushMax`, and exactly `FlushMax` when it fires). The flush takes the slice under the mutex and resets `armedAt`, so a message arriving after a take arms a new deadline. A batch is handed back to the caller; the caller calls `Writer.Flush` outside the mutex. Drain returns everything and resets. *Rationale: "1 s after first message" is exactly the arm point; inclusive comparison matches "at 1 s"; a take-and-reset makes flush-in-progress trivially safe because the single consumer serialises flushes.*
- **GF-74 — flush transaction and failure.** `Writer.Flush` runs one transaction (`BEGIN` … `COMMIT`, default isolation): device resolution (GF-75), `positions` INSERT (GF-77), `vehicles_latest` UPSERT (GF-76). No `SELECT … FOR UPDATE`, no advisory locks, no row lock outside the statements. Commit ends the transaction, so no row lock is held across flushes; `TestFlushNoRowLocksHeldAcrossFlushes` asserts it. On any error: rollback, return the error and the untouched batch. Counters and the EMA state are updated only after `COMMIT`. P5 does not retry; the P6 consumer NAKs. *Rationale (D-16, D-19): one transaction keeps `positions` and `vehicles_latest` consistent; post-commit state updates keep the in-memory EMA and counters in step with the DB.*
- **GF-75 — device → vehicle resolution (per flush, one query).**
  ```sql
  SELECT d.id, v.id, v.fleet_id
  FROM devices d
  JOIN vehicles v ON v.paired_device_id = d.id
  WHERE d.id = ANY($1::bigint[]) AND d.revoked_at IS NULL
  ```
  `$1` is the distinct device ids of the non-stale messages (`pq.Array([]int64)`). A message is *paired* iff a row exists for its `DeviceID` and `m.FleetID == v.fleet_id`; otherwise it is *unpaired*, increments `Unpaired`, is acked by the caller, and is not stored. A NULL `paired_device_id` never matches (SQL NULL), so vehicles without a pairing are never selected; device ids in a batch are non-zero `int64` from the strict decoder. *Rationale and findings:* (1) `RevokeDevice` (P3, `internal/registry/devices.go:95`) does not clear `vehicles.paired_device_id` (its comment says pairings are "left untouched"), so the mirror can still point at a revoked device; `d.revoked_at IS NULL` closes that gap in the write path. (2) No code path changes `devices.fleet_id` (devices are issued, rotated, revoked, never moved), so the fleet check is a defence against a corrupted mirror, not a live case. Messages accepted before a revoke and flushed after it are unpaired and not stored (at most a 60 s window, counted).
- **GF-76 — `vehicles_latest` UNNEST upsert (exact).** One statement per flush, one row per vehicle (the winner: the last message per vehicle in the `(server_ts, msg_id)` order of GF-78; `ON CONFLICT` cannot touch the same row twice in one statement, so the Go side must dedupe per vehicle).
  ```sql
  INSERT INTO vehicles_latest (vehicle_id, fleet_id, geom, speed_kmh, heading_deg, device_ts, server_ts)
  SELECT u.vehicle_id, u.fleet_id,
         ST_SetSRID(ST_MakePoint(u.lng, u.lat), 4326)::geography,
         u.speed_kmh, u.heading_deg, u.device_ts, u.server_ts
  FROM unnest($1::bigint[], $2::bigint[], $3::float8[], $4::float8[], $5::float8[], $6::float8[],
              $7::text[]::timestamptz[], $8::text[]::timestamptz[])
       AS u(vehicle_id, fleet_id, lng, lat, speed_kmh, heading_deg, device_ts, server_ts)
  ON CONFLICT (vehicle_id) DO UPDATE SET
    fleet_id    = EXCLUDED.fleet_id,
    geom        = EXCLUDED.geom,
    speed_kmh   = EXCLUDED.speed_kmh,
    heading_deg = EXCLUDED.heading_deg,
    device_ts   = EXCLUDED.device_ts,
    server_ts   = EXCLUDED.server_ts
  WHERE EXCLUDED.server_ts > vehicles_latest.server_ts;
  ```
  `$7`/`$8` are RFC3339Nano UTC strings (`pq.StringArray`), cast to `timestamptz[]`; passing strings avoids `lib/pq` time-array support and keeps the microsecond value exact. Speed and heading are the winner's raw speed and EMA heading (GF-78). The guard is strict `>`: an equal `server_ts` does not overwrite, which makes a replay a no-op. *Rationale (D-19): one statement per flush (README §7 "no row locks held per message"); strict guard makes replay and redelivery safe.*
- **GF-77 — `positions` INSERT (exact).** One statement per flush over every paired, fresh message (duplicates included; `DO NOTHING` absorbs repeats within the statement and across flushes):
  ```sql
  INSERT INTO positions (msg_id, fleet_id, vehicle_id, geom, speed_kmh, heading_deg, accuracy_m, device_ts, server_ts)
  SELECT u.msg_id, u.fleet_id, u.vehicle_id,
         ST_SetSRID(ST_MakePoint(u.lng, u.lat), 4326)::geography,
         u.speed_kmh::real, u.heading_deg::real, u.accuracy_m::real, u.device_ts, u.server_ts
  FROM unnest($1::uuid[], $2::bigint[], $3::bigint[], $4::float8[], $5::float8[], $6::float8[],
              $7::float8[], $8::float8[], $9::text[]::timestamptz[], $10::text[]::timestamptz[])
       AS u(msg_id, fleet_id, vehicle_id, lng, lat, speed_kmh, heading_deg, accuracy_m, device_ts, server_ts)
  ON CONFLICT (server_ts, msg_id) DO NOTHING;
  ```
  Ten arrays map one-to-one onto the ten columns; every array is the same length (shorter arrays would NULL-pad and violate NOT NULL — verified on PG 16.4 during the self-gate). `positions.heading_deg` is the **raw** device heading (history fidelity); smoothing applies only to the latest view (GF-78). *Rationale (D-15): the conflict target is the PK that is the D-15 unique index; repeats are the expected at-least-once case.*
- **GF-78 — heading EMA (README §13).** Per-vehicle state `{heading float64, at time.Time}` in a `Writer` map, held in memory for the life of the process. Lifetime: the first accepted message seeds it (`heading = raw`). Each subsequent paired, fresh message in `(server_ts, msg_id)` order, with `server_ts` strictly greater than `at`, is applied:
  - `delta = wrap(raw − heading)` into `[−180, 180)`, computed as `math.Mod(raw-heading+540, 360) - 180`;
  - `heading = math.Mod(heading + 0.5·delta + 360, 360)`;
  - `at = server_ts`.

  Messages with `server_ts ≤ at` are skipped, so a replay does not move the state (the same guard as GF-76). `α = 0.5`: one sample at 1 Hz moves the estimate halfway, enough to damp jitter without a visible lag on the map. The stored `vehicles_latest.heading_deg` is the state after the winner. The EMA state map is updated only after `COMMIT` (GF-74). Restart re-seeds from raw (the DB row keeps the last value; P6 may persist state if needed). *Rationale: α = 0.5 is a fixed, testable choice; the wrap avoids a 359→1 jump; the `at` guard makes the smoother idempotent under replay.*
- **GF-79 — `Writer` API for P6 and P7.** `FlushResult{Stored []Message, Stale int, Unpaired int}`. `Stored` holds the paired, fresh messages in batch order, which is the seam P7 reads to build `position.batch` without touching the writer (P7 is not built in P5). On a nil error the caller acks every message of the batch, because each one is either stored, stale, or unpaired (`len(Stored) + Stale + Unpaired` equals the batch length; `Stored` counts messages, not new `positions` rows). `Flush` is serialised by a `sync.Mutex`; a single caller (P6) is the expected use. *Rationale: the caller needs one result type to ack; `Stored` is the minimal seam for P7 and costs nothing now.*
- **GF-80 — `FLEETOPS_*` parsing.** Unset or empty string → default. `FLEETOPS_FLUSH_INTERVAL`: `time.ParseDuration`, must be in `[100ms, 10s]`, default `1s`. `FLEETOPS_FLUSH_MAX`: base-10 integer via `strconv.Atoi`, must be in `[1, 5000]`, default `500`. `FLEETOPS_ACCURACY_MAX_M`: `strconv.ParseFloat(…, 64)`, must be finite and in `(0, 10000]` (the GF-55 accuracy ceiling), default `50`. Any other value → `Load` returns an error naming the variable and never the value (same rule as GF-63 and R-9). *Rationale: refusing a typo is safer than silently running at the default; empty = unset matches how compose passes unset variables (compose substitutes an empty string for unset/blank values); the ceilings keep a misconfiguration from breaking the 60 s and 500-message guarantees.* `NaN`/`Inf` are refused explicitly because `ParseFloat` accepts them. `MaxInFlight` (GF-60) stays a code constant; it is not a `FLEETOPS_*` name.
- **GF-81 — partition test helper.** `testinfra.EnsurePositionsPartitions(t, db, from, to)`: for each UTC calendar month `M` from `from` through `to` inclusive, `CREATE TABLE IF NOT EXISTS positions_YYYY_MM PARTITION OF positions FOR VALUES FROM ('YYYY-MM-01 00:00:00+00') TO ('<next month>-01 00:00:00+00')`. Test-only (P6 owns runtime ensure-ahead and the D-14 rule). *Rationale: tests need children; keeping them out of the migration keeps D-14's "children unchanged" and the no-DEFAULT rule.*
- **GF-82 — property-test budget.** `pgregory.net/rapid` v1.2.0 (already pinned). Pure buffer property: default checks. DB properties: one `NewMigratedDB` per test function, `TRUNCATE` per check, fixtures created once (GF-82 keeps `make test` fast). Each DB property runs rapid's default check count; if the measured `make test` time for the package exceeds 60 s, the check count for the DB properties is reduced in the same change and the measured time is recorded in the step report.
- **GF-83 — counters.** `Counters` with atomic `int64` fields `Stored`, `DroppedAccuracy`, `Stale`, `Unpaired`. Incremented only after a successful commit (`Writer`) or at the drop point (`Buffer`). Read by P6 for metrics (P22). *Rationale: atomics make the counters safe for the P6 consumer and any future reader without a lock; no metrics registration in P5.*
- **GF-84 — P5 boundaries.** P5 creates no goroutine, no timer, no NATS call, no Redis call, no HTTP route, no consumer, no ack, no NAK, no partition creation at runtime, no retention, no metric, no presence, no WS, no `position.batch` publish. `devices.last_seen_at` is not written (GF-67 stands; P5 decides only to not touch it). *Rationale: P5 is the write-side core; the consumer and every externally visible effect belong to P6 and P7.*

## Validation

Runnable on this host. Host tools: `/root/.local/go/bin/go`, `/root/.local/bin/golangci-lint`, `/root/go/bin/migrate`, wired through `deploy/.local/tools.mk`. Docker is available for testcontainers. **Do not run `docker compose up`.** Secrets are generated per shell and never committed.

1. **Gates.** `make fmt-check` → exit 0, no files listed. `make lint` → exit 0. `make test` (`go test ./... -race -count=1`) → exit 0 for all packages. **Pass:** all three exit 0.
2. **Migration 007 round trip.** `ls db/migrations | grep '^007_'` → exactly two files (`.up.sql`, `.down.sql`). `make test` includes `TestMigrationRoundTrip` at version 7 and `TestPositionsSchemaIsPartitionedParent`. **Pass:** both PASS in the `make test` output.
3. **Property tests, named.** `go test ./internal/ingest -run 'TestFlushSemanticsProperty|TestFlushSemanticsDBProperty|TestBatchUpsertPermutationProperty|TestBatchUpsertReplayProperty' -v -count=1`. **Pass:** four PASS lines, no rapid failure output.
4. **Buffer state machine (unit).** `go test ./internal/ingest -run 'TestBuffer|TestIsStale|TestHeadingEMA' -v -count=1`. **Pass:** every test in group 4 and group 5 PASS (each name listed above appears in the output).
5. **Writer integration.** `go test ./internal/ingest -run 'TestFlush|TestPositionsDuplicateIgnored|TestVehiclesLatestOlderNotApplied' -v -count=1`. **Pass:** every test in group 6 PASS.
6. **Throughput smoke (named).** `go test ./internal/ingest -run '^TestIngestThroughputSmoke100PerSecond$' -v -count=1`. **Pass:** PASS; the log line shows offers = stored, zero stale, zero unpaired, zero accuracy drops, max batch ≤ 500, and the measured msgs/s and p99 flush latency (reported, not gated).
7. **No row locks (named).** `go test ./internal/ingest -run '^TestFlushNoRowLocksHeldAcrossFlushes$' -v -count=1` → PASS.
8. **Config.** `go test ./internal/config -run 'TestLoadFlush|TestLoad' -v -count=1` → PASS, including `TestLoadFlushErrorNeverContainsValue`.
9. **Regression on P4.** `go test ./services/api -run 'TestIngestPosition|RoleMatrix' -v -count=1` → PASS (P4 untouched).
10. **Scope fence.** `git diff --name-only main...HEAD -- db/` → only `db/migrations/007_positions_vehicles_latest.up.sql`, `db/migrations/007_positions_vehicles_latest.down.sql`, and `db/*_test.go` files for group 1. `git diff --name-only main...HEAD -- README.md deploy/` → empty output. `git diff --name-only main...HEAD -- go.mod go.sum` → empty output, or only `go.mod` lines that promote an already-required module (no new `require` entries). **Pass:** the three outputs match.
11. **CLAUDE.md scope.** `git diff main...HEAD -- CLAUDE.md` adds only the optional-tunables table (three `FLEETOPS_*` names); `wc -l CLAUDE.md` < 200.
12. **No new env names.** `git diff main...HEAD -- internal/config/config.go | grep '^+' | grep -oE '"[A-Z][A-Z0-9_]{3,}"' | grep -v '"FLEETOPS_'` → empty output (the only new names are the three `FLEETOPS_*` names; §12 names are unchanged). **Pass:** empty.
13. **No P6/P7 scope.** `git diff --name-only main...HEAD` contains no path matching `consumer`, `redis`, `metrics`, `ws`, `presence`, `simulator`, or `services/`. **Pass:** no match.
14. **No secret leakage (R-9).** `grep -R "fo_dev_[A-Za-z0-9_-]\{43\}" --include=*.go --include=*.sql --include=*.md .` → no literal 43-character key in any file. `git diff --name-only main...HEAD | grep -i '\.env'` → empty. **Pass:** both empty.

## Testing depth

Roadmap P5 + tech-stack §9.

- **Unit (stdlib `testing`, no infra):** buffer state machine (group 4: every trigger, boundary, drop and drain path); pure helpers (`isStale`, heading EMA, group 5); config parsing (group 2).
- **Property (`pgregory.net/rapid`):**
  - Flush semantics, pure: `TestFlushSemanticsProperty`, virtual clock, no infra (the buffer is pure state).
  - Flush semantics, DB-backed: `TestFlushSemanticsDBProperty`, PostGIS testcontainer.
  - Batch upsert: `TestBatchUpsertPermutationProperty` and `TestBatchUpsertReplayProperty`, PostGIS testcontainer.
- **Integration (testcontainers PostGIS; real SQL; no mocks of infra):** group 1 schema test, group 3 helper, group 6 writer cases, group 8 smoke, group 7 lock check.
- **Race:** `go test -race` runs everywhere via `make test`. Concurrency tests: `TestBufferConcurrentOfferTickDrain`.
- **No mocks** of Postgres or time. Time is passed in as `now`; no fake clock library.
- **Migrations:** `TestMigrationRoundTrip` at version 7 covers up, down to zero, up, and idempotent up.

## SPEC-AMEND repeat (R-4)

- **D-14** — `positions` parent table partitioned by `server_ts` (mission §7: P5 row). Children are P6; the test helper is P5 (GF-81).
- **D-22** — the three `FLEETOPS_*` names read in P5, with defaults, and the parse rules in GF-80 (mission §7: P5 row). `WS_ROOM_CAP` and `PRESENCE_TTL` are not read in P5.
- **D-18 (interpretation)** — drop points and comparisons in GF-71, GF-72, GF-73 (mission §7: P5 row, "interpretation, stricter").
- The D-15 unique key is the `positions` primary key in GF-68; no new mission §7 row is needed because D-15 is already in tech-stack §11.
- `apierr`, `GPS_RAW` retention and `MaxBytes` are unchanged. The GF-59 value stays; tech-stack §8.2 "value set in P5" is answered by GF-59 (set in P4) and not changed in P5.

## Out of scope

- P6 behavior: the JetStream consumer loop; ack after commit; NAK with delay; `MaxDeliver` then terminate; partition ensure-ahead; daily retention; missing-partition metric; ingest metrics registration; durable retry of a failed flush.
- P7 behavior: Redis fanout and the `position.batch` publish (the P5 seam is `FlushResult.Stored`, GF-79); presence; WS hub.
- `devices.last_seen_at` writes (GF-84).
- Metrics, OTel, Prometheus (P22).
- k6 load tests and the 2,000 msg/s burst (P23); the smoke here is 100 msg/s only.
- Compose, `deploy/PORTS.md`, README edits (R-8, no port change).
- Any env name other than the three `FLEETOPS_*` names in GF-80.
- Any new Go module dependency.
- Index beyond the `positions` PK (read indexes are P8).
- Simulator, map, jobs, geofences, alerts, reports.
