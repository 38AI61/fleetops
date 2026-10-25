# Feature 06 — Ingest consumer & partitions (P6)

Status: approved plan, persisted 2026-10-25. Branch `phase-6-consumer-partitions`.
Source plan: `/root/.claude/plans/p6-plan-majestic-quill.md` (approved). Binding constitution: `specs/constitution/roadmap.md` §1 (R-1…R-9), §3 P6, §5, `specs/constitution/tech-stack.md` §3, §4, §6.3, §8, §9, §11 (D-14, D-16), `specs/constitution/mission.md` §7 (SPEC-AMEND rows), `AGENTS.md`, `CLAUDE.md`.

This spec is written before any P6 code (R-1, SDD). Implementation starts at Plan task group G0.

## Scope

P6 closes the ingest path that P4 (gateway) and P5 (batch writer) left unwired. It adds:

- a durable JetStream pull consumer on `GPS_RAW` that drives the existing `Buffer` and `Writer.Flush`, acks after commit, NAKs with delay on failure, and terminates after `MaxDeliver` (D-16);
- runtime partition lifecycle for `positions`: ensure-ahead (current + next 2 months at boot and daily), no DEFAULT partition, missing-partition → metric + NAK, daily retention by detach + drop (D-14);
- ingest Prometheus metrics, registered in-process and not exposed (exposure is P22);
- integration tests on the full path gateway → JetStream → consumer → PostgreSQL with injected DB faults and an injected clock.

Facts that shape the plan (verified in the repo):

- No consumer exists in production code; `Buffer` and `Writer` are called only from tests. `services/api/main.go` never reads `cfg.FlushInterval`, `FlushMax`, `AccuracyMaxM`.
- Partitions for `positions` are created only by `testinfra.EnsurePositionsPartitions` (GF-81). Production inserts fail today when no child exists for the timestamp.
- `GPS_RAW` has `MaxAge` 60 s (D-16), so a message is in the stream for at most 60 s. This bounds every retry window in this spec.
- The positions insert is already `ON CONFLICT DO NOTHING` on `(server_ts, msg_id)` (D-15, P5). Redelivery is therefore idempotent at the row level.
- `github.com/prometheus/client_golang` is absent from `go.mod`; it is the only new dependency (D-36).
- `github.com/nats-io/nats.go v1.55.0` is already a direct dependency; `jetstream` is used by `internal/ingest` and `internal/testinfra`.
- Known spec drift, not fixed here: spec 04 §7 gives `Gate.Publish(ctx, js, subject, payload)`; code is `Publish(js, m Message)`.

Out of scope: P7 (Redis fanout, `FlushResult.Stored` publish), P22 (`/metrics` exposure, dashboards, compose), README and `deploy/PORTS.md` edits, port changes, simulator, MinIO, k6.

## Plan

Build order. Each group lists its tests. Test files live in package `ingest` (internal tests) where a fault seam is needed; otherwise in `ingest_test`.

**G0 — Verify assumptions (read-only checks, first commit of the branch).**
- Grep `db/migrations/` for any `CHECK` on `positions`; assert none (GF-90 depends on it).
- Test `TestMissingPartitionSQLSTATE`: insert a position whose `server_ts` falls in a month with no child; assert `*pq.Error` with `Code == "23514"` and that the error came from the insert (GF-90).
- Test `TestNoDefaultPartition`: `pg_class`/`pg_inherits` has no child with `relispartition` and a DEFAULT bound.

**G1 — Pure primitives.**
- `ParseMessage(data []byte) (Message, error)` — inverse of `Message.Payload()` (GF-96). File `internal/ingest/consume.go`.
- NAK delay schedule `nakDelay(numDelivered int) time.Duration` and Term rule `shouldTerm(numDelivered, maxDeliver int) bool` (GF-88, GF-89).
- Metric set and `NewMetrics(reg prometheus.Registerer) *Metrics` in `internal/ingest/metrics.go` (GF-93).
- Tests: `TestParseMessageRoundTrip` (rapid property over generated `Message`), `TestParseMessageMalformed` (table: bad JSON, missing key, bad UUID, bad fleet id → error), `TestNakDelaySchedule` (1s,2s,4s,8s,8s… cap 8s), `TestShouldTermAtMaxDeliver`, `TestMetricsRegisteredNames` (fresh `prometheus.NewRegistry()`, `Gather` returns exactly the GF-93 names, types, label names).

**G2 — Partition lifecycle.** File `internal/ingest/partitions.go`.
- `EnsurePartitions(ctx, db *sql.DB, now time.Time) error` (GF-91).
- `RetainPartitions(ctx, db *sql.DB, now time.Time) (dropped int, err error)` (GF-92).
- `Maintenance{DB, Clock func() time.Time, Metrics}` with `Run(ctx)` (boot once, then `time.Ticker` 24 h) and `RunOnce(ctx, now)` used by tests (injected clock).
- Pure helpers: `ensureMonths(now) []time.Time`, `retainCutoff(now) time.Time`, `retainable(children []child, cutoff) []child`.
- Tests: `TestEnsureAheadCreatesCurrentPlusTwo` (real PG, clock = fixed UTC date), `TestEnsureIdempotent` (run twice), `TestEnsureConcurrentCallers` (two goroutines, advisory lock, both nil), `TestRetentionDropsOnlyExpired` (**roadmap Validation item**: partitions from now−14 months through now+2 created via ensure + explicit children; `RunOnce` at fixed clock; assert dropped set == children with upper bound ≤ cutoff, retained set == the rest, rows in a retained partition survive), `TestRetentionNeverDropsCurrentOrNext` (boundary: clock on the 1st of a month), `TestRetainableProperty` (rapid: random month sets and clocks; invariant: no child with month ≥ cutoff month is returned), `TestMaintenanceRunsOnBoot` (`Run` executes ensure before retain; checked by counters).

**G3 — Consumer loop.** File `internal/ingest/consume.go`.
- `Consumer{js jetstream.JetStream, db *sql.DB, buf *Buffer, w *Writer, m *Metrics, clock Clock, spec consumerSpec}`; `NewConsumer(...)`; `EnsureConsumer(ctx, js)` (CreateOrUpdate, GF-86); `Run(ctx) error` (loop, GF-86/87/98).
- Tests: `TestConsumerHappyPath` (gateway publishes N messages through `ingest.Handler`; after `Run` exits via drain, exactly N rows; consumer `NumAckPending == 0`), `TestConsumerAcksOnlyAfterCommit` (seam `beforeAck` observes DB row count == batch size at ack time), `TestConsumerAccuracyDropAckedImmediately` (accuracy > 50 m: acked at Offer, zero rows, counter `dropped_accuracy`), `TestConsumerStaleAndUnpairedAcked` (server_ts 61 s old → stale; unknown device → unpaired; both acked, `NumAckPending == 0`, zero rows), `TestConsumerResultMismatchNaks` (seam returns a short `FlushResult`; asserts NAK path, metric `result_mismatch`).

**G4 — Fault paths.**
- `TestKillPGRedelivery` (**roadmap Validation item**): `beforeFlush` seam on the first flush runs `SELECT pg_terminate_backend(pid)` for the consumer's `application_name` (set on the test DSN). Flush errors → NAK each → redelivery after delay → exactly one row per message; `flush_failures_total{reason="db"}` ≥ 1.
- `TestFlushTimeoutNaks`: test holds `LOCK TABLE positions IN ACCESS EXCLUSIVE MODE` in another transaction; `FlushTimeout` set to 300 ms in the test; flush fails by deadline → NAK → after lock release, redelivery → one row.
- `TestAckLostIsIdempotent`: `ackFault` seam fails the first ack after a successful commit → redelivery after AckWait (test sets AckWait 1 s) → second flush hits `ON CONFLICT DO NOTHING` → exactly one row.
- `TestMaxDeliverTerminates`: `MaxDeliver` 3 (test spec), DB lock held for the whole test → each message reaches `NumDelivered == 3` → Term; `terminated_total{reason="max_deliver"} == N`; zero rows; consumer keeps running; after lock release a fresh message is stored.
- `TestMalformedTerminates`: raw non-JSON payload published to `gps.raw.1` → Term immediately, `terminated_total{reason="malformed"}` == 1, consumer continues.
- `TestMissingPartitionNakThenEnsure`: gateway clock set to a month 3 ahead (outside ensure window); flush fails with 23514 → `missing_partition_total` ≥ 1 and NAK; then `EnsurePartitions` at that clock; redelivery succeeds → one row.

**G5 — Shutdown.** File `internal/ingest/consume.go`.
- Order (GF-95): `Run` observes ctx cancel → stop fetching → `Buffer.Drain` → `Writer.Flush` with a fresh `ShutdownTimeout` (10 s) context → ack on nil / NAK on error → fetched-but-not-offered messages `Nak()` (no delay).
- Tests: `TestShutdownDrainsAndAcks` (publish 3 within the flush interval, cancel ctx before the 1 s deadline → 3 rows, pending 0), `TestShutdownNaksUnprocessed` (fetch 5, cancel mid-batch via seam → unprocessed messages redelivered immediately; verified by `NumDelivered` and a second consumer run).

**G6 — Wiring.** File `services/ingest/main.go` (new binary, local `go run ./services/ingest`; GF-94). `services/api` is untouched (gateway-only, as built in P4).
- Boot order: `config.Load` → DB open+ping → NATS connect → `ingest.NewJetStream` → `ingest.EnsureStream` → `Maintenance.Run` (first pass: ensure, then retain) → `ingest.EnsureConsumer` → `Consumer.Run(ctx)`; `signal.NotifyContext` drives shutdown (GF-95).
- Shutdown order: consumer ctx cancel and wait for `Run` return (drain) → close NATS → close DB.
- Tests: `TestFullPathGatewayToPG` (named, roadmap Testing item): `httptest` echo with `ingest.Handler` + running `Consumer` + `Maintenance`; POST N positions; assert N rows and `NumAckPending == 0`. Covers GF-86…GF-95 end to end without a process boot.
- No `/metrics` route anywhere (P22).

**G7 — Validation run group.** See the Validation section.

## Requirements

### Binding, carried verbatim

From tech-stack §11:

- **D-14**: `positions` parent `PARTITION BY RANGE (server_ts)`; children `positions_YYYY_MM`; ensure current + next 2 months at ingest boot and daily; no DEFAULT partition; daily retention detaches and drops children whose upper bound ≤ start of (now − 12 months); insert into missing partition → metric + NAK.
- **D-16**: JetStream stream `GPS_RAW` (subjects `gps.raw.>`, file storage, `MaxAge` 60 s to match the stale rule, bounded `MaxBytes`); durable consumer with explicit ack after commit; NAK with delay on failure; `MaxDeliver` then terminate + metric. Gateway uses async publish and returns 503 when the pending buffer is full (backpressure). NATS container gets `-js`. **SPEC-AMEND.**

From tech-stack §8.2 (D-16 consumer semantics): durable consumer, explicit ack after DB commit; on DB failure NAK with delay; after `MaxDeliver` terminate and count.

From tech-stack §6.3: partition lifecycle as D-14.

From mission §7: D-14 (P5, P6); D-16 (P4).

### New decisions (GF-86 onward)

**GF-86 — Consumer shape.** Pull consumer on `GPS_RAW`, durable name `ingest-positions`, `FilterSubject` `gps.raw.>`, `DeliverPolicy` `DeliverAll`, `AckPolicy` `Explicit`, `AckWait` 30 s, `MaxDeliver` 8, `MaxAckPending` 10000, no `BackOff` (NAK delays are client-side, GF-88). Created with `CreateOrUpdateConsumer` at boot after `EnsureStream` (idempotent). One goroutine owns fetch, `Buffer`, `Writer.Flush` and acks; no `Consume` callback, so ack ordering is sequential by construction. Each cycle: `Fetch(FLEETOPS_FLUSH_MAX, FetchMaxWait(t))` where `t` is time until `Buffer.Deadline()` (floor 10 ms, or 1 s when unarmed). Each fetched message is parsed (GF-96) and offered; a returned batch flushes immediately; after the fetched slice `Tick(clock())`.
*Why:* `Buffer` is already single-goroutine by design (GF-70); one flusher keeps ordering and avoids a second flush in flight. `MaxAckPending` 10000 = 2 × the 5000 max: unacked messages are at most the buffer (< flush max) plus one fetched slice (≤ flush max). Pulls can never be blocked by the server cap while the flush deadline still fires, so there is no deadlock. `AckWait` 30 s exceeds the 10 s max buffer hold plus the 5 s flush timeout (GF-98), so normal operation never redelivers.

**GF-87 — Ack discipline.** A message is acked only when the `Writer.Flush` that contains it returns nil, and then the whole batch is acked (GF-79). `FlushResult` must account for every batch message in exactly one of `Stored`, `Stale`, `Unpaired`; if the counts differ, the batch is treated as a failure (NAK path, metric reason `result_mismatch`). Accuracy-dropped messages (`Offer` returns `buffered == false`) are acked at once: nothing is written, so there is nothing to commit. Stale and unpaired messages are acked after commit like stored ones. Ack errors are logged and counted (`ack_errors_total`); the message redelivers after `AckWait` and the `ON CONFLICT DO NOTHING` (D-15) absorbs the duplicate.
*Why:* D-16 "ack after commit"; GF-79 whole-batch ack; the accuracy drop never reaches the DB.

**GF-88 — NAK policy.** On any flush error (DB, missing partition, timeout, result mismatch), each message in the failed batch is NAKed with delay `nakDelay(NumDelivered) = min(1 s × 2^(NumDelivered−1), 8 s)`, unless GF-89 terminates it. The failed batch is dropped from memory; there is no in-process retry. Later messages form new batches and retry on their own schedule. AckWait does not apply after a NAK. The 8 s cap and `MaxDeliver` 8 make the retry window about 39 s total (1+2+4+8+8+8+8), which fits inside the 60 s `MaxAge` of `GPS_RAW` (D-16). A DB outage longer than about 40 s therefore loses messages to termination or `MaxAge`; that is recorded as residual risk, not hidden.
*Why:* D-16 "NAK with delay"; a fixed cap keeps redelivery inside `MaxAge`; retrying past 60 s would be stale anyway (D-18).

**GF-89 — Termination.** Two terminal paths, each `Term()` per message, each counted: (a) `NumDelivered ≥ MaxDeliver` at failure time → `terminated_total{reason="max_deliver"}`; (b) payload that fails `ParseMessage` → `terminated_total{reason="malformed"}`, terminated on first delivery. The consumer keeps running after a Term. P6 does not use the server's `MAX_DELIVERIES` advisory; messages the server exhausts without our NAK path ever running (e.g. AckWait redelivery loops) are not counted by P6 and are recorded as a residual observability gap, for P22 to close.
*Why:* Terminating on our final attempt makes the metric deterministic; a server-side exhaustion without a Term is silent. D-16 "terminate + metric".

**GF-90 — Missing-partition detection.** Detection = `errors.As(err, *pq.Error)` with `Code == "23514"` from the positions insert. Valid because `positions` has no CHECK constraint (G0 test), so 23514 on that statement only arises from partition routing. Chosen over message-text matching (locale-dependent) and over a pre-check (extra round trip, race with retention). Action: same as GF-88 (NAK, or Term per GF-89) plus `missing_partition_total` += batch length and `flush_failures_total{reason="missing_partition"}`. Not poison: the message retries until ensure-ahead creates the partition or `MaxDeliver` terminates it.
*Why:* D-14 "insert into missing partition → metric + NAK"; the SQLSTATE is stable across server locales.

**GF-91 — Partition ensure-ahead.** `EnsurePartitions(ctx, db, now)`: for each of the UTC months of `now`, `now+1 month`, `now+2 months`, in one transaction that first takes `pg_advisory_xact_lock(<fixed key>)`, runs `CREATE TABLE IF NOT EXISTS positions_YYYY_MM PARTITION OF positions FOR VALUES FROM ('YYYY-MM-01 00:00:00+00') TO ('YYYY-MM-01 00:00:00+00' of next month)`. Bounds are formatted from `time.Time`, never from input (same idiom as GF-81, gosec-clean). No DEFAULT partition is ever created. The advisory lock serialises several API instances. Runs at boot before the consumer fetches, and every 24 h thereafter (`Maintenance`).
*Why:* D-14 current + next 2 months at boot and daily; the advisory lock avoids the `CREATE TABLE IF NOT EXISTS` race on `pg_type`.

**GF-92 — Retention.** `RetainPartitions(ctx, db, now)`: `cutoff` = start of the UTC month containing `now − 12 months`. Children are listed from `pg_inherits` for `positions`, names matched by `^positions_(\d{4})_(\d{2})$`; upper bound = first instant of the following month. A child is dropped when upper bound ≤ cutoff. Each child is dropped in its own transaction: `ALTER TABLE positions DETACH PARTITION <child>` then `DROP TABLE <child>`; rollback on error. Safety guards: a child whose month is the current month or later is never dropped (checked in Go before SQL, plus the cutoff arithmetic). Example: `now` = 2026-10-25 → cutoff 2025-10-01 → drops `positions_2025_09` and older; retains `positions_2025_10` onward. Runs at boot after ensure, and daily. `now` is injected (`Maintenance.Clock`); tests never read wall-clock.
*Why:* D-14 retention definition ("upper bound ≤ start of (now − 12 months)"), with the month-start reading stated explicitly so it is testable; detach+drop in one transaction is rollback-safe; plain `DETACH` is used because `CONCURRENTLY` cannot run in a transaction (GF-5 forbids the concurrent index variants too).

**GF-93 — Metric set and registration.** Package `internal/ingest/metrics.go`. Registered into a private `prometheus.NewRegistry()` created in `services/ingest` (the consumer process) and passed to `NewMetrics`; no HTTP handler is attached (P22). Names, types, labels:

| Name | Type | Labels (fixed sets) | Meaning |
|---|---|---|---|
| `fleetops_ingest_messages_total` | counter | `outcome` ∈ {stored, stale, unpaired, dropped_accuracy} | message rate (`rate()` in P22) |
| `fleetops_ingest_flush_size` | histogram, buckets 1,5,10,25,50,100,250,500,1000,2500,5000 | — | batch size per successful flush |
| `fleetops_ingest_flush_duration_seconds` | histogram, default buckets | — | flush wall time, success and failure |
| `fleetops_ingest_flush_failures_total` | counter | `reason` ∈ {db, missing_partition, timeout, result_mismatch} | failed flushes |
| `fleetops_ingest_missing_partition_total` | counter | — | messages NAKed for a missing partition |
| `fleetops_ingest_terminated_total` | counter | `reason` ∈ {max_deliver, malformed} | D-16 terminate |
| `fleetops_ingest_ack_errors_total` | counter | — | ack publish failures |
| `fleetops_ingest_partitions_created_total` | counter | — | ensure-ahead creates (count of statements that created a table) |
| `fleetops_ingest_partitions_dropped_total` | counter | — | retention drops |
| `fleetops_ingest_partition_maintenance_errors_total` | counter | `op` ∈ {ensure, retain} | failed maintenance runs |

Counters are incremented from `Offer`/`FlushResult`, never from the existing `Counters` struct (GF-83 untouched).
*Why:* roadmap §5 "P6 (registered)" with "rate, flush size" minimum; the terminate and missing-partition metrics are required by D-14/D-16. Label sets are fixed so cardinality is bounded.

**GF-94 — Process wiring and config.** The consumer runs as its own binary `services/ingest` (tech-stack §3 pins `services/ingest/` as one of the three Go services; §4 gives the ingest process its own local `/metrics` on host 10008 in P22, so a separate process is the pinned target shape). It is run locally as `go run ./services/ingest` with the same env set as the API; compose gains no service in P6. The API process stays gateway-only (as built in P4). Multiple instances share the durable pull consumer (work distributes); cross-instance per-device ordering is not guaranteed by the consumer, and the `vehicles_latest` guard (D-19) plus `server_ts` ordering handle it. No new environment variable; `FLEETOPS_FLUSH_INTERVAL`, `FLEETOPS_FLUSH_MAX`, `FLEETOPS_ACCURACY_MAX_M` (GF-80) are passed into `NewBuffer` for the first time. `CLAUDE.md` gains only the local-run line for `services/ingest` (env names unchanged; D-40 allows a mechanical facts update).
*Why:* the pinned layout names three services; wiring the consumer into `services/api` would contradict tech-stack §3/§4 and P22's separate ingest metrics port.

**GF-95 — Shutdown.** Order: (1) the consumer process stops accepting work — there is no HTTP server in `services/ingest`; SIGTERM/SIGINT cancels the consumer context (signal.NotifyContext); (2) `Run` stops fetching, calls `Buffer.Drain`, flushes with a fresh `ShutdownTimeout` = 10 s context, acks on nil or NAKs on error; fetched-but-unoffered messages are `Nak()` with no delay; (3) `Run` returns; (4) close NATS, then DB. Acks come after commit at every step (GF-87). The API process's own shutdown is unchanged (P4 behavior).
*Why:* D-18 "flush on shutdown"; no message is acked before its commit, even on shutdown.

**GF-96 — Payload decode.** `ParseMessage(data []byte) (Message, error)` in `consume.go` is the exact inverse of `Message.Payload()` (GF-58): `msg_id`, `fleet_id`, `device_id`, position fields, `server_ts`. Unknown keys rejected (strict decode, as the gateway's GF-54). The subject is not parsed; fleet id comes from the payload. Malformed → GF-89(b).
*Why:* the consumer needs the exact `Message` back; strictness mirrors the producer.

**GF-97 — GF-61 residual risk (gateway publish failure after 204).** P6 does not change the gateway. A publish that fails after the 204 is still logged only (`ingest: publish failed msg_id=… fleet_id=…`), not retried, not counted, and never reaches the stream, so the consumer cannot redeliver it. This is an accepted residual risk of P6: a position acknowledged with 204 can be lost if NATS rejects the publish after the response. P6 does not close it; the consumer-side retry semantics (GF-88/89) cover only messages that reached `GPS_RAW`. The fix (publish-future counter, or a durable outbox) is out of scope and recorded for the backlog. The log line is the only record in P6.
*Why:* the roadmap risk must be explicit; closing it needs a gateway change owned by P4.

**GF-98 — Flush deadline and duplicate absorption.** Each `Writer.Flush` runs under a `FlushTimeout` = 5 s context (test override allowed). A stalled DB therefore becomes a NAK within 5 s, before `AckWait` (30 s). A flush that outlives `AckWait` or an ack lost after commit produces a redelivery; the insert is `ON CONFLICT DO NOTHING` (D-15), so the row count is unchanged. Exactly-one-row-per-message holds because of D-15, not because the consumer tracks delivered IDs.
*Why:* deterministic failure path; D-15 is the idempotency mechanism the spec already names.

**GF-99 — Boundaries.** `FlushResult.Stored` is consumed by nothing in P6 (the P7 seam is untouched; no Redis, no `position.batch`). No `/metrics` route, no dashboards, no compose, no port bound (ingest host 10008 in D-36 is P22). No DEFAULT partition. No migration (no `db/` change: partitions are runtime DDL, positions schema already exists in migration 007). No in-process retry of failed batches. No advisory subscription (GF-89).

## Validation

Each check runs on this host with `make` and `go test`; none requires `docker compose up`.

1. **Make gates** — `make fmt-check` exits 0; `make lint` exits 0; `make test` exits 0 (`-race -count=1`). Pass: exit 0 for all three.
2. **Roadmap Validation items as named tests**:
   - `go test ./internal/ingest -race -count=1 -run 'TestKillPGRedelivery'` passes and asserts exactly one row per message.
   - `go test ./internal/ingest -race -count=1 -run 'TestRetentionDropsOnlyExpired'` passes: dropped set == expired children, retained set == rest.
3. **Consumer unit behaviour** — `-run 'TestConsumer|TestNakDelaySchedule|TestShouldTermAtMaxDeliver|TestParseMessage'` passes.
4. **Metrics registration** — `-run TestMetricsRegisteredNames` passes; the test fails if any GF-93 name is missing or renamed.
5. **Full-path integration** — `-run 'TestFullPathGatewayToPG|TestMaxDeliverTerminates|TestMissingPartitionNakThenEnsure|TestAckLostIsIdempotent|TestFlushTimeoutNaks'` passes.
6. **Scope fences** (on the branch, against `main`):
   - `git diff --name-only main...HEAD` lists no path under `README.md`, `deploy/`, `specs/constitution/`, `AGENTS.md`. Only `specs/features/06-consumer-partitions.md`, `prompts/21-p6-plan.md` (provenance), `internal/ingest/`, `services/ingest/`, `go.mod`, `go.sum`, and tests appear (`services/api/` is untouched; `CLAUDE.md` only per item 8).
   - `git diff main...HEAD -- db/` is empty (no migration).
   - `git diff main...HEAD -- go.mod` adds only `github.com/prometheus/client_golang` (direct) and its indirect lines.
   - `grep -rn '"/metrics"' services internal` returns nothing.
   - `grep -rniE 'redis|position\.batch|websocket' internal/ingest` returns nothing.
   - `grep -rn 'DEFAULT' internal/ingest` returns nothing outside comments and tests asserting absence.
7. **No secrets** — `git diff main...HEAD | grep -iE 'password=|secret=|DATABASE_URL=|JWT_SECRET=[^ ]'` returns nothing; test DSNs come from testcontainers at runtime.
8. **CLAUDE.md scope** — `git diff main...HEAD -- CLAUDE.md` adds only the `services/ingest` local-run line (no new env names; GF-94); `wc -l CLAUDE.md` < 200.

## Testing depth

| Layer | Depth | Notes |
|---|---|---|
| Pure logic (`ParseMessage`, `nakDelay`, `shouldTerm`, `ensureMonths`, `retainCutoff`, `retainable`) | Unit + rapid property (`pgregory.net/rapid`, GF-82 budget) | No infra. |
| Metrics | Unit on a private registry | Name, type, label set. |
| Partition SQL (ensure, retain, 23514 detection) | Integration, real `postgis/postgis:16-3.4` via `testinfra.NewMigratedDB` | No mocks. Injected clock for every date. |
| Consumer loop, ack/NAK/Term | Integration, real NATS JetStream (`testinfra.NewJetStream`) + real PostGIS | Fault injection by real means: `pg_terminate_backend`, held `ACCESS EXCLUSIVE` lock, unexported seams for ack-fault and result-mismatch only. |
| Full path gateway → JetStream → consumer → PG | Integration, `httptest` + real infra | `TestFullPathGatewayToPG` is the roadmap P6 Testing item. |
| Shutdown order | Integration | Real timing bounded by `ShutdownTimeout`. |
| Race | `go test -race` on `./internal/ingest/...` | Via `make test` (D-37). |

Test helpers: reuse `testinfra.NewJetStream`, `FetchSubject`, `NewMigratedDB`; the existing `EnsurePositionsPartitions` (GF-81) stays for tests that need partitions without the production path.

## SPEC-AMEND repeat

No new SPEC-AMEND row. Repeats D-14 (P5, P6) and D-16 (P4) from mission §7 as binding. The P6 decisions are GF-86 to GF-99 and do not amend a constitution row.

## Out of scope

- P7: Redis publish of `FlushResult.Stored`, presence, WS, `position.batch`.
- P22: `/metrics` exposure, Prometheus or Grafana service, dashboards, compose, port 10008 binding.
- README and `deploy/PORTS.md` edits; any port change.
- Gateway publish-failure fix (GF-97) — recorded, not implemented.
- Advisory subscription for `MAX_DELIVERIES` (GF-89).
- Simulator, MinIO, k6, a DEFAULT partition, in-process batch retry.
- Any binary beyond `services/ingest` (GF-94); compose services; a worker entry inside `services/api`.
