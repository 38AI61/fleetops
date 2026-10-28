# Feature 22 — Observability (P22)

Status: planned on `phase-22-observability`, not implemented, 2026-10-28. Validation results and deviations are appended at the end when the phase runs.

Source plan: /root/.claude/plans/p22-plan-swirling-bunny.md (approved).

## Scope

Roadmap P22 (`specs/constitution/roadmap.md` §3, D-36) delivers:

- Prometheus `/metrics` on the three services: api (`127.0.0.1:10010`), ingest (`127.0.0.1:10008`, local `go run`, not compose), ws-hub (`127.0.0.1:10011`).
- Compose services `prometheus` (`127.0.0.1:10014`) and `grafana` (`127.0.0.1:10015`), and nothing else in compose.
- `deploy/prometheus/` scrape config for the three targets, and `deploy/grafana/` dashboard JSON plus provisioning, covering the README §9 set.
- OpenTelemetry spans on the assign flow, exported over OTLP HTTP when `OTEL_EXPORTER_OTLP_ENDPOINT` is set. Unset means a no-op.
- `deploy/PORTS.md` and `CLAUDE.md` mechanical rows, updated in this branch.

Out of scope: k6 and demo seed (P23), 10k WS race (P23), demo script (P23), README edits (R-8), any migration, any new metric beyond README §9, tracing beyond assign, collector or sampling infra, compose services beyond the two, merge to main, push.

### Baseline facts (verified read-only)

- Branch `phase-22-observability`, base `main`. Working tree clean. Recent: `da8d11c` (P22 prompt), `9e3e519` (P21 merged).
- Highest decision ID is GF-328 (per the task brief). Next free is GF-329. G0 re-checks this with grep.
- Highest migration is `012` (P21). P22 adds none.
- Existing private registries, all populated and never exposed:
  - `internal/ingest/metrics.go:49` `NewMetrics(reg)`. Series: `fleetops_ingest_messages_total{outcome}` (:52), `fleetops_ingest_flush_size` (:56), `fleetops_ingest_flush_duration_seconds` (:61), `fleetops_ingest_flush_failures_total{reason}` (:65), `fleetops_ingest_missing_partition_total` (:69), `fleetops_ingest_terminated_total{reason}` (:73), `fleetops_ingest_ack_errors_total` (:77), `fleetops_ingest_partitions_created_total` (:81), `fleetops_ingest_partitions_dropped_total` (:85), `fleetops_ingest_partition_maintenance_errors_total{op}` (:89), `fleetops_fanout_publish_failures_total{kind}` (:93), `fleetops_presence_transitions_total{state}` (:97), `fleetops_geofence_evaluation_duration_seconds` (:101), `fleetops_geofence_events_total{kind}` (:106), `fleetops_geofence_debounced_total` (:110), `fleetops_geofence_evaluation_failures_total{stage}` (:114). Pre-seeding at :118-141, `MustRegister` at :142-148.
  - `internal/eta/metrics.go:29` (`NewMetrics`). Series: `fleetops_eta_calls_total{outcome}`, `fleetops_eta_rule_skipped_total{reason}`, `fleetops_eta_pass_duration_seconds`, `fleetops_eta_passes_skipped_total`.
  - `internal/reports/metrics.go:20` (`NewMetrics`). Series: `fleetops_reports_rollup_pass_duration_seconds`, `fleetops_reports_rollup_passes_skipped_total{reason}`.
  - `internal/alerts/metrics.go:30-43` registers `fleetops_alerts_raised_total`, `fleetops_alert_evaluation_duration_seconds`, `fleetops_alert_evaluation_failures_total`, `fleetops_alert_passes_skipped_total` into the same ingest registry.
- All four sets are created in one registry at `services/ingest/main.go:83`, wired at :84, :107, :120, :133.
- Geofence evaluation latency is `fleetops_geofence_evaluation_duration_seconds` (ingest registry). It is observed in `internal/ingest/consume.go:383-397`. `internal/geofence` has no metrics.
- Hub WS connections: no Prometheus series exists. `internal/hub/hub.go:52-61` has atomic `Counters` with `ConnsOpen` (:54), incremented and decremented in `internal/hub/room.go:51, 62`. GF-121 says these are read only by tests.
- Mains today:
  - `services/api/main.go`: flag `-addr` default `127.0.0.1:10010` (:25). `NewServer(Deps)` at :70. `e.Start` at :94. `services/api/server.go:60`. Jobs handler wired at `server.go:87`.
  - `services/ingest/main.go`: no HTTP server. `config.Load` at :37, signal context at :75.
  - `services/ws-hub/main.go`: flag `-addr` default `127.0.0.1:10011` (:27). `net.Listen` at :91. `http.NewServeMux` at :95, `/stream` at :96.
  - No `promhttp` or `/metrics` anywhere in non-test code.
- Deps: `github.com/prometheus/client_golang v1.20.5` is direct (`go.mod:16`). OpenTelemetry `otel`, `otel/trace`, `otel/metric` v1.44.0 are indirect (`go.mod:90-94`). `go.opentelemetry.io/otel/sdk` and the OTLP HTTP exporter are not in `go.mod`. The module cache has `otel@v1.44.0` and `client_golang@v1.20.5`. No `vendor/`.
- No first-party OTel code exists.
- Assign flow: route `internal/jobs/http.go:57`, `POST /jobs/:id/assign`, behind `RequireRole(admin, dispatcher)`. Handler `(h *Handler) assign` at `internal/jobs/http.go:261`. Steps: principal, `bindStrict`, validate ids, parse `:id`, `h.Svc.Assign(c.Request().Context(), …)` at about :275, then `publishChange` if applied. Store `internal/jobs/store.go:383`. `begin` at :194-205 issues `set_config('app.actor_id', …, true)` first. Commit at `store.go:455`. Publish `internal/jobs/publish.go:35`, subject `jobs.events.<fleet_id>`, after commit. The `Publisher` interface is at `publish.go:10`.
- No ETA assign hook exists. The ETA loop is a 30 s ticker (`internal/eta/loop.go:17`) started from ingest (`services/ingest/main.go:120`).
- Assign tests use the `services/api` harness `startServer` (`harness_test.go:54`), and `startServerEvents` in `jobs_publish_test.go`.
- `deploy/docker-compose.yml`: postgres (`postgis/postgis:16-3.4`, `127.0.0.1:10002`), redis (`redis:7`, `10003`), nats (`nats:2`, `-js`, `10004`), osrm (`osrm/osrm-backend`, untagged, `10005`), minio (`bitnamilegacy/minio:2025.7.23-debian-12-r5`, `10006`/`10007`). Top-level named volumes: `postgres_data`, `redis_data`, `osrm_data`, `minio_data`. No `networks:`, no `extra_hosts`.
- `deploy/PORTS.md`: active 10002-10005, reserved 10006/10007, 10010-10013. Line 26: "Unassigned in range: 10000, 10001, 10008, 10009, 10014–10019."
- `deploy/prometheus/` and `deploy/grafana/` do not exist.
- `CLAUDE.md` is 193 lines.
- Docker is installed at `/usr/bin/docker`. `go` is not on PATH in this shell (G0 handles this).
- `tech-stack.md` §4 (line 82) names prometheus 9090→10014, and line 83 grafana 3000→10015. Line 81 names ingest metrics 10008 as local, not compose. D-36 is at `tech-stack.md:295`.

## Plan

Build order. Each group names its tests. Shared helper package: `internal/observability`.

**G0 — Preflight (no code).**
- Confirm Go is available. `go.mod` pins 1.27.3 (GF-16). If `go` is not on PATH, install that toolchain or set the host override in `deploy/.local/tools.mk`. If neither is possible, Go gates are recorded NOT RUN with that reason, and only config checks run.
- Run the baselines on the phase branch before any edit: `make fmt-check`, `make lint`, `make test`. If `internal/hub` fails, re-run that package in isolation before failing anything (flake policy).
- Web and driver-pwa baselines: `npm --prefix apps/web run typecheck` and `npm --prefix apps/web test`. The same two for `apps/driver-pwa`. Run only if `node_modules` exists. Otherwise record NOT RUN with the reason. Both apps stay untouched either way.
- Compose baseline: `docker compose -f deploy/docker-compose.yml config -q` exits 0.
- Confirm `grep -rn 'GF-32[0-9]' specs/` shows GF-328 as the highest ID.
- Confirm `gopkg.in/yaml.v3` is in `go.mod` (direct or indirect). If it is, G5 uses it. If not, G5 uses stdlib line-based asserts. No new dependency is added for YAML.
- Confirm the OTel SDK and exporter modules resolve at v1.44.0 (`go mod download` in the cache, or with network). The pin is recorded in GF-334.
- Output: baseline counts appended to Validation results. No test.

**G1 — Registry exposure helper and the `/metrics` handlers (GF-329, GF-330).**
- `internal/observability/metrics.go`:
  - `NewRegistry() *prometheus.Registry` creates a private registry and registers `collectors.NewGoCollector()` and `collectors.NewProcessCollector(...)`.
  - `Handler(reg *prometheus.Registry) http.Handler` returns `promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg})`.
- Registries are not consolidated. Each service exposes its own registry. Ingest's existing single registry (`services/ingest/main.go:83`) already carries ingest, eta, reports and alerts.
- `services/api`: in `server.go`, register `e.GET("/metrics", echo.WrapHandler(metricsHandler))` on the root Echo, outside the authenticated groups. The api registry holds only Go and process collectors (api owns no fleetops series).
- `services/ws-hub`: mount `mux.Handle("/metrics", observability.Handler(reg))` at `main.go:95`, beside `/stream`. Its registry holds the WS gauge from G2.
- `services/ingest`: add `-metrics-addr` (default `127.0.0.1:10008`). Start a minimal `http.Server` with only `/metrics`, `ReadHeaderTimeout` set, and shutdown joined LIFO like the other loops. This is the only new listener in P22.
- Tests:
  - `TestMetricsHandlerServesRegistry` (unit): `httptest` against `Handler(reg)` returns 200, and the body contains `# TYPE go_goroutines` and `process_cpu_seconds_total`.
  - `TestIngestMetricsMux` (unit): the ingest metrics mux returns 200 on `/metrics` and 404 on any other path.
  - `TestAPIMetricsOutsideAuth` (integration, `services/api`): `GET /metrics` with no JWT returns 200. `GET /metrics` with a driver JWT also returns 200, and the body is identical.

**G2 — Gap-fill: `fleetops_ws_connections` (GF-331).**
- `internal/hub`: no change to the hot path. In `services/ws-hub/main.go`, register `prometheus.NewGaugeFunc(Opts{Name: "fleetops_ws_connections", Help: ...}, func() float64 { return float64(counters.ConnsOpen.Load()) })` on the ws-hub registry. Confirm the field type (`atomic.Int64`) at G0.
- No labels. No pre-seed needed.
- Tests:
  - `TestWSConnectionsGauge` (unit, `internal/observability` or `services/ws-hub`): set `ConnsOpen` to 3 and `Gather` returns the gauge at 3. Set it to 0 and the gauge returns 0.
  - `TestWSHubMetricsServed` (integration): with the real ws-hub mux, open one `/stream` WS connection with an auth frame. `/metrics` reports `fleetops_ws_connections 1`, then 0 after close.

**G3 — Metric name and label registration (GF-329, GF-331).**
- `internal/observability/registered_test.go`:
  - Build the ingest registry as `services/ingest/main.go` does (`NewMetrics` for ingest, eta, reports, alerts, geofence). `Gather` and assert every README §9 name exists (`fleetops_ingest_messages_total`, `fleetops_ingest_flush_size`, `fleetops_geofence_evaluation_duration_seconds`).
  - Assert every seeded label set is present with value 0 (`outcome` for ingest messages, `reason` for flush failures and terminated, `op`, `kind`, `state`, `stage`, eta `outcome` and `reason`, reports `reason`).
  - Assert the label names match GF-93 and GF-301. Assert no series carries a `fleet_id` label.
  - Assert the ws-hub registry contains `fleetops_ws_connections` with no labels.
- This test replaces nothing. It is the binding "metric names and labels are registered" test named in roadmap P22.

**G4 — Compose additions, scrape config, ports (GF-332, GF-333).**
- `deploy/docker-compose.yml`: two services, and no other changes.
  - `prometheus`: image `prom/prometheus:v3.5.0`. `network_mode: host`. `command: ["--config.file=/etc/prometheus/prometheus.yml", "--web.listen-address=127.0.0.1:10014"]`. Volume `./prometheus:/etc/prometheus:ro`.
  - `grafana`: image `grafana/grafana:12.1.0`. `network_mode: host`. Environment `GF_SERVER_HTTP_ADDR=127.0.0.1`, `GF_SERVER_HTTP_PORT=10015`, `GF_AUTH_ANONYMOUS_ENABLED=true`, `GF_AUTH_ANONYMOUS_ORG_ROLE=Viewer`. Volumes `./grafana/provisioning:/etc/grafana/provisioning:ro` and `./grafana/dashboards:/var/lib/grafana/dashboards:ro`.
  - Neither service gets a named volume. Prometheus TSDB is ephemeral, as with the other local services. No secret is introduced.
- `deploy/prometheus/prometheus.yml`: `global.scrape_interval: 15s`. Jobs `fleetops-api` → `127.0.0.1:10010`, `fleetops-ingest` → `127.0.0.1:10008`, `fleetops-ws-hub` → `127.0.0.1:10011`, all with `metrics_path: /metrics`.
- `deploy/PORTS.md`:
  - Add 10008 as ingest metrics (local `go run`, `127.0.0.1`).
  - Add 10014 as prometheus and 10015 as grafana, both `127.0.0.1`, host network.
  - Replace the unassigned line with "Unassigned in range: 10000, 10001, 10009, 10016–10019."
- Tests:
  - `TestComposeObservabilityBlocks` (config, `deploy/` Go test or the G5 config test): the two service blocks exist; `network_mode` is `host`; the command and environment name 10014 and 10015; no other service changed (compared with the `main` version).
  - `TestPrometheusConfigTargets` (config): parse `deploy/prometheus/prometheus.yml` (yaml.v3 if present, else line asserts). The three targets and the `/metrics` path are exact.
  - `docker compose -f deploy/docker-compose.yml config -q` exits 0.

**G5 — Grafana dashboards as code (GF-333).**
- `deploy/grafana/provisioning/datasources/prometheus.yml`: one datasource, uid `prometheus`, type `prometheus`, url `http://127.0.0.1:10014`, default true.
- `deploy/grafana/provisioning/dashboards/fleetops.yml`: file provider on `/var/lib/grafana/dashboards`.
- `deploy/grafana/dashboards/fleetops-observability.json`: uid `fleetops-observability`, four panels only:
  1. Ingest rate: `sum by (outcome) (rate(fleetops_ingest_messages_total[1m]))`.
  2. Flush size: `histogram_quantile(0.5|0.95, sum by (le) (rate(fleetops_ingest_flush_size_bucket[5m])))`.
  3. WS connections: `fleetops_ws_connections`.
  4. Geofence eval latency: `histogram_quantile(0.95, sum by (le) (rate(fleetops_geofence_evaluation_duration_seconds_bucket[5m])))`.
- Tests:
  - `TestDashboardJSON` (config): the file parses with `encoding/json`, has the uid, has exactly four panels, and each panel `expr` names a metric from the G3 set.
  - Every datasource uid in the dashboard is `prometheus`.
  - Provisioning YAML parses (yaml.v3 or line asserts, as in G4).

**G6 — OTel assign-flow spans (GF-334, GF-335).**
- `internal/observability/tracing.go`: `InitTracing(ctx, lookup func(string) (string, bool), service string) (shutdown func(context.Context) error, err error)`.
  - If `OTEL_EXPORTER_OTLP_ENDPOINT` is unset or empty, return a no-op shutdown and set nothing. The global provider stays the default no-op.
  - Otherwise build `sdktrace.NewTracerProvider` with `otlptracehttp.New(ctx)` (the SDK reads the endpoint env), resource `service.name=service`, `otel.SetTracerProvider`, and return the provider's `Shutdown`.
- `services/api/main.go`: `InitTracing(ctx, os.LookupEnv, "fleetops-api")`. Shutdown is deferred with a 5 s timeout. Its tracer goes into `Deps.Tracer`.
- `internal/jobs/http.go`: add field `Tracer trace.Tracer` on `Handler`. Nil means `otel.Tracer("fleetops/jobs")`. In `assign`, after parsing `:id` and before `h.Svc.Assign`, start span `jobs.assign` with attribute `job.id` (int64). Record the error on the span if `Assign` returns one. `defer span.End()` covers `publishChange`. No span in `Store`, no SQL spans, no inbound propagation.
- `services/api/server.go`: wire `Tracer: d.Tracer` into the jobs handler at `:87`.
- `go.mod`: add `go.opentelemetry.io/otel/sdk`, `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp`, both at v1.44.0 to match the existing OTel line (GF-16).
- Tests:
  - `TestInitTracingUnsetIsNoop` (unit): lookup returns no endpoint. `InitTracing` returns no error. `otel.GetTracerProvider()` is not an SDK provider. Shutdown is nil-safe. No exporter is constructed and no error is logged.
  - `TestAssignEmitsSpan` (integration, `services/api`, real Postgres via testinfra): build the server with `Deps.Tracer` from an SDK provider using `tracetest.NewInMemoryExporter()` and `WithSyncer`. Assign a created job. The exporter holds one span named `jobs.assign` with `job.id` equal to the job id. The status is OK.
  - `TestAssignSpanOnError` (integration): assigning to a non-existent vehicle (409 or 422 path, per the existing handler) records an error status on the span, and the response is unchanged.
  - `TestAssignReplayStillSpans` (integration): a replayed assign still emits one span.
  - Existing `TestJobsAssignReplayAndReassign` and `TestJobsPublishOnePerTransition` stay green unchanged.

**G7 — CLAUDE.md rows (mechanical, GF-336).**
- Add three host-port rows to the existing table: ingest metrics 10008 (local `go run`), prometheus 10014, grafana 10015.
- Add an Observability block of at most 6 lines: `/metrics` on api, ingest (`-metrics-addr`), ws-hub; compose services prometheus and grafana; dashboard path; `OTEL_EXPORTER_OTLP_ENDPOINT` unset means no-op (the only env var, already pinned by D-36).
- If the file reaches 199 lines or more, compress existing Tooling prose without removing any rule. G7 measures with `wc -l`.
- Tests: `wc -l CLAUDE.md` < 200, and a line check that the three port rows exist.

**G8 — Full validation run.** See the Validation section. Record results in the feature spec's Validation results block.

## Requirements

### Binding decisions carried verbatim

- **D-36** (`specs/constitution/tech-stack.md:295`): Prometheus `/metrics` on api, ingest (host 10008, local) and hub. prometheus and grafana compose services. OTel via OTLP HTTP with standard `OTEL_EXPORTER_OTLP_ENDPOINT`; unset means no-op. `deploy/PORTS.md` is updated in the branch that binds each port.
- **D-41**: cloud profile deferred; the Fanout seam is untouched by P22.
- **roadmap §1**: R-1 and R-2 (smallest MVP). P22 adds no behavior beyond the named scope.
- **roadmap §5**: P22 gate rows (observability metrics; geofence latency dashboard).
- **README §9** (`README.md:191`): "Prometheus metrics (ingest rate, flush sizes, WS conns, geofence eval latency), Grafana dashboard, OpenTelemetry traces on the assign flow."
- **README §10** phase-5 row: the dashboard file lands in P22; the demo reading is P23.
- **README §11** (line 222): `deploy/grafana/` dashboards as code.
- **GF-93** and **GF-301**: metric naming and label pre-seeding. Private registries until P22. P22 lifts "not exposed".
- **GF-325**: the two reports series, pre-seeded in the private registry.
- **GF-318**: constants are not env vars.
- **GF-16**: tool pins (Go 1.27.3, otel v1.44.0 line, golangci-lint and migrate pins unchanged).
- **mission §7** SPEC-AMEND ledger. See SPEC-AMEND repeat.
- **GF-121** (`specs/features/07-fanout-ws.md:267`): hub `Counters` are read by tests. P22 adds a gauge that reads them.

### Reused GF decisions (cite, do not restate)

- GF-291 and GF-306: loop conventions, used only if a loop is added (none is).
- GF-298: API response shapes. Not used; `/metrics` is not a JSON route.

### New decisions

**GF-329 — Metrics exposure wiring and auth.** Expose the existing private registries through one helper, `internal/observability` (`NewRegistry`, `Handler`). Registries are not consolidated. Each service exposes its own registry. Path `/metrics`, unauthenticated, outside the JWT chain. Not added to the README §8 role matrix. Rationale: a Prometheus scrape carries no JWT, and no series carries a `fleet_id` label (GF-93, GF-301), so the endpoint exposes no tenant data. Local-only binds (GF-330) bound the exposure. Consequence: P22 adds no authenticated route, and the role matrix is unchanged.

**GF-330 — Listeners and binds.** api `/metrics` lives on the existing Echo listener at `127.0.0.1:10010`. ws-hub `/metrics` lives on the existing mux at `127.0.0.1:10011`. ingest gains one listener, `-metrics-addr` default `127.0.0.1:10008`, serving only `/metrics`. Rationale: no service binds beyond loopback, and the only new port is 10008, already reserved for this purpose in tech-stack §4.

**GF-331 — Gap-fill: one gauge.** README §9 "WS conns" has no series. Add `fleetops_ws_connections` (gauge, no labels), a `GaugeFunc` over `hub.Counters.ConnsOpen` in the ws-hub registry. It follows GF-93 naming. The other three README §9 items exist: ingest rate from `fleetops_ingest_messages_total`, flush sizes from `fleetops_ingest_flush_size`, geofence latency from `fleetops_geofence_evaluation_duration_seconds`. The api registry holds only Go and process collectors, which are standard and not fleetops series. No other fleetops series is added.

**GF-332 — Compose: host networking for prometheus and grafana.** Both services use `network_mode: host`, bound to `127.0.0.1` via their own flags and env (10014 and 10015). Rationale: every scrape target is a host loopback listener (GF-330). A bridge network would need the services to bind non-loopback addresses, which breaks the loopback-only rule. Deviation from tech-stack §4's "9090→10014" *mapping* wording only; the host port values 10014 and 10015 are unchanged. This deviation is recorded as a SPEC-AMEND row (see SPEC-AMEND repeat). Residual: on Docker Desktop for macOS, host networking does not reach the host's loopback, so the compose scrape requires a Linux Docker Engine. Local runs on macOS use a manual Prometheus against the same config. Not verified here (see Testing depth).

**GF-333 — Scrape config and dashboards.** `deploy/prometheus/prometheus.yml` has three jobs against `127.0.0.1:10010`, `127.0.0.1:10008` and `127.0.0.1:10011`, path `/metrics`, `scrape_interval: 15s`. Grafana provisioning and one dashboard, `fleetops-observability`, with four panels: ingest rate, flush sizes (p50 and p95), WS connections, geofence eval latency (p95). Rationale: exactly the README §9 set (GF-93 names). No more panels, since README §9 is the binding set. Provisioning is file-based, so there are no named volumes.

**GF-334 — OTel SDK wiring, api only.** `internal/observability.InitTracing` builds a tracer provider only when `OTEL_EXPORTER_OTLP_ENDPOINT` is non-empty, using OTLP HTTP (`otlptracehttp.New`, which reads the standard env). Unset means the global no-op provider. Called from `services/api/main.go` only. Resource `service.name=fleetops-api`. Module pins: `go.opentelemetry.io/otel/sdk` and `.../otlptrace/otlptracehttp` at v1.44.0 (GF-16). Rationale: traces only exist on the assign flow (README §9, roadmap P22), and api owns that flow. No sampler config, no collector. D-36 requires unset to be a no-op.

**GF-335 — Assign span placement and attributes.** One span, `jobs.assign`, started in `internal/jobs/http.go` `assign` after `:id` parsing, ended after `publishChange`. Attribute `job.id` (int64). The error status is set on a store error. No span in `Store`, no SQL spans, no inbound propagation. `Handler.Tracer` is injected; nil uses `otel.Tracer("fleetops/jobs")`. Rationale: the handler owns the request context and the job id, so no context plumbing through `Store` is needed (R-1 smallest). Fence exception for `internal/jobs/http.go` only (see Validation (b)).

**GF-336 — Testing and CLAUDE.md scope.** Compose verification is limited to `docker compose config -q` and the config tests. The compose stack is not started (see Testing depth). CLAUDE.md gets three port rows and a block of at most 6 lines. `OTEL_EXPORTER_OTLP_ENDPOINT` is the only new env var, already pinned by D-36. The file stays under 200 lines.

## Validation

Runnable on this host, with pass conditions.

- (a) `make fmt-check`, `make lint`, `make test` pass, including the new and changed packages (`internal/observability`, `internal/jobs`, `services/api`, `services/ws-hub`, `services/ingest`). Baselines recorded in G0. Web and driver-pwa are untouched; their baselines are recorded in G0.
- (b) Scope fence: `git diff --name-only main...HEAD` touches only:
  - `internal/observability/**` and `internal/**` metrics files (registries and helpers);
  - `services/{api,ingest,ws-hub}/**`;
  - `internal/jobs/http.go` and `internal/jobs/*_test.go` (the span exception, GF-335);
  - `go.mod` and `go.sum`;
  - `deploy/docker-compose.yml` (two service blocks), `deploy/prometheus/**`, `deploy/grafana/**`, `deploy/PORTS.md`;
  - `CLAUDE.md`;
  - `specs/features/22-observability.md`;
  - `specs/constitution/mission.md` (the §7 SPEC-AMEND ledger row only, GF-332 exception);
  - `prompts/**`.
  It must NOT touch `README.md`, `e2e/`, `apps/web/**`, `apps/driver-pwa/**`, `db/migrations/**`, the rest of `specs/constitution/`, or `.github/`.
- (c) No secrets in the diff: `git diff main...HEAD | grep -iE 'secret|password|token|key='` shows no values.
- (d) `make e2e`, `docker compose up`, and any live Prometheus or Grafana scrape are NOT RUN. Standing reason: the driver ban on compose up. Verification is limited to `docker compose config -q` and the unit and integration tests.
- (e) `wc -l CLAUDE.md` < 200.
- (f) Metrics endpoint shape: each of the three services serves a handler whose output contains the README §9 named series (unit-test asserted, not a live scrape).
- (g) Assign span test passes with the `job.id` attribute, via the in-memory exporter.
- (h) `docker compose -f deploy/docker-compose.yml config -q` exits 0. The dashboard JSON parses (`TestDashboardJSON`).
- (i) OTel no-op: with `OTEL_EXPORTER_OTLP_ENDPOINT` unset, `TestInitTracingUnsetIsNoop` passes, the api test suite passes, and the logs contain no exporter errors (test-proven).
- (j) Role matrix: P22 adds no authenticated route. `/metrics` is unauthenticated per GF-329. The consequence is stated: scrape traffic bypasses the JWT chain and is loopback-only (GF-330).
- (k) `deploy/PORTS.md` names 10008, 10014 and 10015 as bound (127.0.0.1) and removes them from the unassigned list. The new unassigned line reads "10000, 10001, 10009, 10016–10019".

## Testing depth

| Layer | What | Where | Depth |
|---|---|---|---|
| Unit | Metric names and label sets registered; `fleet_id` absent | `internal/observability` (G3) | `Gather` on the real registries |
| Unit | `/metrics` handler serves its registry | `internal/observability` (G1) | `httptest` |
| Unit | WS gauge tracks `ConnsOpen` | `services/ws-hub` (G2) | direct |
| Unit | No-op tracing when endpoint unset | `internal/observability` (G6) | direct |
| Integration (real Postgres via testinfra) | Assign emits `jobs.assign` with `job.id`; error status; replay still spans | `services/api` (G6) | in-memory exporter, no collector |
| Integration | api `/metrics` with and without JWT | `services/api` (G1) | real Echo route |
| Integration | ws-hub `/metrics` reports the open WS connection | `services/ws-hub` (G2) | real mux, real WS |
| Config | Compose blocks, ports, `network_mode`, nothing else changed | `deploy/` Go test (G4) | git diff vs `main` |
| Config | Prometheus config targets and paths | `deploy/` Go test (G4) | yaml.v3 if present, else line asserts |
| Config | Dashboard JSON parses, four panels, uid, datasource | `deploy/` Go test (G5) | `encoding/json` |
| Config | `docker compose config -q` exits 0 | CLI (G8) | parse only |
| Existing gates | Go suite green (hub flake policy); web and driver-pwa untouched | Makefile, G0 and G8 | recorded baselines |

## SPEC-AMEND repeat

P22 adds **one** SPEC-AMEND row. The reason is GF-332: host networking for prometheus and grafana replaces the tech-stack §4 "9090→10014" mapping wording. Port values 10014 and 10015 are unchanged. The row is appended to the mission §7 ledger in the same branch (fence exception for `specs/constitution/mission.md`, §7 only).

No other row. D-36 already names OTel via OTLP HTTP and `OTEL_EXPORTER_OTLP_ENDPOINT`, so the env var is not a new row. The WS gauge is a README §9 series, not a new metric. No schema change, so no migration row. The new CLI flag `-metrics-addr` is not an env var.

The GF-192 hub identity binding remains open and belongs to P23.

## Out of scope

- k6 load test and demo seed (P23).
- 10k WS race (P23).
- Demo script (P23).
- README edits (R-8).
- Any migration or schema change.
- Any new metric beyond the README §9 set (WS gauge is the only addition).
- Tracing beyond the assign flow; propagation; sampling; collector infra.
- Compose changes beyond prometheus and grafana.
- Merge to `main`; push.
- Any P23+ content.
