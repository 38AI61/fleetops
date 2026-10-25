# Feature 01 — Toolchain, CI, migrations baseline (P1)

Status: approved plan, persisted 2026-10-25. Branch `phase-1-toolchain`.
Source plan: `/root/.claude/plans/p1-toolchain-composed-pebble.md` (approved). Binding constitution: `specs/constitution/roadmap.md` §3 P1, `specs/constitution/tech-stack.md` §2, §3, §9, §11 (D-13, D-37, D-39, D-40, GF-5…GF-18).

## Scope

Roadmap P1 (`specs/constitution/roadmap.md` §3), verbatim:

> **P1 — Toolchain, CI, migrations baseline** · D-13 · README §10 P0 remainder
> - Scope: Go module; Makefile (`migrate`, `test`, `lint`, `seed`); golangci-lint; GitHub Actions (fmt, lint, test, `-race` on ingest/hub); golang-migrate baseline (`postgis` extension, `fleets`); shared testcontainers helper; `db/migrations/`.
> - Validation: CI green on an empty-feature PR; `make migrate` up → down → up on a PostGIS container.
> - Testing: smoke — migration round-trip test.

P1 is the first phase that creates Go code. P1 delivers: a Go module; a golang-migrate baseline (`postgis` extension + `fleets`) under `db/migrations/`; a shared testcontainers helper; a Makefile (`migrate`, `test`, `lint`, `seed`, plus reserved `seed-demo`); a golangci-lint config; GitHub Actions workflows that mirror the local gates; a CLAUDE.md update. Done = local gates green, `make migrate` up → down → up on a PostGIS container, and the round-trip smoke test passing.

Branch `phase-1-toolchain` is the feature branch. No git remote exists and `gh` is unauthenticated, so the workflows are committed but cannot run remotely in this phase. CI parity is validated by running the same make targets locally.

### Verified facts that shape the plan

- Toolchain (none are on PATH): `/root/.local/go/bin/go` (go1.27.3), `/root/.local/go/bin/gofmt`, `/root/.local/bin/golangci-lint` (v2.15.0), `/root/.local/bin/migrate` (golang-migrate v4.18.3, built from source, reports `dev`).
- `postgis/postgis:16` does not exist on Docker Hub. `postgis/postgis:16-3.4` is pullable. The compose file still references the broken tag (`deploy/docker-compose.yml:11`). Compose is not edited in P1, so `docker compose up` stays broken until the phase that next edits compose (P4).
- `.gitignore` does **not** contain `deploy/.local/`, although tech-stack §3 says it is gitignored.
- `.github/` does not exist and is not in the D-39 layout list.
- `CLAUDE.md` lines 3 and 5 say `specs/` is "not yet present"; it now exists. `CLAUDE.md` and `deploy/PORTS.md` both say MinIO is "phase 3"; the binding phase is P12 (D-07).
- `deploy/PORTS.md` rule: "Do not bind any other host port in this project." Testcontainers picks OS-assigned host ports (see GF-13).
- `docker-compose.yml` has no `-js` on NATS yet (P4), so nothing in P1 touches NATS.

## Plan

Task groups in build order. Each group lists its files and its tests. Tests are real `go test` tests where the group has Go code, and explicit shell checks otherwise. Every check is re-listed in Validation.

### T0 — Feature spec (R-1: spec before implement)

- **Create** `specs/features/01-toolchain-ci-migrations.md` containing: Scope (roadmap P1 sentence), Plan (T1–T8 in order), Requirements (GF-5…GF-18 and reused D-IDs), Validation (V0–V9), Testing depth, SPEC-AMEND repeat (GF-12), out-of-scope list.
- **Also add** the SPEC-AMEND row to `specs/constitution/mission.md` §7 and the GF-5…GF-18 rows to `specs/constitution/tech-stack.md` §11 (gap-fill table), per D-40.
- **Tests:** V7 (spec has all required headings: `grep -c '^## '` ≥ 6 with names Scope, Plan, Requirements, Validation, Testing, Out of scope; GF-5…GF-18 each present in tech-stack §11).

### T1 — Go module bootstrap and repo hygiene

- **Create** `go.mod` (`module fleetops`, `go 1.27`, `toolchain go1.27.3`).
- **Edit** `.gitignore`: add `deploy/.local/`.
- **Tests:** `go list -m` prints `fleetops`; `gofmt -l .` empty; `git check-ignore -q deploy/.local/x` exits 0.

### T2 — Baseline migration and embed package

- **Create** `db/migrations/001_postgis_fleets.up.sql`:
  ```sql
  CREATE EXTENSION postgis;
  CREATE TABLE fleets (
    id   BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL
  );
  ```
- **Create** `db/migrations/001_postgis_fleets.down.sql`:
  ```sql
  DROP TABLE fleets;
  DROP EXTENSION postgis;
  ```
- **Create** `db/migrations.go` (`package db`, `//go:embed migrations/*.sql` into `var Migrations embed.FS`).
- **Create** `db/migrations_test.go` (package `db`, stdlib, no infrastructure):
  - `TestMigrationFilesArePaired`: every `.up.sql` has a `.down.sql` and vice versa; versions are contiguous from 001 with no gaps or duplicates; names match `^\d{3}_[a-z0-9_]+\.(up|down)\.sql$`.
  - `TestMigrationFilesAreTransactionSafe`: neither file contains `BEGIN`, `COMMIT`, or `CONCURRENTLY` (case-insensitive) (GF-5).
- **Tests:** the two tests above, pass.

### T3 — Shared testcontainers helper

- **Create** `internal/testinfra/postgis.go`:
  - `const PostgisImage = "postgis/postgis:16-3.4"` (GF-12).
  - `func StartPostgis(t testing.TB) (dsn string)`: starts the testcontainers-go `modules/postgres` container with that image, database `fleetops`, user `fleetops`, a random password per run (generated, never a literal), `sslmode=disable`; registers `t.Cleanup` to terminate it; returns the DSN.
  - Helper fails the test (`t.Fatal`) with the image name in the message if Docker is unavailable.
- **Create** `internal/testinfra/postgis_test.go`:
  - `TestStartPostgisHasPostGIS`: opens the DSN with `database/sql` + lib/pq and runs `SELECT postgis_lib_version()` (must return a non-empty string).
- **Tests:** `TestStartPostgisHasPostGIS` pass. The helper is exercised by T4.
- **Note:** pin the latest testcontainers-go release that supports Docker 29 (host runs 29.1.3; older clients can fail against the new minimum API version). This is verified by the test itself.

### T4 — Migration round-trip smoke test (roadmap P1 testing)

- **Create** `db/roundtrip_test.go` (package `db_test`):
  - `TestMigrationRoundTrip`: `dsn := testinfra.StartPostgis(t)`; builds a `golang-migrate` instance with `source/iofs` over `db.Migrations` (subdir `migrations`) and `database/postgres`.
  - Up → assert `Version()==1`, `dirty==false`, `fleets` table present (`information_schema`), `postgis` present in `pg_extension`.
  - Down → assert `Version()` returns `migrate.ErrNilVersion`, `fleets` absent, `postgis` absent from `pg_extension`.
  - Up again → same assertions as the first Up.
  - A second `Up()` on the already-current schema returns `migrate.ErrNoChange` (idempotence).
- **Tests:** `TestMigrationRoundTrip` pass; this is the roadmap smoke test.

### T5 — Makefile

- **Create** `Makefile` with:
  - Header: `-include deploy/.local/tools.mk` (host-only, gitignored; GF-6), then `GO ?= go`, `GOFMT ?= gofmt`, `GOLANGCI_LINT ?= golangci-lint`, `MIGRATE ?= migrate`, `GOLANGCI_LINT_VERSION := v2.15.0`, `MIGRATE_VERSION := v4.18.3`, `MIGRATE_ARGS ?= up`.
  - `.PHONY` for all targets; `.DEFAULT_GOAL := help`.
  - `help`: lists targets with a one-line description each (grep of `## ` comments).
  - `tools`: `$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)` and `$(GO) install -tags postgres github.com/golang-migrate/migrate/v4/cmd/migrate@$(MIGRATE_VERSION)` (GF-16).
  - `fmt-check`: runs `$(GOFMT) -l .`; if the output is non-empty, prints it and exits 1.
  - `lint`: depends on `fmt-check`; runs `$(GOLANGCI_LINT) run`.
  - `test`: `$(GO) test ./... -race -count=1`.
  - `migrate`: the GF-6 recipe; `@`-prefixed; exits 2 with `DATABASE_URL is not set` when unset.
  - `seed`: prints `seed: tools/seed lands in P2 (D-05)` to stderr, exits 1 (GF-7).
  - `seed-demo`: prints `seed-demo: lands in P23 (roadmap)` to stderr, exits 1 (GF-7).
- **Create** `deploy/.local/tools.mk` **on this host only** (not committed; gitignored after T1): `GO := /root/.local/go/bin/go`, `GOFMT := /root/.local/go/bin/gofmt`, `GOLANGCI_LINT := /root/.local/bin/golangci-lint`, `MIGRATE := /root/.local/bin/migrate`.
- **Tests:** V5 checks: `make help` lists every target; `make seed` and `make seed-demo` exit 1 with the stated message; `env -u DATABASE_URL make migrate` exits 2 with the message; `make -n test` prints the `go test ./... -race -count=1` line; `make fmt-check` and `make lint` and `make test` exit 0 (exercised again in V2/V3).

### T6 — golangci-lint configuration

- **Create** `.golangci.yml`: `version: "2"`; `linters.default: standard`; `linters.enable: [errorlint, gosec, misspell]`; `formatters.enable: [gofmt]` (GF-9).
- **Tests:** `golangci-lint config verify` exits 0; `golangci-lint run -v` reports `errorlint`, `gosec`, `misspell`, and `gofmt` among active linters; `golangci-lint run` exits 0 over the T1–T4 code.

### T7 — GitHub Actions workflow

- **Create** `.github/workflows/ci.yml`:
  - Triggers: `push` (all branches) and `pull_request` (branch `main`). `permissions: contents: read`.
  - One job `gates` on `ubuntu-latest`, `timeout-minutes: 20`:
    1. `actions/checkout@v4`
    2. `actions/setup-go@v5` with `go-version-file: go.mod` and `cache: true`
    3. Step: `echo "$(go env GOPATH)/bin" >> "$GITHUB_PATH"`
    4. `make tools`
    5. `make fmt-check`
    6. `make lint`
    7. `make test`
    8. Step "start postgres": `PGPW=$(openssl rand -hex 16)`; `echo "::add-mask::$PGPW"`; `docker run -d --name fleetops-ci-pg -e POSTGRES_USER=fleetops -e POSTGRES_DB=fleetops -e POSTGRES_PASSWORD="$PGPW" -p 127.0.0.1:55432:5432 postgis/postgis:16-3.4`; wait loop with `docker exec fleetops-ci-pg pg_isready -U fleetops`; then writes `DATABASE_URL=postgres://fleetops:$PGPW@127.0.0.1:55432/fleetops?sslmode=disable` to `$GITHUB_ENV`.
    9. `make migrate` (up), then `make migrate MIGRATE_ARGS="down 1"`, then `make migrate` (up).
    10. `if: always()` step: `docker rm -f fleetops-ci-pg`.
- **Tests:** V6 — YAML parses (`python3 -I -c 'import yaml,sys; yaml.safe_load(open(sys.argv[1]))' .github/workflows/ci.yml`, pyyaml must be present; if absent, install it in a venv outside the repo or report and fall back to `go run` with `gopkg.in/yaml.v3` in a scratch dir); every gate command above appears; `git grep -nE 'POSTGRES_PASSWORD[=:] *[A-Za-z0-9]' -- .github` finds only the generated variable. `actionlint` is not installed; that limitation is recorded in the final report.

### T8 — CLAUDE.md update (D-40, GF-15)

- **Edit** `CLAUDE.md`:
  - Replace the "not yet present" lines with the current state (specs exist, `specs/constitution/`).
  - Correct MinIO to "reserved, phase 12" (D-07); same fix in `deploy/PORTS.md` (text only).
  - Add `## Make targets`: `make help`, `make tools`, `make fmt-check`, `make lint`, `make test`, `make migrate` (+ `MIGRATE_ARGS="down 1"` and `MIGRATE_ARGS="force <version>"` recovery), `make seed` / `make seed-demo` (stubs, phases stated).
  - Add `## Tooling`: pinned versions; host override via `deploy/.local/tools.mk`; `DATABASE_URL` required for `make migrate`; migration rules (GF-5) in four bullets; testcontainers image `postgis/postgis:16-3.4` (GF-12).
  - Keep the env-variable list unchanged (names match README §12).
- **Tests:** V7 — `wc -l CLAUDE.md` < 200; every target in `make help` is mentioned in CLAUDE.md; the env-name list is byte-identical to the pre-edit list; `grep -n 'not yet present' CLAUDE.md` is empty.

## Requirements

### Decisions

New decision IDs for P1 (zero open).

| ID | Decision | Rationale |
|---|---|---|
| **GF-5** | Migrations: sequential, zero-padded 3-digit `NNN_snake_name.up.sql` / `.down.sql` in `db/migrations/`, starting `001`. No timestamps. Each file is a single transaction-friendly script: no explicit `BEGIN`/`COMMIT`, no `CREATE INDEX CONCURRENTLY`. Migrations are forward-only once merged to `main`; `down` exists to restore the pre-state for dev and the round-trip test. **Dirty state:** `make migrate` exits non-zero when `schema_migrations.dirty = true`. Recovery is manual: inspect the schema, fix it by hand, then `make migrate MIGRATE_ARGS="force <version>"`. Nobody edits `schema_migrations` by SQL. | One feature branch per phase (R-1/R-2) makes sequential numbers safe and readable. golang-migrate's dirty flag is the only safe signal; documenting recovery in CLAUDE.md avoids ad-hoc SQL. |
| **GF-6** | Makefile wraps the `migrate` CLI directly, with no custom Go wrapper. `make migrate` runs `$(MIGRATE) -path db/migrations -database "$$DATABASE_URL" $(MIGRATE_ARGS)`, default `MIGRATE_ARGS=up`; `make migrate MIGRATE_ARGS="down 1"` for down. `DATABASE_URL` is required (the recipe exits 2 with a message if unset), is referenced as `$$DATABASE_URL` so make never echoes its value, and is **not** read from `deploy/.env`. Tool locations are `?=`/`:=` variables. Host-specific paths go in `deploy/.local/tools.mk` (gitignored, written on this host only) and are pulled in with `-include` before the `?=` defaults. | Committing `/root/.local/...` paths would leak host layout; the gitignored override keeps the repo portable. Not reading `.env` avoids parsing shell-style secrets in make. |
| **GF-7** | Target timing: `seed` is a stub in P1 that prints `seed: tools/seed lands in P2 (D-05)` to stderr and exits 1. `seed-demo` is also a stub that exits 1, naming P23. Both names are reserved now because tech-stack §3 binds the target set, but no fake behavior is shipped. | Roadmap P1 scope lists `seed`; tech-stack §3 lists `seed-demo`. A failing stub is honest and keeps `make help` complete. |
| **GF-8** | Locations: testcontainers helper is `internal/testinfra/` (a library, test-only; `tools/` holds binaries). Embedded migrations live in package `db` at `db/migrations.go` (`//go:embed migrations/*.sql`); the package is named `db`, so the directory and import path are `fleetops/db`. | `internal/` prevents import outside the module; `tools/` is reserved for `cmd` binaries (D-39). Embedding avoids a relative path from tests. Embedding does not run migrations, so "never at app boot" still holds. |
| **GF-9** | Lint: golangci-lint v2 config `version: "2"`. Linters = `standard` (errcheck, govet, ineffassign, staticcheck, unused) + `errorlint`, `gosec`, `misspell`. Formatter: `gofmt`. `revive`, `gocritic`, `unparam`, and `goimports` are **not** enabled in P1. | Keeps a near-empty module's signal high and deterministic. The extra set covers the security defaults (R-9) and error wrapping, which matter before auth lands. Stricter linters can be added in the phase whose code they would judge. |
| **GF-10** | Go module path: `fleetops` (bare, no host). `go.mod` pins `go 1.27` and `toolchain go1.27.3`. | No remote or GitHub owner exists; inventing one would be wrong. Nothing external imports the module yet, so renaming later is a mechanical change. |
| **GF-11** | CI shape: one workflow `.github/workflows/ci.yml`, one job `gates` on `ubuntu-latest`, `permissions: contents: read`, `timeout-minutes: 20`. Each gate runs a make target: `make tools`, `make fmt-check`, `make lint`, `make test` (`-race ./...`, global for P1; ingest/hub inherit it), then a Postgres service started by a `docker run` step with a **generated** password (`openssl rand -hex 16`, masked with `::add-mask::`, exported to `GITHUB_ENV`), then `make migrate`, `make migrate MIGRATE_ARGS="down 1"`, `make migrate`. A final `if: always()` step removes the container. No static password appears in the repo. | Same gates locally and in CI through the same targets. Generating the password at runtime respects AGENTS.md (no passwords in repo). Running `-race` across `./...` is a superset of the roadmap's "ingest and hub" requirement. |
| **GF-12** | Image tag for tests and CI: `postgis/postgis:16-3.4`, defined once as `testinfra.PostgisImage`. README §12 and compose keep `16` until compose is touched (P4). This is a **SPEC-AMEND** (image tag correction) and gets a row in mission §7 and in tech-stack §11 (D-40). Compose is untouched in P1. | Verified pullable tag. The gap between README and compose is recorded rather than silently changed. |
| **GF-13** | Testcontainers host ports are OS-assigned ephemeral ports. The PORTS.md "no other host port" rule applies to long-running dev and compose services, which keep their fixed range (`127.0.0.1` binds). Test containers are short-lived and never bind 10000–10019. | Testcontainers cannot practically use fixed ports for parallel test runs. The rule's intent is stable dev ports, and PORTS.md stays accurate. |
| **GF-14** | `fleets` baseline is exactly README §5: `fleets(id BIGSERIAL PK, name TEXT NOT NULL)`. Fleet timezone (needed by D-05 `--timezone` and D-32) arrives as an additive migration in P2, not in 001. | Keeps the baseline to the roadmap scope. The timezone column belongs to the phase that seeds it. |
| **GF-15** | Repo hygiene in P1: add `deploy/.local/` to `.gitignore` (tech-stack §3 requires it); add `.github/workflows/` to the layout (D-39 gap); fix `CLAUDE.md` lines 3/5 ("specs not yet present") and the MinIO phase text in `CLAUDE.md` and `deploy/PORTS.md` (`phase 3` → `phase 12`, D-07). No port value changes. | Stale facts in mechanical docs violate CLAUDE.md's purpose. Each fix is a text correction, not a behavior change. |
| **GF-16** | Tool pins: `go1.27.3` (`go.mod` toolchain line); `GOLANGCI_LINT_VERSION := v2.15.0` and `MIGRATE_VERSION := v4.18.3` in the Makefile, consumed by `make tools` (`go install …@version`). `migrate` is installed with `-tags postgres`. On this host the binary reports `dev` (built from source); the functional round-trip is the acceptance check, not the version string. Library pins (testcontainers-go, golang-migrate library, lib/pq) are the exact latest stable versions resolved during implement and recorded in `go.mod`. | D-13 requires pinned tool versions; `go install @vX` makes CI deterministic. The `dev` string is a known, accepted artifact of the host build. |
| **GF-17** | `pgregory.net/rapid` is **not** added in P1 (roadmap P1 defines no property test). It enters with the first property test (P3) and is pinned then (D-37). | Avoids an unused dependency. |
| **GF-18** | Driver for test and round-trip code: `github.com/lib/pq` via golang-migrate's `database/postgres` driver. The Go application driver choice is deferred to the phase that first needs one (P2). | Only one driver is needed in P1; the migrate library already brings lib/pq. |

Reused decision IDs:

- **D-13** — CI, golangci-lint, golang-migrate, Makefile, `db/migrations/`, `make migrate` explicit and never at boot, tool versions pinned.
- **D-37** — stdlib `testing`, testcontainers-go PostGIS module, no mocks of infra; rapid deferred per GF-17.
- **D-40** — one feature spec `specs/features/01-toolchain-ci-migrations.md`; CLAUDE.md updated in the same branch; SPEC-AMEND rows in mission §7 and tech-stack §11.
- **D-39** — layout additions `db/migrations/`, `Makefile`, `deploy/.local/`.
- **D-36** — PORTS.md unchanged in value.
- **D-05** — seed stub only; real behavior in P2.
- **D-07** — MinIO is P12; text fix only.

### Consolidated requirements (binding)

1. Go module `fleetops`, Go 1.27.3 toolchain (GF-10, GF-16).
2. Migrations live in `db/migrations/`, numbered, paired, transaction-safe, applied only by `make migrate` (D-13, GF-5, GF-8).
3. Baseline 001 contains exactly `postgis` and `fleets(id, name)` (GF-14).
4. Test helper `internal/testinfra` uses `postgis/postgis:16-3.4` via testcontainers; no mocks of infra (D-37, GF-12).
5. Smoke test: migration round-trip up → down → up on PostGIS (roadmap P1 testing).
6. Lint config v2 per GF-9; formatter gofmt.
7. Makefile targets per GF-6, GF-7; tool versions pinned per GF-16; `DATABASE_URL` never echoed.
8. CI workflow runs the same make targets as the local gates, in the order listed in T7 (GF-11).
9. No compose change, no README change, no port value change, no secret in repo (AGENTS.md, R-8, R-9).
10. CLAUDE.md < 200 lines, mechanical, and updated in this branch (R-3, D-40).

## Validation

Run in this order from the repo root. Commands use the host tool paths; the make targets are also exercised with `make` overrides so no PATH change is needed.

- **V0 Preflight:** `/root/.local/go/bin/go version` prints go1.27.3; `/root/.local/bin/golangci-lint version` prints v2.15.0; `docker info` succeeds; `gcc --version` succeeds (CGO is required for `-race`). **Pass:** all four succeed. If gcc is missing, stop and report; `-race` cannot run without it.
- **V1 fmt:** `/root/.local/go/bin/gofmt -l .` prints nothing. **Pass:** empty output, exit 0.
- **V2 lint:** `make GO=/root/.local/go/bin/go GOLANGCI_LINT=/root/.local/bin/golangci-lint lint` exits 0; `/root/.local/bin/golangci-lint config verify` exits 0. **Pass:** both exit 0.
- **V3 test:** `make GO=/root/.local/go/bin/go test` exits 0; `/root/.local/go/bin/go test ./... -race -count=1 -v` shows `--- PASS` for `TestMigrationFilesArePaired`, `TestMigrationFilesAreTransactionSafe`, `TestStartPostgisHasPostGIS`, `TestMigrationRoundTrip`. **Pass:** exit 0, all four PASS, none SKIP.
- **V4 migrate round trip on a dev PostGIS container (CLI path, the make target):**
  1. `docker run -d --name fleetops-p1-pg -e POSTGRES_USER=fleetops -e POSTGRES_PASSWORD=fleetops-dev -e POSTGRES_DB=fleetops -p 127.0.0.1:10002:5432 postgis/postgis:16-3.4`; wait until `docker exec fleetops-p1-pg pg_isready -U fleetops` succeeds.
  2. `DATABASE_URL='postgres://fleetops:fleetops-dev@127.0.0.1:10002/fleetops?sslmode=disable' make MIGRATE=/root/.local/bin/migrate migrate` → exit 0.
  3. Same with `MIGRATE_ARGS="down 1"` → exit 0.
  4. Same as step 2 → exit 0.
  5. `docker exec fleetops-p1-pg psql -U fleetops -d fleetops -tAc "select to_regclass('public.fleets') is not null, exists(select 1 from pg_extension where extname='postgis')"` prints `t|t`.
  6. `docker exec … psql … -tAc "select dirty, version from schema_migrations"` prints `f|1`.

  **Pass:** every step exits 0; the final state is `t|t` and `f|1`. Then `docker rm -f fleetops-p1-pg`.
- **V5 make targets:**
  - `make help` exits 0 and lists `help tools fmt-check lint test migrate seed seed-demo`.
  - `make seed` exits 1 with `tools/seed lands in P2`; `make seed-demo` exits 1 with `lands in P23`.
  - `env -u DATABASE_URL make migrate` exits 2 with `DATABASE_URL is not set`.
  - `make -n test` prints `go test ./... -race -count=1`.
  - `make -n migrate DATABASE_URL=x` prints the migrate line with `"$$DATABASE_URL"` unexpanded (password not echoed by make).
  - **Pass:** all as stated.
- **V6 workflow:** YAML parses; the gate commands in T7 appear in order (`make tools`, `make fmt-check`, `make lint`, `make test`, `make migrate`); no literal password under `.github`. **Pass:** parse OK, grep hits, no literal. CI itself cannot run here: no remote, `gh` unauthenticated. The "CI green on an empty-feature PR" roadmap check is **not verifiable in P1 on this host**; it runs when the repo has a remote. The local mirror (V1–V5) is the proof for now.
- **V7 docs and spec:** `wc -l CLAUDE.md` < 200; every `make help` target is mentioned in CLAUDE.md; the env-name list is unchanged (`diff` of the list against the pre-edit copy is empty); `grep -n 'not yet present' CLAUDE.md` empty; `specs/features/01-toolchain-ci-migrations.md` exists with headings Scope, Plan, Requirements, Validation, Testing, Out of scope.
- **V8 scope guard:** `git status --short` shows only: `go.mod`, `.gitignore`, `db/`, `internal/testinfra/`, `Makefile`, `.golangci.yml`, `.github/workflows/ci.yml`, `CLAUDE.md`, `deploy/PORTS.md` (text only), `specs/features/01-toolchain-ci-migrations.md`, `specs/constitution/mission.md` (§7 row), `specs/constitution/tech-stack.md` (§11 rows), plus the untracked `prompts/04-p1-plan.md` which is pre-existing and not ours. **Pass:** no other path changed; `git diff --quiet -- README.md deploy/docker-compose.yml` exits 0; `git check-ignore -q deploy/.local/tools.mk` exits 0; no `services/`, `tools/`, `apps/` directories created; no auth, seed logic, endpoints, or NATS/Redis code.
- **V9 cleanup:** `docker ps -a --filter name=fleetops-` shows no P1 containers left behind. **Pass:** empty.

## Testing depth

- **Smoke:** `TestMigrationRoundTrip` (T4), exercising the T3 helper, on a real PostGIS container. Required by the roadmap.
- **Unit:** `TestMigrationFilesArePaired`, `TestMigrationFilesAreTransactionSafe` (T2). Stdlib only.
- **Helper:** `TestStartPostgisHasPostGIS` (T3).
- **No property tests in P1** (roadmap defines none; rapid deferred per GF-17). **No mocks of infra.** No E2E; no k6.

## SPEC-AMEND repeat

Per GF-12 (D-40): the test and CI image tag is `postgis/postgis:16-3.4`, not `postgis/postgis:16`. Row in `specs/constitution/mission.md` §7; decision in `specs/constitution/tech-stack.md` §11. README §12 and `deploy/docker-compose.yml` keep `16` until the compose-touching phase (P4). No other SPEC-AMEND is introduced by P1.

## Out of scope

- Any P2+ behavior: auth, users, seed logic, endpoints, NATS/Redis app code, MinIO, OSRM data, property tests.
- Compose edits (including the broken `postgis/postgis:16` tag), README edits, port value changes.
- Any edit to `CLAUDE.md` or `deploy/PORTS.md` beyond the T8 / GF-15 text corrections listed above.
- Git operations beyond the implement step: no commit or push unless the user asks.
- Remote CI execution (no remote; `gh` unauthenticated). Validated locally via V1–V5 instead.
- `actionlint` (not installed); workflow syntax is checked by YAML parse only.
- Compose tag fix: recommended as a one-line follow-up for the user to approve; not done in P1.
