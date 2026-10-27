# CLAUDE.md

Mechanical reference for this repo. Product scope and design live in specs/ (constitution in `specs/constitution/`, per-phase feature specs in `specs/features/`).

**specs/ are the source of truth — read specs/ before non-trivial work.**

## Host ports

Full allocation and reserved ports: `deploy/PORTS.md`. All host ports are in 10000–10019.

| Service | Container port | Host port |
|---|---|---|
| postgres | 5432 | 10002 |
| redis | 6379 | 10003 |
| nats | 4222 | 10004 |
| osrm | 5000 | 10005 |
| minio (reserved, phase 12) | 9000 / 9001 | 10006 / 10007 |
| api (reserved, local go run) | 8080 | 10010 |
| ws-hub (reserved, local go run) | 8090 | 10011 |
| web dispatcher (reserved, vite dev) | 5173 | 10012 |
| driver-pwa (reserved, vite dev) | 5174 | 10013 |

## Local infrastructure

Compose file: `deploy/docker-compose.yml`

```
docker compose -f deploy/docker-compose.yml up -d
docker compose -f deploy/docker-compose.yml down
```

The `nats` service runs with `-js` (JetStream, GF-62).

## Make targets

Run from the repo root. `make help` lists every target.

- `make help` — list targets
- `make tools` — install the pinned golangci-lint and migrate CLIs into `$(go env GOPATH)/bin`
- `make fmt-check` — fail if any Go file is not gofmt-clean
- `make lint` — `fmt-check`, then golangci-lint
- `make test` — `go test ./... -race -count=1` (PostGIS tests need Docker)
- `make migrate` — apply migrations to `$DATABASE_URL` (`MIGRATE_ARGS` defaults to `up`)
  - Roll back one step: `make migrate MIGRATE_ARGS="down 1"`
  - Recover from a dirty version: fix the schema by hand, then `make migrate MIGRATE_ARGS="force <version>"`
- `make seed` — bootstrap the first fleet and admin (D-05). Run `SEED_ARGS="--admin-email <email>" make seed`. The password comes from a TTY prompt or one line on stdin, never a flag. Exit 0 when already seeded (no prompt); exit 2 on usage errors.
- `SEED_ARGS="--admin-email <email> --demo-devices" make seed` — first seed only: also creates 50 demo vehicles with tracker devices and writes `deploy/.local/devices.json`. The flag needs `DEVICE_KEY_SALT` (at least 32 bytes).
- `make seed-demo` — stub; exits 1 until P23

## Tooling

- Pins: Go 1.27.3 (`go.mod` toolchain line); golangci-lint v2.15.0 and golang-migrate CLI v4.18.3 (built with `-tags postgres`) in the Makefile.
- Host override: `deploy/.local/tools.mk` (gitignored) may set `GO`, `GOFMT`, `GOLANGCI_LINT`, `MIGRATE` to host paths. The Makefile includes it.
- `make migrate` requires `DATABASE_URL` in the environment; it is not read from `deploy/.env`.
- Local API: `DATABASE_URL=… JWT_SECRET=… DEVICE_KEY_SALT=… NATS_URL=nats://127.0.0.1:10004 REDIS_URL=redis://127.0.0.1:10003 MINIO_ENDPOINT=127.0.0.1:10006 MINIO_KEY=… MINIO_SECRET=… go run ./services/api` listens on `127.0.0.1:10010` (`-addr` overrides). It does not run migrations. MinIO must be reachable at boot (P12).
- Local ingest consumer: same env as the API, `DATABASE_URL=… JWT_SECRET=… DEVICE_KEY_SALT=… NATS_URL=nats://127.0.0.1:10004 REDIS_URL=redis://127.0.0.1:10003 go run ./services/ingest`. No listener; it drains `GPS_RAW` into `positions`, publishes to Redis, and runs partition maintenance.
- Local WS hub: same env as the API, `DATABASE_URL=… JWT_SECRET=… DEVICE_KEY_SALT=… NATS_URL=nats://127.0.0.1:10004 REDIS_URL=redis://127.0.0.1:10003 go run ./services/ws-hub` listens on `127.0.0.1:10011` (`-addr` overrides). It does not connect to Postgres. Clients open `WS /stream?fleet=<id>` and send `{"type":"auth","token":…}` as the first frame.
- Local simulator (P8): `go run ./tools/simulator --fleet=50 --rate=5s --api http://127.0.0.1:10010`. It reads `deploy/.local/devices.json` and needs no env vars.
- `JWT_SECRET` must be at least 32 bytes or the API and `make seed` refuse to start; generate one per shell with `openssl rand -hex 32`.
- `DEVICE_KEY_SALT` is required and must be at least 32 bytes, or the API refuses to start before it opens the database (GF-30). Generate one per shell with `openssl rand -hex 32`. It is the HMAC key for device keys (D-29) and license hashes (D-31).
- **Salt rotation (GF-50):** changing `DEVICE_KEY_SALT` invalidates every device key and every license hash. P3 ships no re-keying tool.
- Registry (P3): `/vehicles`, `/drivers`, `/vehicles/:id/pairings` follow the README §8 role matrix. Device endpoints (`POST /devices`, `POST /devices/:id/rotate`, `POST /devices/:id/revoke`) are admin only; a device key never authenticates them (GF-47). Issued and rotated keys are shown once.
- Ingest (P4): `POST /ingest/position` is device-key authenticated. The key goes in the `X-Device-Key` header, never in the body or `Authorization`. Any JWT gets 401 on this route (GF-52, GF-64). Startup creates the `GPS_RAW` stream (GF-59).
- Map (P8): `GET /map/vehicles?bbox=minLng,minLat,maxLng,maxLat`, JWT, any role, fleet-scoped.
- Migration rules (GF-5):
  - Files are `db/migrations/NNN_snake_name.up.sql` and `.down.sql`, sequential from `001`, no timestamps.
  - Each file is one transaction-friendly script: no `BEGIN`/`COMMIT`, no `CREATE INDEX CONCURRENTLY`.
  - Migrations are forward-only once merged to `main`; `down` exists for dev and the round-trip test.
  - A dirty `schema_migrations` makes `make migrate` fail. Fix the schema by hand, then `force`. Never edit `schema_migrations` by SQL.
- Tests and CI use `postgis/postgis:16-3.4` (`testinfra.PostgisImage`, GF-12). Compose uses the same tag (GF-62).
- CI (`.github/workflows/ci.yml`) runs the same make targets as the local gates.

## Environment variables

Names from README §12. Values come from the environment or `deploy/.env` (gitignored). Never commit values.

- `DATABASE_URL`
- `NATS_URL`
- `REDIS_URL`
- `OSRM_URL`
- `MINIO_ENDPOINT`
- `MINIO_KEY`
- `MINIO_SECRET`
- `JWT_SECRET`
- `DEVICE_KEY_SALT`
- `POSTGRES_PASSWORD` (read by the postgres service in compose; local-dev default applies if unset)

## Optional tunables

Values come from the environment, like the names above. Unset or empty means the default. `config.Load` refuses an invalid value, and the error names the variable, never the value.

| Variable | Default | Allowed |
|---|---|---|
| `FLEETOPS_FLUSH_INTERVAL` | `1s` | Go duration from `100ms` to `10s` |
| `FLEETOPS_FLUSH_MAX` | `500` | integer from `1` to `5000` |
| `FLEETOPS_ACCURACY_MAX_M` | `50` | finite number above `0` and at most `10000` |
| `FLEETOPS_WS_ROOM_CAP` | `2000` | integer from `1` to `10000` |
| `FLEETOPS_PRESENCE_TTL` | `120s` | Go duration from `30s` to `600s` |

## Frontend (P9)

- Install: `npm --prefix apps/web ci --include=dev`
- Dev server: `npm --prefix apps/web run dev` → `http://localhost:10012` (`http://127.0.0.1:10012`)
- Build (typecheck + build): `npm --prefix apps/web run build`
- Typecheck only: `npm --prefix apps/web run typecheck`
- Unit and component tests: `npm --prefix apps/web test`
- Dev proxy: `/auth`, `/map`, `/vehicles`, `/jobs` → `127.0.0.1:10010`; `/stream` → `127.0.0.1:10011` (WebSocket)

## Jobs (P10)

- Dispatcher routes (JWT; admin and dispatcher unless noted): `POST /jobs` (state `created`, unassigned), `GET /jobs?state=&driver=&day=` (admin, manager, dispatcher), `POST /jobs/:id/assign` with `{"vehicle_id","driver_id"}` (also reassigns while `assigned`), `POST /jobs/:id/cancel` (empty body or `{}`).
- Every jobs mutation runs `SELECT set_config('app.actor_id', …, true)` as its first statement; the trigger writes `job_events`. A state or assignment change without that setting fails with `FL428`.
- D-23 edges live in `internal/jobs/transitions.go`; `job_transitions` mirrors them for the trigger.
- `FL409` = illegal transition or assignment change (API 409). `FL428` = actor missing (API 500, programming error).
- `day` filter: `window_start` evaluated in `fleets.timezone`. A NULL window matches only when `day` is absent.
- Publish: `jobs.events.<fleet_id>` on core NATS, no JetStream stream (GF-4). One message per applied transition after commit; the durable record is `job_events`.
- Driver routes (JWT, `driver` role only): `GET /driver/jobs/today`; `POST /driver/jobs/:id/accept|start|pickup` (empty body or `{}`); `POST /driver/jobs/:id/fail` with `{"reason"}` (trimmed, 1 to 1000 runes).
- Driver identity: the caller's driver row is `users.driver_id` in the principal's fleet. An unlinked driver gets `{"jobs":[]}` from `today` and 404 on transitions. Another driver's job, or a job of another fleet, is 404, checked before the replay.
- Driver `today`: active jobs (not delivered, failed or cancelled) whose `window_start` day in `fleets.timezone` is on or before today, plus active jobs with no window. Order `window_start` ASC NULLS LAST, then id.
- Fail reason: `fail` is valid from `accepted`, `en_route` and `picked_up` only. The reason is written in the same UPDATE as the state. It is not in `job_events` or the NATS body. A replayed fail keeps the first reason.

## Proof of delivery (P12)

- Deliver route: `POST /driver/jobs/:id/deliver`, driver role only, own jobs only, valid from `picked_up` only. Multipart fields: `photo` (file; type sniffed by `http.DetectContentType` on the first 512 bytes, declared Content-Type ignored; JPEG, PNG or WebP; at most 5242880 bytes), `lat` and `lng` (text, finite, lat in [-90, 90], lng in [-180, 180]).
- Success is 200 with the job object. A replay of `delivered` is 200 with no event, no publish and no upload; the body is still validated.
- Errors: 400 `validation_failed`; 404 for wrong driver, cross-fleet or malformed id; 409 for any other state; 413 `payload_too_large` for a photo over 5 MiB; 503 `proofs_unavailable` when no proof store is configured. Nothing is stored on any refusal from the gate or body checks.
- Object key `proofs/<fleet_id>/<job_id>/<uuid>.<ext>` in bucket `proofs`. The key is stored verbatim in `jobs.proof_photo`. The same UPDATE sets `delivered_at` and `delivery_geom`.
- Presigned URLs: every job object has `proof_photo_url`, a presigned GET valid 15 minutes, signed on read and never stored. It is null when the job has no proof.
- Body limits: the deliver route allows 8M; every other route keeps the global 64K.
- Boot: the API requires `MINIO_ENDPOINT`, `MINIO_KEY` and `MINIO_SECRET`, ensures the `proofs` bucket, and refuses to start if either step fails.

## Nearest and assignment (P13)

- `GET /jobs/:id/nearest?limit=` (JWT; admin, manager and dispatcher; driver 403). The job must be in the caller's fleet, and a missing, cross-fleet or malformed id is 404. `limit` defaults to 10 and must be an integer 1..50, else 400 `validation_failed`. No job-state gate.
- Response: `{"job_id", "candidates": [{"vehicle_id", "plate", "driver_id", "driver_name", "distance_m", "lat", "lng", "server_ts"}]}`, ordered by `ST_Distance` then vehicle id. `distance_m` is rounded to 0.1 m. Candidates are active vehicles with a latest position and an open pairing to a driver of the fleet.
- UI routes (login required): `/jobs` lists jobs with state, driver and day filters, and opens the assignment panel from a created or assigned row. `/jobs/new` picks pickup and dropoff on the map beside the create form.
