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
- `make seed-demo` — stub; exits 1 until P23

## Tooling

- Pins: Go 1.27.3 (`go.mod` toolchain line); golangci-lint v2.15.0 and golang-migrate CLI v4.18.3 (built with `-tags postgres`) in the Makefile.
- Host override: `deploy/.local/tools.mk` (gitignored) may set `GO`, `GOFMT`, `GOLANGCI_LINT`, `MIGRATE` to host paths. The Makefile includes it.
- `make migrate` requires `DATABASE_URL` in the environment; it is not read from `deploy/.env`.
- Local API: `DATABASE_URL=… JWT_SECRET=… go run ./services/api` listens on `127.0.0.1:10010` (`-addr` overrides). It does not run migrations.
- `JWT_SECRET` must be at least 32 bytes or the API and `make seed` refuse to start; generate one per shell with `openssl rand -hex 32`.
- Migration rules (GF-5):
  - Files are `db/migrations/NNN_snake_name.up.sql` and `.down.sql`, sequential from `001`, no timestamps.
  - Each file is one transaction-friendly script: no `BEGIN`/`COMMIT`, no `CREATE INDEX CONCURRENTLY`.
  - Migrations are forward-only once merged to `main`; `down` exists for dev and the round-trip test.
  - A dirty `schema_migrations` makes `make migrate` fail. Fix the schema by hand, then `force`. Never edit `schema_migrations` by SQL.
- Tests and CI use `postgis/postgis:16-3.4` (`testinfra.PostgisImage`, GF-12). Compose keeps the `16` tag until P4.
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
