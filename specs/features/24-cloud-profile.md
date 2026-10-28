# Feature 24 — Cloud deployment profile (P24)

Status: planned on `phase-24-cloud-profile`, not implemented, 2026-10-28. Validation results and deviations are appended at the end when the phase runs.
Source plan: /root/.claude/plans/p24-plan-compiled-leaf.md (approved, self-gate passed — four brief deviations carried with rationale and ledger rows: GF-347 embedded-NATS transport replacing in-process channels, presence store swap in nats mode, GF-353 WS origin allowlist env, GF-355 lowercase GHCR image name; SPEC-AMEND rows exact and enumerated).

## Scope

P24 releases the roadmap backlog row for the cloud profile. It delivers, behind env gates and without changing the local compose stack: an in-container fanout transport (`FANOUT_MODE=nats`), a single-port front proxy and supervisor in one container image (`deploy/cloud/Dockerfile.render`), a non-interactive demo seed, Pages Functions proxies for the two React apps, a GHCR image and Render deploy job on `main`, `GET /healthz`, `docs/cloud.md`, and CLAUDE.md rows. A local cloud-shape smoke is defined here and run by the orchestrator after implementation.

Binding constitution: `specs/constitution/roadmap.md` §4 (cloud row), `specs/constitution/tech-stack.md` §11 (D-17, D-22, D-37, D-40, D-41), `specs/constitution/mission.md` §7 (SPEC-AMEND rule), README §6, §9, §10, §12, §15 (product spec for this phase), R-8 (README is never edited), GF-23, GF-201, GF-247, GF-329, GF-330, GF-340, `CLAUDE.md`, `specs/features/07-fanout-ws.md` (D-41 seam origin), `specs/features/22-observability.md`, `specs/features/23-load-tuning-demo.md` (format).

### Verbatim constitution text (carried, not paraphrased)

- roadmap §4 backlog row (line 167): `| Cloud deployment profile: Neon, Render, Cloudflare Pages, embedded NATS, in-process hub, public OSRM | README §15, D-41 | Single-instance note applies |`
- tech-stack §11 D-41 (line 300): `| D-41 | Cloud profile (§15) deferred to backlog. The \`Fanout\` interface (D-17) is kept as the seam; no in-process fanout or embedded NATS in MVP. | §15 is not §2.1; MVP is local compose; avoids a second code path now. |`
- tech-stack line 214: "Hub subscribes to NATS for jobs and alerts and to Redis for positions and presence. `Fanout` interface with a Redis implementation keeps the seam (D-41)."
- tech-stack line 114 (D-40): "Rule: any env name change updates CLAUDE.md in the same branch (D-40)."
- tech-stack line 106 (D-22): "Optional, additive (SPEC-AMEND, D-22; defaults in brackets)" — the D-22 ledger row (mission §7) reads "Optional `FLEETOPS_*` tunables; §12 names stay required".
- tech-stack line 30 (D-37): "testcontainers-go (PostGIS, Redis, NATS, MinIO modules) | Integration tests, no mocks of infra".
- mission §7 rule (line 97): "Rule: any new SPEC-AMEND gets a row here and a decision ID (D-nn from the plan, or GF-n for self-gate additions) in `tech-stack.md` §11 (D-40)."
- README §15 (carried as the product spec):

| Local (docker-compose) | Cloud (free tier) |
|---|---|
| PostgreSQL + PostGIS | Neon — `CREATE EXTENSION postgis` (supported) |
| NATS + separate ws-hub | embedded NATS in the Render container; in-process WS hub (single-instance note) |
| Redis pub/sub | in-process channels (single node) — Redis stays local for load tests |
| OSRM container | public demo server `router.project-osrm.org` (rate-limited, demo only) |
| React dispatcher UI | Cloudflare Pages (Vite static build, `/stream` proxied to Render) |

  Deploy: 1) push to GitHub, Actions CI builds the Go image → Render. 2) Neon: migrate + enable PostGIS; set `DATABASE_URL`, `JWT_SECRET` via env. 3) verify live URL: login, map loads, simulator (run locally) streams positions to the cloud ingest endpoint. 4) demo-on-demand — Render free spins down when idle (cold start ~1 min).
  Honest notes: hosted demo targets ~50 vehicles; the 2,000 msg/s burst profile stays a local k6 exercise.

### Baseline facts (verified read-only, 2026-10-28)

- Fanout seam: `internal/fanout/fanout.go:15` (`Fanout`: `PublishPositions(ctx, []Point) error`, `PublishPresence(ctx, PresenceEvent) error`); `Nop` at `:45`; `RedisFanout` `internal/fanout/redis.go:20`, constructor `NewRedisFanout(rdb)` `:25`, publishes `:47`, `:64`. No other constructor; no `FANOUT_MODE` or in-process bus anywhere in code (grep).
- Hub: `internal/hub/subscribe.go:53` `Subscribe(ctx, rdb, nc)`; Redis PSUBSCRIBE `positions.batch.*`, `presence.changed.*` at `:54`, relayed via `relayRedis` `:64-69`; NATS core `jobs.events.*` and `alerts.*` at `:72-81` via `relayNATS`. Started at `services/ws-hub/main.go:86`. Origin policy: `internal/hub/hub.go:35-42` (default vite dev origins), `:89-90`, `Accept` with `OriginPatterns` at `:115`.
- Ingest: `Publisher.Publish` `internal/ingest/publish.go:32` (calls `PublishPositions` `:46`, `Tracker.Observe` `:54`). `Consumer.flush` `internal/ingest/consume.go:342`: `c.w.Flush` `:344` (commit inside writer), `c.pub.Publish` `:368`, `evaluateGeofences` `:369` (detached 1 s context `:383`), acks `:374-376`. `services/ingest/main.go`: Redis connect `:70-81`, `fanout.NewRedisFanout(rdb)` `:117`, `presence.NewTracker(rdb, fan, ttl)` `:118`, `ingest.NewConsumer` `:190`, `-metrics-addr` default `127.0.0.1:10008` `:33`.
- Redis is used for more than pub/sub: presence device state with TTL via `SetArgs{TTL, Get:true}` in `internal/presence/tracker.go:95-111` (ingest only). ws-hub uses Redis only for ping (`services/ws-hub/main.go:63-70`) and PSUBSCRIBE. `REDIS_URL` is required by `internal/config/config.go:102-104` for every service.
- API: `services/api/main.go` NATS connect `:50` (fatal), JetStream ensure, `proofs.FromEnv` + `EnsureBucket` `:64-70` (GF-201). `internal/proofs/store.go:34-44` requires `MINIO_ENDPOINT`, `MINIO_KEY`, `MINIO_SECRET`; bucket `proofs` `:17`. `config.Load` (`internal/config/config.go`): `JWT_SECRET` `:78-83` (≥32 bytes), `DEVICE_KEY_SALT` `:87-92` (≥32), `NATS_URL` `:96-98`, `OSRM_URL` default `http://127.0.0.1:10005` `:43`, `:193-201`. `-addr` default `127.0.0.1:10010` `services/api/main.go:27`. Router `NewServer` `services/api/server.go:64`. No `/healthz` route exists anywhere; the only unauthenticated probe-like route is `GET /metrics` `server.go:72`.
- ws-hub: `-addr` default `127.0.0.1:10011` `services/ws-hub/main.go:28`; routes `/stream` `:100`, `/metrics` `:101`; first-frame auth `{"type":"auth","token":…}` via `hub.Verifier` (`internal/hub/conn.go:184`); no Postgres driver.
- Seed: `tools/seed/main.go` flags `--admin-email` (required), `--fleet-name`, `--timezone`, `--demo-devices`; `DATABASE_URL` from env; password via `passwordSource`: no-echo TTY prompt, else one stdin line; no flag or env path (GF-23). `internal/seed/seed.go` returns early when `fleets` is non-empty (`~:110`) before any prompt (GF-340), with a `LOCK TABLE` re-check (`~:130`). Exit codes 0 / 1 / 2. `--demo-devices` writes `deploy/.local/devices.json` (0600, atomic, `internal/devicesfile/devicesfile.go:99-115`) and `deploy/.local/demo-users.json` (0600, `internal/seed/demo_users.go:28,68-79`).
- Simulator: `tools/simulator/main.go:29-34` flags `-fleet`, `-rate` (500 ms–60 s), `-api`, `-devices` (default `deploy/.local/devices.json`); device key sent in `X-Device-Key` (`internal/simulator/client.go:71`). `POST /ingest/position` is served by the API (CLAUDE.md, P4).
- Frontend: all REST calls are relative (`apps/web/src/api/client.ts:20`, `apps/driver-pwa/src/api/client.ts:20`). No `VITE_*` usage and no `.env*` in either app. WebSocket: `apps/web/src/ws/client.ts:25-28` `wsUrl()` returns `${scheme}//${loc.host}/stream?fleet=<id>`. Driver PWA has no WebSocket client. `apps/web/vite.config.ts` proxies `/auth /map /jobs /vehicles /alerts /alert-rules /reports/daily /geofences` to `:10010` and `/stream` to `:10011` (ws). `apps/driver-pwa/vite.config.ts:8-11` proxies `/auth /driver /ingest`. No `functions/` and no `_redirects` in either app. Build scripts: web `tsc -b && vite build`; driver-pwa `tsc -b && vite build && node scripts/check-pwa-build.mjs`.
- CI: `.github/workflows/ci.yml` (118 lines): triggers `push` (all branches) and `pull_request` to `main` (lines 3-6); top-level `permissions: contents: read` (8-9); jobs `gates` (line 12) and `e2e` (line 69, `make e2e` at 108). No docker build, push, or GHCR step, no `packages:` permission.
- Makefile: no `docker` or cloud target; `seed` and `seed-demo` at `Makefile:36-40`; `e2e*`, `loadtest*`, `hub-scale`, `migrate`, `fmt-check`, `lint`, `test`, `tools`, `help` exist.
- `deploy/`: no `cloud/`. `deploy/PORTS.md:3` restricts host ports to 10000-10019; unassigned in range: 10000, 10001, 10009, 10016-10019 (line 34).
- `CLAUDE.md`: 199 lines (budget below 200).
- `go.mod`: `github.com/nats-io/nats.go v1.55.0` (direct); `github.com/nats-io/nats-server/v2` **absent** (new dependency); `go.yaml.in/yaml/v3 v3.0.5` present as indirect; `github.com/redis/go-redis/v9 v9.23.0`; `github.com/minio/minio-go/v7 v7.4.1`.
- testinfra: `internal/testinfra/{database,nats,postgis,redis,partitions}.go` exist.
- Host: Node v26.12.0; Docker 29.1.3 (server); `psql` present; k6 absent.
- Neon project `frosty-hall-37378198`: LIVE, migrated to version 12 (22 tables, PostGIS 3.3.10). Orchestrator-verified; not re-read in plan mode. **Migrations must not run against it again.** The connection string lives outside the repo at `/root/.local/tmp/deploy-probes/neon-uri.txt`; it is read only by the orchestrator for the smoke and never printed, committed, or written to a tracked file.

### Design findings that change the brief (decided here, GF-347…GF-355)

1. **Separate processes.** The supervisor runs api, ingest and ws-hub as separate OS processes (`services/*/main.go`). A Go-channel bus cannot cross process boundaries. The in-container transport is embedded NATS core (GF-347).
2. **Redis carries presence state.** Presence TTL state is kept in Redis (`internal/presence/tracker.go:95-111`), and `REDIS_URL` is required for every service. `FANOUT_MODE=nats` therefore also swaps the presence store to in-memory and makes `REDIS_URL` optional in that mode (GF-347).
3. **Hub origin check.** `coder/websocket` `OriginPatterns` default to the vite dev origins, so the Pages origin is rejected unless allowlisted. A `WS_ORIGIN_PATTERNS` env is added (GF-353).
4. **Proof photo URLs are not browser-reachable in cloud.** Presigned GET URLs are signed against MinIO's internal endpoint (`127.0.0.1:9000`). Exposing MinIO needs a second public port, which Render does not give us. This is a recorded known limitation (GF-350). Photo upload still works.
5. **No persistent disk on Render free.** `/data` (NATS JetStream store, MinIO data) is lost on redeploy and restart. Positions are durable in Neon; the JetStream buffer and proof photos are not (GF-349, docs).
6. **Route collisions on Pages.** UI routes `/jobs`, `/reports`, `/alerts`, `/zones` collide with API prefixes. Document navigations must be served `index.html`, not proxied (GF-352).
7. **GHCR requires lowercase.** The image is `ghcr.io/38ai61/fleetops`. The brief's `38AI61` casing is rejected by docker (GF-355).
8. **`SEED_PASSWORD`, not `FLEETOPS_SEED_PASSWORD`.** The `FLEETOPS_*` prefix is the D-22 tunable namespace with `config.Load` validation semantics (GF-351).
9. **Smoke host port 10019** is in range and unassigned. It is used only for the cloud smoke container and recorded in `deploy/PORTS.md` (scope fence).

## Plan

Build order. Each group names its tests. Commits happen only on the implementer's request; no push is part of this phase (the orchestrator merges and pushes).

**G0 — Preflight (no code change).**
- On the branch run `make fmt-check`, `make lint`, `make test`, `npm --prefix apps/web run build`, `npm --prefix apps/driver-pwa run build`. Record each exit code as the baseline. If `apps/web` e2e deps are absent, record `make e2e` as not run locally; CI `e2e` is then the gate.
- Pin the names the code depends on by grep, and record them here: `/auth/login` request body fields (`internal/auth` handler), the hub envelope type for positions (`position.batch`, per `internal/hub/scale_test.go`), `internal/testinfra/nats.go` helper signature, and the `Store`/`Tracker` constructor call sites.
- Pull images and record digests or tags: `golang:1.27.3-bookworm`, `debian:bookworm-slim`, and the latest `minio/minio:RELEASE.*` tag (written into the Dockerfile `ARG MINIO_IMAGE` in G4). Confirm `/usr/bin/minio` exists in that image (`docker run --rm --entrypoint sh <image> -c 'command -v minio'`).
- Confirm `node --test` on Node 26 is available (`node --version` ≥ 22.12 is required for G6).
- Tests: none (read-only checks, output recorded in the validation results).

**G1 — `FANOUT_MODE` and the in-process transport (GF-347).** Scope: `internal/config`, `internal/fanout`, `internal/presence`, `internal/hub`, `internal/ingest` (only if wiring changes), `services/ingest/main.go`, `services/ws-hub/main.go`, `services/api/main.go` (config path only), `go.mod`, `go.sum`.
- `config.Load`: `FANOUT_MODE` ∈ {`redis` (default when unset or empty), `nats`}; any other value → error that names the variable and never its value. `REDIS_URL` is required iff the mode is `redis`. If `REDIS_URL` is set in `nats` mode it is still format-checked (fail fast on typos). Add `Config.FanoutMode`.
- `fanout.New(mode, rdb, nc) (Fanout, error)`: `redis` → `NewRedisFanout(rdb)`; `nats` → `NewNATSFanout(nc)`. `NATSFanout` publishes core NATS (no JetStream, fire-and-forget like Redis pub/sub) to subjects `positions.batch.<fleet_id>` and `presence.changed.<fleet_id>` with the same payload bytes as the Redis implementation. The payload encoder is shared by both implementations.
- `presence.Store` interface: `SetGet(ctx, key, state string, ttl time.Duration) (prev string, found bool, err error)` with Redis `SetArgs{TTL, Get:true}` semantics. `RedisStore` is the existing behavior. `MemoryStore` is a mutex-guarded map with expiry checked on read and an injectable clock. `presence.NewTracker(store, fan, ttl)`. Redis-mode behavior is unchanged and the existing tests pass without edits.
- `hub.Subscribe(ctx, tr Transport, nc)`. `Transport` has two implementations: `redisTransport{rdb}` (existing PSUBSCRIBE) and `natsTransport{nc}` (core `positions.batch.*` and `presence.changed.*`, reusing `relayNATS` and the same fleet-parsing helper). In `nats` mode `services/ws-hub` and `services/ingest` skip the Redis connect and ping entirely.
- Tests:
  - Unit `TestLoadFanoutMode`: table covering unset → redis; `nats` ok without `REDIS_URL`; `redis` without `REDIS_URL` → error; `bogus` → error that names the variable and not the value.
  - Property (`pgregory.net/rapid`, D-37) `TestNATSAndRedisFanoutPayloadParity`: random point batches; subjects and payloads match between `RedisFanout` (testinfra Redis) and `NATSFanout` (embedded nats-server, a real in-process server).
  - Property `TestMemoryStoreMatchesRedisWithinTTL`: random key/state sequences with no expiry crossing; `(prev, found)` results match real Redis.
  - Unit `TestMemoryStoreExpiresAfterTTL` (injected clock).
  - Integration `TestIngestToHubInProcessNoRedis`: embedded NATS, `Publisher.Publish` with `NATSFanout`, hub with `natsTransport` and nil `rdb` (any Redis touch would panic). A WebSocket client on fleet A receives `position.batch`. A client on fleet B receives nothing.
  - Integration `TestPresenceChangedOverNATS`: `Tracker` + `MemoryStore` + `NATSFanout` → hub receives `presence.changed`.
  - Regression: existing `internal/hub`, `internal/presence`, `internal/fanout`, `internal/ingest` tests pass unchanged in `redis` mode.

**G2 — Single-port front proxy and supervisor (GF-348, GF-349, GF-350).** Scope: `internal/cloudproxy/**` (new), `services/cloud/**` (new, package main), `go.mod`, `go.sum`.
- `internal/cloudproxy.New(apiURL, hubURL *url.URL) http.Handler`: exact path `/stream` → `httputil.ReverseProxy` to ws-hub (native WebSocket upgrade); `/metrics` and `/metrics/*` → 404; everything else → api (including `/healthz`).
- `services/cloud` binary `fleetops-cloud` (supervisor):
  - Boot checks (names only in errors): `FANOUT_MODE` must equal `nats`; `MINIO_KEY` ≥3 bytes and `MINIO_SECRET` ≥8 bytes present; `DATABASE_URL`, `JWT_SECRET`, `DEVICE_KEY_SALT` present (length rules enforced by the children).
  - Start the embedded NATS server from `github.com/nats-io/nats-server/v2/server` in-process: host `127.0.0.1`, port 4222, JetStream on, store `/data/nats`; wait for `ReadyForConnections(10s)`.
  - Start MinIO as a child: `minio server /data/minio --address 127.0.0.1:9000 --console-address 127.0.0.1:9001` with env `MINIO_ROOT_USER=$MINIO_KEY`, `MINIO_ROOT_PASSWORD=$MINIO_SECRET`, `MINIO_BROWSER=off`. Wait for `GET http://127.0.0.1:9000/minio/health/ready` for up to 60 s, else exit 1.
  - Start children in order: `fleetops-api`, `fleetops-ingest`, `fleetops-ws-hub`. The supervisor overrides env: `NATS_URL=nats://127.0.0.1:4222`, `MINIO_ENDPOINT=127.0.0.1:9000`, `FANOUT_MODE=nats`, and removes `REDIS_URL`.
  - The front proxy listens on `0.0.0.0:$PORT` (default 8000) with `ReadHeaderTimeout` 10 s.
  - Child binaries are resolved in `filepath.Dir(os.Executable())`: `fleetops-api`, `fleetops-ingest`, `fleetops-ws-hub`, and `minio`.
  - Child stdout and stderr are relayed line by line to the supervisor's stdout and stderr with a `[api] `-style prefix. No log files.
  - Restart policy: a child exit (not during shutdown) restarts after a backoff of 1 s, doubling to 30 s. The backoff resets after 60 s of uptime. NATS and the proxy are not restarted; their failure exits 1.
  - Shutdown on SIGTERM or SIGINT: shut the proxy down (5 s), then SIGTERM the children in reverse order (ws-hub, ingest, api, minio), waiting up to 10 s per child before SIGKILL. Shut NATS down and exit 0.
  - `exec.Command` on fixed binary names: `//nolint:gosec // fixed child binary names resolved beside this executable` (repo `nolint` style, see `internal/apierr/apierr.go:12`).
- Tests:
  - Unit `TestProxyRoutesStreamToHubAndRestToAPI` (httptest upstreams). `TestProxyDeniesMetrics` (`/metrics`, `/metrics/x` → 404; `/stream/x` → api).
  - Integration `TestProxyForwardsWebSocketUpgrade`: a real ws-hub handler on embedded NATS behind the proxy; a `coder/websocket` client dials through the proxy and receives a `position.batch` frame.
  - Unit `TestSupervisorRefusesRedisMode` and `TestSupervisorRequiresMinIOCreds` (error text contains the variable name and not the value).
  - Unit `TestSupervisorRestartBackoff`: a re-exec helper child that exits 1; asserts the backoff sequence via an injected sleep.
  - Unit `TestSupervisorShutdownOrder`: helper children record exit order over a pipe; asserts ws-hub → ingest → api → minio.
  - Integration `TestEmbeddedNATSJetStreamReady`: embedded server starts, JetStream account info succeeds, a `nats.go` client connects.
  - Unit `TestWaitMinIOReady` against an httptest server (ready, then timeout).

**G3 — `/healthz`, `WS_ORIGIN_PATTERNS` (GF-353, GF-354).** Scope: `services/api/server.go`, `internal/config`, `services/ws-hub/main.go`, the role-matrix test file, `internal/hub` (test file only).
- `GET /healthz` on the api: 200 `{"status":"ok"}`, unauthenticated, liveness only, no database or NATS probe (a Neon cold start must not flap the instance).
- `WS_ORIGIN_PATTERNS` (optional, comma-separated `coder/websocket` origin patterns) parsed once in `config.Load` as `Config.WSOriginPatterns`. Unset or empty → the defaults in `internal/hub/hub.go:35-42`. Each entry is trimmed and must be non-empty. Errors name the variable and not the value.
- Tests:
  - Integration `TestHealthzUnauthenticated` (testinfra Postgres): 200 without a token, and the same request on `/vehicles` is 401.
  - Role-matrix row for `/healthz` marked exempt (GF-329 precedent).
  - Unit `TestWSOriginPatternsFromEnv` (unset, list, empty entry → error).
  - Integration `TestHubAcceptsConfiguredOrigin`: a dial with an `Origin` matching the env pattern is accepted; the default-origins case is rejected.

**G4 — Merged container image (GF-349, GF-350).** Scope: `deploy/cloud/Dockerfile.render` (new), `deploy/cloud/cloud_test.go` (new), `.dockerignore` (new, repo root), `deploy/PORTS.md` (one row for smoke port 10019).
- `Dockerfile.render`:
  - `ARG GO_IMAGE=golang:1.27.3-bookworm`, `ARG MINIO_IMAGE=minio/minio:<tag pinned in G0>`, `ARG RUNTIME_IMAGE=debian:bookworm-slim`.
  - Build stage: `CGO_ENABLED=0 go build -trimpath -o /out/fleetops-api ./services/api`, and the same for `fleetops-ingest` (`./services/ingest`), `fleetops-ws-hub` (`./services/ws-hub`), and `fleetops-cloud` (`./services/cloud`).
  - Runtime stage: `apt-get install --no-install-recommends ca-certificates` (cache cleaned); `COPY --from=${MINIO_IMAGE} /usr/bin/minio /usr/local/bin/minio`; the four Go binaries copied to `/usr/local/bin`; a `fleetops` system user with uid 10001; `/data` owned by it; `ENV FANOUT_MODE=nats PORT=8000 OSRM_URL=https://router.project-osrm.org`; `USER fleetops`; `WORKDIR /data`; exactly one `EXPOSE 8000`; `ENTRYPOINT ["/usr/local/bin/fleetops-cloud"]`.
  - No `ARG` or `ENV` holds a secret, token, salt, or database URL.
- `.dockerignore` excludes `deploy/.local/`, `deploy/.env`, `.git/`, `apps/*/node_modules`, `e2e/`. This keeps device keys and demo passwords out of the build context.
- Makefile target `cloud-image`: `docker build -f deploy/cloud/Dockerfile.render -t fleetops:cloud .`.
- Tests:
  - Static `TestDockerfileShape` (`deploy/cloud/cloud_test.go`): exactly one `EXPOSE 8000`; `ENTRYPOINT` is `fleetops-cloud`; the four binaries and the minio copy are present; `USER` is non-root; no `ARG`/`ENV` key matching `(?i)secret|password|token|salt|database|jwt|key`; `FANOUT_MODE=nats`; no `REDIS_URL`.
  - Static `TestDockerignoreExcludesLocalSecrets`: `.dockerignore` contains `deploy/.local` and `deploy/.env`.
  - The docker build itself runs in validation (e) and in the CI image job (G7). It is not run inside `go test` (no docker-in-docker in the gates).

**G5 — Cloud seeding, non-interactive path (GF-351).** Scope: `tools/seed/**`, `internal/seed/**` (only if the path needs it), `Makefile` (`cloud-seed` target).
- New boolean flag `--password-from-env`. When set, the password is read from env `SEED_PASSWORD`. Unset or empty → exit 2 (usage), with a message that names the variable only. The password policy is unchanged (weak → exit 2).
- Without the flag the existing TTY and stdin path is unchanged (GF-23), and `SEED_PASSWORD` is never read.
- GF-340 ordering: the already-seeded check runs before any password read, so a rerun with no `SEED_PASSWORD` exits 0 with "already seeded; no changes".
- The password is never printed, logged, or written to a file. `demo-users.json` keeps its existing content (the 50 demo driver passwords, 0600, under `deploy/.local`).
- Cloud mode writes the same local files under `deploy/.local` on the operator's workstation. Nothing is uploaded.
- Makefile `cloud-seed: ## Seed the cloud demo fleet non-interactively (SEED_PASSWORD env, first seed only)` → `$(GO) run ./tools/seed --demo-devices --password-from-env $(SEED_ARGS)`.
- Tests (testinfra Postgres, no mocks, D-37):
  - `TestPasswordFromEnvSeedsAndExitsZero`.
  - `TestPasswordFromEnvMissingExitsTwo` (no value in the message).
  - `TestPasswordFromEnvWeakExitsTwo`.
  - `TestPasswordFromEnvRerunExitsZeroWithoutEnv`.
  - `TestEnvIgnoredWithoutFlag`: `SEED_PASSWORD` set plus a stdin password; the stdin password is the one that logs in.
  - `TestSeedPasswordNeverLogged`: captured stdout and stderr contain no password string.
  - Existing seed tests still pass.

**G6 — Frontend base and Pages Functions (GF-352).** Scope: `apps/web/src/ws/client.ts`, `apps/web/src/vite-env.d.ts` (typing only, if the file exists or is needed), `apps/web/functions/**` (new), `apps/web/functions-test/**` (new), `apps/driver-pwa/functions/**` (new), `apps/driver-pwa/functions-test/**` (new), `Makefile` (`pages-test` target).
- No REST base variable. REST paths stay same-origin through the Pages Function, so there is no CORS and cookies stay on the Pages origin.
- `wsUrl(fleetId, loc)`: if `import.meta.env.VITE_WS_BASE` is set (for example `wss://<render-host>`), use `${base}/stream?fleet=<id>`. Otherwise the current behavior is unchanged. `VITE_WS_BASE` is the fallback if the Pages WebSocket upgrade check fails post-deploy.
- `apps/web/functions/[[path]].js` (`onRequest`):
  - Allowlist (exact path or path followed by `/`): `/auth`, `/map`, `/vehicles`, `/jobs`, `/geofences`, `/alerts`, `/alert-rules`, `/reports/daily`.
  - A document navigation (`Sec-Fetch-Mode: navigate`) → `env.ASSETS.fetch(new Request(new URL('/index.html', request.url)))`. This is the SPA fallback and resolves the UI/API prefix collision.
  - Allowlisted → `fetch(new Request(UPSTREAM + pathname + search, request))`, where `UPSTREAM = env.UPSTREAM_ORIGIN` (no trailing slash). If missing, return 500 with a message that names the variable only.
  - Anything else → `context.next()` (static assets).
- `apps/web/functions/stream.js`: a non-upgrade request → 426. An upgrade → `fetch(new Request(UPSTREAM + '/stream' + search, request))`. `Origin` passes through, so `WS_ORIGIN_PATTERNS` on Render must include the Pages host.
- `apps/driver-pwa/functions/[[path]].js`: allowlist `/auth`, `/driver`, `/ingest`; the same navigation fallback and `context.next()` behavior. **No `/stream`**: the driver PWA has no WebSocket client, so the orchestrator's list is narrowed here.
- Pages project settings (documented in `docs/cloud.md`, not in code): root directory `apps/web` (and `apps/driver-pwa`), build command `npm --prefix apps/<app> ci --include=dev && npm --prefix apps/<app> run build`, output `dist`, runtime env `UPSTREAM_ORIGIN`, optional build env `VITE_WS_BASE`.
- Tests:
  - Vitest (existing `apps/web` runner): `wsUrl` default unchanged; with `VITE_WS_BASE` set it returns the base plus `/stream?fleet=`.
  - `node --test` (`apps/web/functions-test/functions.node-test.mjs`, `apps/driver-pwa/functions-test/functions.node-test.mjs`) with a fake `env` (`ASSETS`, `UPSTREAM_ORIGIN`) and a stubbed `fetch`:
    - Allowlisted path proxied with method, headers, and body preserved.
    - Non-allowlisted path → `next()`.
    - `/jobs/new` with `Sec-Fetch-Mode: navigate` → `ASSETS` index, not proxied.
    - `/stream` non-upgrade → 426; upgrade → upstream `/stream?fleet=` preserved.
    - Missing `UPSTREAM_ORIGIN` → 500 without leaking any value.
  - Makefile `pages-test`: `node --test apps/web/functions-test apps/driver-pwa/functions-test`.
  - Builds: `npm --prefix apps/web run build` and `npm --prefix apps/driver-pwa run build` exit 0 (validation h).

**G7 — CI image and deploy on `main` (GF-355).** Scope: `.github/workflows/ci.yml`, `go.mod` (promote `go.yaml.in/yaml/v3` to direct), `deploy/cloud/cloud_test.go` (CI shape test).
- Keep `gates`, `e2e`, the triggers, and the top-level `permissions: contents: read` unchanged.
- New job `image`: `needs: [gates, e2e]`; `if: github.event_name == 'push' && github.ref == 'refs/heads/main'`; job `permissions: contents: read, packages: write`. Steps: checkout (same action version as the existing jobs); login with `echo "$GITHUB_TOKEN" | docker login ghcr.io -u "$GITHUB_ACTOR" --password-stdin` (token via step `env`, never echoed); `docker build -f deploy/cloud/Dockerfile.render -t ghcr.io/38ai61/fleetops:sha-${GITHUB_SHA} -t ghcr.io/38ai61/fleetops:latest .`; push both tags. No third-party docker actions (fewer pins).
- New job `deploy`: `needs: image`; the same `if`. One step with `env: RENDER_DEPLOY_HOOK_URL: ${{ secrets.RENDER_DEPLOY_HOOK_URL }}`. If the variable is empty → `::notice::RENDER_DEPLOY_HOOK_URL not set; deploy skipped` and exit 0. Otherwise `curl -fsS -X POST -o /dev/null "$RENDER_DEPLOY_HOOK_URL"`. No `set -x`, no echo of the URL.
- Missing secret → skip with a notice, so `main` stays green until the orchestrator sets the secret.
- PRs run `gates` and `e2e` only.
- GHCR package visibility is set to public by the orchestrator after the first push (manual).
- Tests:
  - Static `TestCIWorkflowShape` (`deploy/cloud/cloud_test.go`, yaml.v3): `gates` and `e2e` still present; `image` needs both and is guarded by `refs/heads/main`; `deploy` needs `image`; `packages: write` appears only in `image`; the only `secrets.` references are `GITHUB_TOKEN` and `RENDER_DEPLOY_HOOK_URL`; the image name is lowercase (no `38AI61`).

**G8 — Docs, CLAUDE.md, smoke harness (GF-353, GF-354 documented).** Scope: `docs/cloud.md` (new), `CLAUDE.md`, `deploy/cloud/smoke.mjs` (new), `Makefile` (`cloud-smoke` target).
- `docs/cloud.md` runbook sections: architecture (merged container with the port map and process tree); env table (names only, with the consumer for each: api, ingest, ws-hub, supervisor, seed, Pages Functions, web build); deploy steps (Neon: a fresh project runs `DATABASE_URL=… make migrate` then verifies PostGIS; the existing version-12 project runs no migrate; Render: image service `ghcr.io/38ai61/fleetops:latest`, instances = 1, health check `/healthz`, `PORT` provided by Render; Pages: root `apps/web` and `apps/driver-pwa`, env `UPSTREAM_ORIGIN`, optional `VITE_WS_BASE`); post-deploy checklist (login, map loads, refresh cookie survives on the Pages origin, WebSocket through Pages `/stream` connects, `Host` reaches Render correctly); cold start (~1 min spin-down, Neon autosuspend); limits (about 50 vehicles; single instance; no 2,000 msg/s burst, which stays local per README §14; `/data` ephemeral, so restarts drop the JetStream buffer, proof photos, and presence; proof photo URLs not browser-reachable, GF-350; public OSRM demo with D-10 `eta_slip` degradation); rollback (Render: redeploy the previous `sha-<sha>` tag; Pages: roll back the deployment; database: none, since P24 adds no migrations; never run `migrate down` against Neon).
- `CLAUDE.md`: add a "Cloud profile (P24)" section of about 10 lines (`make cloud-image`, `make cloud-seed`, `make cloud-smoke`, `make pages-test`; env names `FANOUT_MODE` (`redis` default | `nats`), `WS_ORIGIN_PATTERNS`, `SEED_PASSWORD` (cloud seed only, with `--password-from-env`), `PORT` (cloud container), `UPSTREAM_ORIGIN` (Pages Functions), `VITE_WS_BASE` (web build, optional), `RENDER_DEPLOY_HOOK_URL` (secret name only)). Update the `REDIS_URL` note (required unless `FANOUT_MODE=nats`). Add `SEED_PASSWORD` to the never-commit list. **Budget:** fold the `make help` target list and the loadtest bullets into fewer lines and condense the tooling bullets to pay for the additions. Final `wc -l CLAUDE.md` ≤ 199.
- `deploy/cloud/smoke.mjs` (Node ≥22, global `WebSocket`), run by `make cloud-smoke` with env `SMOKE_BASE` (default `http://127.0.0.1:10019`), `SMOKE_ADMIN_EMAIL`, `SMOKE_PASSWORD`. Steps:
  1. Poll `GET /healthz` for up to 120 s until 200.
  2. `POST /auth/login` → access token (body fields as pinned in G0).
  3. `GET /map/vehicles?bbox=-180,-90,180,90` → 200, JSON array.
  4. Read `deploy/.local/devices.json` for `fleet_id`. Spawn `go run ./tools/simulator --fleet=50 --rate=5s --api <base>` for 90 s, then kill it.
  5. While the simulator runs, open `ws://…/stream?fleet=<fleet_id>` (ws scheme from base), send `{"type":"auth","token":…}`, and count `position.batch` frames for up to 60 s.
  6. After the simulator stops, `GET /map/vehicles` again → at least 1 vehicle.
  7. Print `healthz=… login=… map=… ws_position_frames=N vehicles=M result=PASS|FAIL`. Exit 0 only on PASS. The token and password are never printed.

## Requirements

Binding decisions (carried verbatim above in Scope) plus the decisions below. Each has rationale and a ledger row in SPEC-AMEND repeat.

**GF-347 — In-process fanout shape (`FANOUT_MODE`).**
- Values: `redis` (default; the existing behavior, compose untouched) and `nats` (container profile).
- `nats`: `NATSFanout` on core NATS subjects `positions.batch.<fleet_id>` and `presence.changed.<fleet_id>`. The hub's `natsTransport` subscribes to them. Ingest uses `presence.MemoryStore`. `REDIS_URL` is not required.
- Rationale: the services are separate processes, so a shared in-memory broker cannot work. Embedded NATS (D-16, JetStream) already runs in the container, so the transport adds no component. Core NATS gives the same at-most-once semantics as Redis pub/sub; the durable record is `positions` and `job_events`.
- Error semantics: publish errors use the existing ingest budgets (1 s, `context.WithoutCancel`), are logged, and never change the ack (the same as Redis). The memory store has no error path.
- Validation: `FANOUT_MODE` is parsed once in `config.Load`, as a non-`FLEETOPS_*` name, so it is validated there and not in the D-22 tunables table.

**GF-348 — Single-port front proxy.**
- `internal/cloudproxy` (stdlib `httputil.ReverseProxy`) inside `fleetops-cloud`. `/stream` → `127.0.0.1:10011`; `/metrics*` → 404; everything else → `127.0.0.1:10010`. The public port is `PORT` (default 8000).
- Rationale: Render exposes one port. The stdlib proxy handles WebSocket upgrades natively. A third binary adds a process; Echo middleware would couple the WebSocket path to the api's router.
- `/metrics` is denied on the public port because P22 keeps metrics loopback-only.

**GF-349 — Supervisor and container layout.**
- One Go binary `fleetops-cloud` (`services/cloud`) hosts embedded NATS and the front proxy, and supervises the children `fleetops-api`, `fleetops-ingest`, `fleetops-ws-hub`, and `minio`. The policy is as stated in G2. Logs go to stdout only.
- Rejected: a shell script with `wait` (no restart, backoff, or ordering without far more code, and not testable); s6 (a non-Go runtime, not needed).
- Storage: `/data` (ephemeral on Render free; see Scope finding 5).
- Single-instance note: the in-process transport requires one Render instance. Two instances would split WebSocket clients from the ingest that feeds them.

**GF-350 — Embedded NATS and MinIO.**
- NATS: the server is embedded as a library (`nats-server/v2/server`) in the supervisor process. JetStream is on, the store is `/data/nats`, listening on `127.0.0.1:4222`.
- MinIO: the binary is copied from the pinned `minio/minio` image, because MinIO has no Go embedding API. State is `/data/minio`. `MINIO_KEY`/`MINIO_SECRET` map to `MINIO_ROOT_USER`/`MINIO_ROOT_PASSWORD`. The supervisor sets `MINIO_ENDPOINT=127.0.0.1:9000` for the api.
- Deviation from README §15 table: MinIO is added because the api refuses to boot without it (GF-201). The table omits it; the boot requirement wins.
- Known limitation (recorded): presigned `proof_photo_url` is signed against `127.0.0.1:9000`, so a browser cannot fetch it in cloud. Exposing MinIO needs a second public port, which Render does not give us. Photo upload still works.

**GF-351 — Cloud seeding.**
- Non-interactive path: `--password-from-env` (boolean flag) reads env `SEED_PASSWORD`. The interactive path is unchanged.
- Rationale: the flag carries no secret, which is the intent of GF-23. The env name is outside the `FLEETOPS_*` tunable namespace (D-22). GF-340 ordering keeps reruns exit 0 without the variable.

**GF-352 — Frontend base and Pages Functions.**
- REST is same-origin through `functions/[[path]].js`. No `VITE_API_BASE` and no CORS. Cookies stay on the Pages origin.
- WebSocket: `functions/stream.js` passes the upgrade through. `VITE_WS_BASE` is the documented fallback (direct to Render), selected by the orchestrator only if the post-deploy WebSocket check through Pages fails.
- Navigation requests are served `index.html`, because UI routes collide with API prefixes.
- The driver PWA does not proxy `/stream` (no WebSocket client).
- Validation: the Pages behavior (`ASSETS`, upgrade pass-through, `Host` handling) is verified by the `node --test` fakes here and by the post-deploy checklist. The live Pages behavior is not verified in this phase.

**GF-353 — Hub origin allowlist.** `WS_ORIGIN_PATTERNS` (optional, comma-separated). Rationale: the Pages host is deploy-specific, so it is an env value and not code. Defaults are unchanged.

**GF-354 — `/healthz`.** Unauthenticated, liveness only, no database probe. Rationale: a Neon cold start or autosuspend must not mark the instance unhealthy and cause a restart loop. Exempt from the role matrix under the GF-329 precedent.

**GF-355 — CI image and deploy.**
- Shape: `image` and `deploy` jobs after `gates` and `e2e`, on `main` only.
- Image: `ghcr.io/38ai61/fleetops` (lowercase; GHCR requires it), tags `sha-<GITHUB_SHA>` and `latest`.
- Deploy: `curl` to the Render hook from secret `RENDER_DEPLOY_HOOK_URL`; a missing secret is a skip with a notice.
- No third-party docker actions. The login uses stdin.
- The secret value is set by the orchestrator. The workflow reads only the name.

**Standing constraints carried into this phase:**
- D-22 (ledger row): "Optional `FLEETOPS_*` tunables; §12 names stay required." The new non-`FLEETOPS_*` env names (`FANOUT_MODE`, `WS_ORIGIN_PATTERNS`, `SEED_PASSWORD`, `PORT`, `UPSTREAM_ORIGIN`, `VITE_WS_BASE`, `RENDER_DEPLOY_HOOK_URL`) are not tunables. Server-side names are validated in `config.Load` (the single env reader), with error text naming the variable and not the value.
- D-40: env names are documented in CLAUDE.md and `docs/cloud.md`. Values are never committed or printed.
- D-37: infrastructure tests use testcontainers or real embedded servers; no mocks of infra.
- GF-201: MinIO is a boot requirement; the container runs it.
- GF-23 and GF-340: the seed password is never a flag on the interactive path; reruns on a seeded DB are exit 0 without a prompt.
- GF-329 and GF-330: `/metrics` is loopback-only and carries no `fleet_id`. The public front denies `/metrics`.
- R-8: README is never edited. `roadmap.md` is not edited (the orchestrator updates STATE.md).
- D-10: the public OSRM demo is accepted; `eta_slip` degrades (skipped, metric).
- D-16: NATS runs with `-js`; the embedded server enables JetStream.
- D-17: Redis carries positions and presence in the default mode. The NATS override applies only in `nats` mode (GF-347).
- D-41 (released by P24): "Cloud profile deferred to backlog" no longer holds for cloud mode. Local compose is unchanged; `redis` remains the default.
- CLAUDE.md is 199 lines and must stay under 200 (D-40).

## Validation

Runnable checks on this host, in order. The implementer runs (a)–(n). The orchestrator runs the smoke (f).

(a) Gates green on the branch: `make fmt-check lint test` exit 0 (`make test` is `go test ./... -race -count=1`). Recorded with output tails.

(b) Scope fence: `git diff --name-only main...HEAD | grep -Ev '^(internal/(config|fanout|presence|hub|cloudproxy|seed|testinfra|ingest)/|services/|tools/seed/|internal/seed/|deploy/cloud/|deploy/PORTS\.md$|apps/web/src/ws/|apps/web/src/vite-env\.d\.ts$|apps/web/functions(-test)?/|apps/driver-pwa/functions(-test)?/|\.github/workflows/ci\.yml$|\.dockerignore$|docs/cloud\.md$|CLAUDE\.md$|Makefile$|go\.mod$|go\.sum$|specs/features/24-cloud-profile\.md$|prompts/|specs/constitution/(mission|tech-stack)\.md$)'` → empty output. Constitution files may change only in the §7 and §11 ledger rows listed in SPEC-AMEND repeat.

(c) Forbidden paths untouched: `git diff --quiet main...HEAD -- README.md deploy/docker-compose.yml db/migrations e2e specs/constitution/roadmap.md` → exit 0.

(d) No secrets in the diff: `git diff main...HEAD | grep -E 'postgres(ql)?://|neon\.tech|BEGIN [A-Z ]*PRIVATE KEY'` → empty; `git diff main...HEAD | grep -E '(JWT_SECRET|DEVICE_KEY_SALT|MINIO_SECRET|MINIO_KEY|SEED_PASSWORD)=[^$<[:space:]]'` → empty; `git grep -q -F -f /root/.local/tmp/deploy-probes/neon-uri.txt -- .` → exit 1 (no match in the tree). The URI is never printed.

(e) `wc -l CLAUDE.md` → ≤ 199.

(f) Container build and boot shape: `docker build -f deploy/cloud/Dockerfile.render -t fleetops:cloud-p24 .` exit 0. Then `docker run --rm --entrypoint sh fleetops:cloud-p24 -c 'command -v fleetops-api fleetops-ingest fleetops-ws-hub fleetops-cloud minio'` prints five paths.

(g) Pages Functions: `node --test apps/web/functions-test apps/driver-pwa/functions-test` exit 0 (node ≥22.12; host is Node 26).

(h) Frontend builds: `npm --prefix apps/web run build` and `npm --prefix apps/driver-pwa run build` exit 0. Vitest `npm --prefix apps/web test` exit 0.

(i) Route inventory: `git diff main...HEAD -U0 -- services/api | grep -E '^\+.*(GET|POST|PATCH|DELETE|PUT)\('` → only the `/healthz` route is new.

(j) Compose file unchanged byte-for-byte: `git diff --quiet main...HEAD -- deploy/docker-compose.yml` → exit 0.

(k) Makefile: `make help` lists `cloud-image`, `cloud-seed`, `cloud-smoke`, `pages-test`.

(l) `.dockerignore` excludes `deploy/.local/` and `deploy/.env`: confirmed by the static test (G4).

(m) CI workflow shape test passes under `make test` (G7).

(n) `make lint` clean on the new packages (gosec exceptions only with justified `nolint`, the repo style).

### Orchestrator cloud-shape smoke (not a Claude step)

Run by the orchestrator after the implementation merges to the phase branch. Record each command, exit code, and output tail in the spec's validation results. Never print the Neon URI, JWT, salt, MinIO secret, or seed password.

- S0 — env file: write `/root/.local/tmp/deploy-probes/cloud-smoke.env` (mode 0600, outside the repo) with `DATABASE_URL` (from `neon-uri.txt`), `JWT_SECRET` and `DEVICE_KEY_SALT` (`openssl rand -hex 32`), `MINIO_KEY` and `MINIO_SECRET` (random), `SEED_PASSWORD` (random), `FANOUT_MODE=nats`. Load with `set -a; . <file>; set +a`. Pass: the file exists with mode 0600 and lies outside the repo.
- S1 — schema before: `psql "$DATABASE_URL" -tAc "select version||'|'||dirty from schema_migrations"` → `12|f`. Also list `positions_*` partitions. Read-only.
- S2 — seed: `make cloud-seed SEED_ARGS="--admin-email <admin-email>"`. Pass: exit 0 with "seeded" or "already seeded; no changes". If already seeded, pass only if `deploy/.local/devices.json` exists and its `fleet_id` equals the database's `select id from fleets`. The simulator run in S6 is the arbiter for key validity (salt match).
- S3 — build: `docker build -f deploy/cloud/Dockerfile.render -t fleetops:cloud-smoke .` exit 0.
- S4 — run: `docker run -d --name fleetops-cloud-smoke --env-file <env> -e PORT=8000 -p 127.0.0.1:10019:8000 fleetops:cloud-smoke`. Pass: container id printed, exit 0.
- S5 — smoke: `set -a; . <env>; set +a; SMOKE_BASE=http://127.0.0.1:10019 SMOKE_ADMIN_EMAIL=<admin-email> SMOKE_PASSWORD="$SEED_PASSWORD" make cloud-smoke`. Pass: exit 0, `result=PASS`, `ws_position_frames ≥ 1`, `vehicles ≥ 1`. (This covers `/healthz`, login, `/map/vehicles`, the simulator streaming through the container's ingest endpoint, and positions visible on WS `/stream`.)
- S6 — logs: `docker logs fleetops-cloud-smoke 2>&1 | grep -c -F -e "$JWT_SECRET" -e "$SEED_PASSWORD" -e "$MINIO_SECRET" -e "$DEVICE_KEY_SALT" -e "$MINIO_KEY" -e "$DATABASE_URL"` → 0.
- S7 — restart: `docker restart fleetops-cloud-smoke`, then `/healthz` 200 within 120 s. Pass: 200.
- S8 — schema after: same query as S1 → `12|f`, identical. The partition list may gain the current and next month only (ingest partition maintenance); no other DDL. `make migrate` is never run against Neon.
- S9 — teardown: `docker rm -f fleetops-cloud-smoke`. Record the image tag kept.

## Testing depth

| Layer | Scope | Tooling | Where |
|---|---|---|---|
| Unit (Go) | config parse (`FANOUT_MODE`, `WS_ORIGIN_PATTERNS`, `REDIS_URL` conditional); memory presence store expiry; proxy routing and `/metrics` deny; supervisor restart backoff, shutdown order, boot refusals; MinIO readiness; seed flag and exit codes | stdlib `testing` | `internal/config`, `internal/presence`, `internal/cloudproxy`, `services/cloud`, `tools/seed` |
| Property (Go) | fanout payload parity Redis ↔ NATS (random batches); memory presence store ↔ Redis within TTL (random sequences) | `pgregory.net/rapid` (D-37) | `internal/fanout`, `internal/presence` |
| Integration (Go) | ingest → hub over embedded NATS with no Redis (fleet isolation); presence over NATS; proxy → hub WebSocket upgrade; `/healthz` unauthenticated with testinfra Postgres; configured origin accepted; seed non-interactive reruns, password never logged; embedded NATS JetStream ready | testcontainers (Postgres, Redis) + embedded `nats-server` (real, in-process) (D-37) | `internal/hub`, `internal/ingest`, `internal/cloudproxy`, `services/api`, `tools/seed` |
| Race | `-race` on fanout, hub, presence, ingest, cloudproxy, cloud | `make test` (existing `-race`) | all |
| Static | Dockerfile shape; `.dockerignore` secrets exclusion; CI workflow shape (yaml.v3); route inventory diff; compose byte-identical; scope fence | Go test in `deploy/cloud/cloud_test.go`; shell checks in Validation | `deploy/cloud` |
| Frontend unit | `wsUrl` default and `VITE_WS_BASE` | Vitest (existing) | `apps/web` |
| Pages functions | allowlist proxy, navigation fallback, `/stream` upgrade, missing env | `node --test` with fake `env`/`fetch` | `apps/*/functions-test` |
| Build | web and driver-pwa production builds | `npm run build` | validation (h) |
| Container | `docker build` on host; CI image job build; orchestrator smoke (S0–S9) | docker, `make cloud-smoke` | validation (f), G7, orchestrator |
| Gates unchanged | `make fmt-check lint test`; `make e2e` (CI job; local run only if deps present) | existing | CI and local |

Test flakiness policy (P23 precedent): an isolated re-run of the affected package before failing anything.

## SPEC-AMEND repeat

**Expected: YES.** P24 adds new ledger rows. Each has a decision ID in `tech-stack.md` §11 (GF-347…GF-355). The persist step writes these rows exactly. They are flagged for user review at the next user contact, the same as the 2026-10-25 rows.

Exact mission §7 rows (append to the table in `specs/constitution/mission.md`):

| ID | Deviation / addition | README ref | Type | Phase |
|---|---|---|---|---|
| GF-347 (env) | `FANOUT_MODE` env: `redis` (default) or `nats`; selects the fanout transport and presence store; validated in `config.Load` (not a `FLEETOPS_*` tunable) | §12 env list (additive); tech-stack §11 D-41 | SPEC-AMEND additive env | P24 |
| GF-347 (REDIS_URL) | `REDIS_URL` required only when `FANOUT_MODE=redis` | §12 (names stay listed) | SPEC-AMEND deviation | P24 |
| GF-347 (D-17) | D-17 scoped override: in `nats` mode `positions.batch.<fleet_id>` and `presence.changed.<fleet_id>` travel on core NATS, not Redis; the default mode is unchanged | §3, §4, §7 (D-17 row) | SPEC-AMEND override (cloud profile only) | P24 |
| GF-349 | Deployment layout: merged cloud container (`deploy/cloud/Dockerfile.render`, `fleetops-cloud` supervisor, embedded NATS, one public port `PORT` default 8000, `/data` ephemeral); README §15 "in-process channels" implemented as embedded NATS core because services are separate processes | §15 table | SPEC-AMEND deviation | P24 |
| GF-350 | MinIO binary added to the cloud container (README §15 table omits it) because the api refuses boot without MinIO (GF-201); proof photo URLs not browser-reachable in cloud | §15 table, §12, GF-201 | SPEC-AMEND deviation | P24 |
| GF-351 | `SEED_PASSWORD` env and `--password-from-env` flag for non-interactive seeding; interactive path unchanged | §12 (additive env); GF-23 | SPEC-AMEND additive env and flag | P24 |
| GF-353 | `WS_ORIGIN_PATTERNS` env: optional hub origin allowlist | §12 (additive env) | SPEC-AMEND additive env | P24 |
| GF-354 | `GET /healthz` unauthenticated liveness route on the api; exempt from the role matrix (GF-329 precedent) | §6 (no health route) | SPEC-AMEND additive endpoint | P24 |
| GF-355 | CI jobs `image` (GHCR push, `ghcr.io/38ai61/fleetops`, lowercase) and `deploy` (Render hook from secret `RENDER_DEPLOY_HOOK_URL`) on `main` only | §10 CI; GF-247 precedent | SPEC-AMEND additive infra | P24 |
| D-41 (release) | Cloud profile released to scope in cloud mode; local compose and `redis` default unchanged | tech-stack §11 D-41 | Release (status change) | P24 |

Tech-stack §11 rows (append, same style as D-41): one row per GF-347…GF-355 with the decision text from Requirements and its rationale.

Not SPEC-AMEND (recorded as decisions in this spec only): `functions/` Pages proxies and the `VITE_WS_BASE` fallback (GF-352; README §15 already says Pages proxies `/stream`); driver PWA not proxying `/stream`; `/metrics` denied on the public port (within GF-349 and P22's loopback rule); `apps/*/functions-test` layout; `deploy/PORTS.md` smoke row.

## Out of scope

- Render, Neon, Cloudflare Pages, and GHCR account wiring; creating the Render service; setting secret values (`RENDER_DEPLOY_HOOK_URL`, Render env, Pages env); making the GHCR package public. These are orchestrator steps after merge.
- Any secret value in code, docs, CI, or logs. Env names only (D-40).
- Compose changes (`deploy/docker-compose.yml` byte-identical).
- README edits (R-8) and `roadmap.md` edits (STATE.md is the orchestrator's ledger).
- Migrations. Neon is at version 12 and nothing in P24 needs a schema change. No `make migrate` against Neon.
- k6 and the 2,000 msg/s burst (local only, README §14).
- Multi-instance scaling, Redis-backed presence in cloud, and Supabase or Neon connection pooling (`DATABASE_URL` is used as-is).
- Public MinIO access and browser-reachable proof photo URLs in cloud (GF-350 known limitation).
- Pages preview deployments, cookie `Domain` tuning, and custom domains.
- e2e spec changes (`e2e/**`).
- Push and merge: the orchestrator performs `--no-ff` merge to `main` and `git push origin main`.
