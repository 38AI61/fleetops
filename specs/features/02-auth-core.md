# Feature 02 — Auth core (P2)

Status: approved plan, persisted 2026-10-25. Branch `phase-2-auth-core`.
Source plan: `/root/.claude/plans/p2-auth-vivid-bumblebee.md` (approved). Binding constitution: `specs/constitution/roadmap.md` §1 (R-1…R-9) and §3 P2, `specs/constitution/tech-stack.md` §3, §6.3, §7, §9, §11 (D-01…D-06, GF-2, GF-5, GF-7, GF-14, GF-18), `specs/constitution/mission.md` §7 (SPEC-AMEND rows), `AGENTS.md`, `CLAUDE.md`.

This spec is written before any P2 code (R-1, SDD). Implementation starts at Plan task group 1.

## Scope

Roadmap P2 (`specs/constitution/roadmap.md` §3), verbatim:

> **P2 — Auth core** · D-01–D-06, D-02/D-03 endpoints, GF-2 · README §2.1.2, §8
> - Scope: `users`, `refresh_tokens`, `admin_audit`; `POST /auth/login`, `/auth/refresh`, `/auth/logout`; JWT middleware (15 min, `fleet_id` + `role` claims); login throttle (5 per 15 min per (email, IP)); `make seed` bootstrap; admin-only `GET/POST/PATCH /users` (SPEC-AMEND); admin writes record actor.
> - Validation: login → call → refresh → old refresh rejected; bootstrap idempotent; disabled user cannot refresh.
> - Testing: unit (password policy, JWT claims and expiry, throttle) + integration (rotation, reuse detection revokes the family, expiry) + fleet-scoping test (token for fleet A gets 404 on fleet B resources).

P2 delivers: migrations `002`–`004` (`users`, `refresh_tokens`, `admin_audit` with its append-only trigger, `fleets.timezone`); the `services/api` entrypoint with login, refresh, logout, JWT middleware, and admin user endpoints; an in-memory login throttle; the `make seed` bootstrap tool; a role-matrix extension (R-6) and an `admin_audit` trigger test (R-7). Done = the Validation items below hold, the Testing depth exists, and CI is green.

**Reconciliation (roadmap vs D-02).** The roadmap testing line says reuse "revokes the family". D-02 (binding) says reuse "revokes all of that user's tokens". D-02 is the stricter rule and is the one implemented: reuse revokes every refresh token of the user, including tokens from other sessions (other families). The integration test asserts this (Plan §7). No roadmap wording is weakened.

Nothing from P3+ is built in P2: no vehicles, drivers, devices, device keys, pairing, ingest, WS hub, map, jobs, geofences, or alerts.

## Plan

Task groups in build order. Each group ends in a commit on `phase-2-auth-core`. Tests live beside code. Group 15's CLAUDE.md update stays in implement scope (the spec file itself is written by the persist step, before any code).

### 1. Migrations `002`–`004`

- **`db/migrations/002_auth_users_refresh.up.sql`** — `CREATE EXTENSION IF NOT EXISTS citext;`; `users(id BIGSERIAL PK, fleet_id BIGINT NOT NULL REFERENCES fleets(id), email CITEXT NOT NULL UNIQUE, password_hash TEXT NOT NULL, role TEXT NOT NULL CHECK (role IN ('admin','manager','dispatcher','driver')), driver_id BIGINT NULL, disabled_at TIMESTAMPTZ NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now())`; index `users(fleet_id)`; `refresh_tokens(id BIGSERIAL PK, fleet_id BIGINT NOT NULL REFERENCES fleets(id), user_id BIGINT NOT NULL REFERENCES users(id), family_id UUID NOT NULL, token_hash BYTEA NOT NULL UNIQUE, issued_at TIMESTAMPTZ NOT NULL DEFAULT now(), expires_at TIMESTAMPTZ NOT NULL, rotated_at TIMESTAMPTZ NULL, revoked_at TIMESTAMPTZ NULL)`; indexes `(user_id)` and `(family_id)`. No BEGIN/COMMIT.
  - **`002_auth_users_refresh.down.sql`** — drop `refresh_tokens`, `users`, `EXTENSION citext`.
- **`003_admin_audit.up.sql`** — `admin_audit(id BIGSERIAL PK, fleet_id BIGINT NOT NULL REFERENCES fleets(id), actor_id BIGINT NOT NULL REFERENCES users(id), action TEXT NOT NULL, target TEXT NOT NULL, at TIMESTAMPTZ NOT NULL DEFAULT now())`; index `(fleet_id, at)`; function `admin_audit_append_only()` raising `check_violation` with a fixed message; `BEFORE UPDATE OR DELETE … FOR EACH ROW` trigger and `BEFORE TRUNCATE … FOR EACH STATEMENT` trigger (R-7 covers UPDATE/DELETE; TRUNCATE is closed too).
  - **`003_admin_audit.down.sql`** drops the table and the function.
- **`004_fleets_timezone.up.sql`** — `ALTER TABLE fleets ADD COLUMN timezone TEXT NOT NULL DEFAULT 'UTC';` (GF-14).
  - **`004_fleets_timezone.down.sql`** drops the column.
- Test: `db/roundtrip_test.go` extended so that down-to-zero then up succeeds with 002–004 applied (uses the GF-22 library).

### 2. Dependencies and config

- `go get` pins: `github.com/golang-jwt/jwt/v5`, `github.com/labstack/echo/v4`, `golang.org/x/crypto` (promote from indirect), `golang.org/x/term`, `github.com/golang-migrate/migrate/v4` (library; version per GF-22). `go mod tidy`.
- **`internal/config/config.go`** — `Load() (Config, error)` reads `DATABASE_URL` (required) and `JWT_SECRET` (required, `len ≥ 32` bytes, else an error naming the variable, not its value).
- Tests `internal/config/config_test.go` (unit): 31-byte secret refused; 32-byte accepted; missing `DATABASE_URL` refused; error text never contains the secret.

### 3. Password hashing and policy (`internal/auth/password.go`)

- `HashPassword(pw) (string, error)` → PHC-format argon2id string with m=65536, t=3, p=2, 16-byte salt, 32-byte key. `VerifyPassword(hash, pw) (bool, error)` using constant-time compare. `CheckPolicy(pw) error` → min 12 runes, max 256 bytes.
- Dummy hash constant used for unknown-email login to equalize timing.
- Tests (unit): policy boundaries (11, 12, 256, 257 bytes); round trip; wrong password; encoded params parse to the pinned values; malformed hash string → error, not panic.

### 4. JWT issue and verify (`internal/auth/jwt.go`)

- `type Issuer struct{ secret []byte; now func() time.Time; ttl time.Duration }`; `NewIssuer(secret, clock)` refuses `< 32` bytes (defense in depth beside config).
- `Claims{ FleetID int64 "fleet_id"; Role string "role"; jwt.RegisteredClaims }` with `Subject` = user ID string; `exp = iat + 15m`.
- `Issue(userID, fleetID, role) (string, error)`; `Verify(token) (Claims, error)` with GF-20 options.
- Tests (unit): claims round trip; `exp` exactly at +15 min; token rejected 1 s after `exp` using the injected clock; `alg=none` rejected; HS384/HS512 signed token rejected; tampered `fleet_id` rejected; wrong secret rejected.

### 5. Login throttle (`internal/auth/throttle.go`)

- `type Throttle struct{ mu sync.Mutex; now func() time.Time; limit int; window time.Duration; entries map[key]entry }` with `Allow(email, ip) (retryAfter time.Duration, ok bool)` and `RecordFailure(email, ip)`. Defaults `limit=5, window=15m` (GF-25).
- Tests (unit, fake clock): five failures, then `Allow` false on the sixth check; keys independent per IP and per email (case-insensitive); window expiry re-allows; sweep removes expired entries; `retryAfter` equals the remaining window.

### 6. Users store and audit writer (`internal/users/store.go`, `internal/audit/audit.go`)

- `users.Store` on `*sql.DB`: `Create`, `List(fleetID)`, `Update(fleetID, id, patch)`, `GetByEmail` (login), `SetDisabled`, `SetPassword`. Every query carries `fleet_id` (README §8 row scoping). `PATCH` and `POST` run inside a transaction that also inserts the audit row.
- `audit.Write(ctx, tx, Record{FleetID, ActorID, Action, Target})` is the only path to `admin_audit`.
- Tests (integration, PostGIS): create → list scoped to fleet; duplicate email (case-insensitive) → conflict; check constraint rejects unknown role; `driver_id` stays NULL.

### 7. Refresh-token store (`internal/auth/refresh.go`)

- `Issue(tx, userID, fleetID, familyID?) (raw string, err)` → 32 random bytes, base64url raw; stores SHA-256.
- `Rotate(ctx, raw, now) (userID, fleetID, newRaw, err)` implements GF-26 exactly; returns sentinel errors `ErrInvalid`, `ErrReuseDetected`.
- `RevokeFamily(ctx, raw, now)` for logout.
- `RevokeAllForUser(tx, userID, now)` used by disable, password reset, and reuse.
- Tests (integration): rotation issues a new token and marks the old one; old token → `ErrInvalid`; reuse of a rotated token revokes *all* of the user's tokens, including another session's (different family); expired token (set `expires_at` into the past via SQL) → `ErrInvalid`; disabled user → `ErrInvalid` and presented family revoked; logout revokes family, then refresh → `ErrInvalid`; hash stored, raw token never in DB (SELECT check).

### 8. Auth HTTP handlers and JWT middleware (`internal/auth/http.go`, `internal/auth/middleware.go`)

- `POST /auth/login`: body `{email,password}` → 400 on malformed; throttle `Allow` → 429; lookup; dummy-verify on miss; failure → `RecordFailure` → 401 `invalid_credentials`; success → 200 `{access_token, token_type:"Bearer", expires_in:900}` and refresh cookie.
- `POST /auth/refresh`: reads cookie; `Rotate`; 200 with new access token and rotated cookie; any `ErrInvalid` / `ErrReuseDetected` → 401 `unauthorized` and cookie cleared.
- `POST /auth/logout`: no cookie or unknown cookie → 204 (idempotent); otherwise `RevokeFamily`, clear cookie (`Max-Age=-1`), 204.
- Middleware `RequireAuth(issuer)` sets `Principal{UserID, FleetID, Role}` in the Echo context; `RequireRole(roles...)`. Authorization never trusts the claim alone for `fleet_id` scoping: the store queries always add `fleet_id = $principal`.
- Tests (integration via real HTTP): login → protected call → refresh → old refresh rejected (roadmap validation 1); disabled user cannot login (401) and cannot refresh; login 6th attempt → 429 with `Retry-After`; after advancing the injected clock 15 min, login is allowed again.

### 9. Admin users endpoints (`internal/users/http.go`)

- `GET /users` → 200 list (own fleet, no `password_hash`); `POST /users` → 201; `PATCH /users/:id` → 200 / 404 (unknown or other fleet) / 409 `self_lockout` / 400 `weak_password` on bad reset.
- Each successful write inserts an `admin_audit` row in the same transaction: `action ∈ {user.create, user.update, user.password_reset}`, `target = "user:<id>"`, `actor_id` = admin.
- Tests (integration): admin CRUD happy path; audit row present with correct actor/target/fleet per write; failed writes (403, 404, 409, 400) write no audit row; disable revokes refresh tokens; password reset revokes refresh tokens; self-demote / self-disable → 409.

### 10. Role-matrix extension (`services/api/rolematrix_test.go`) — R-6

- Table test: rows = `GET /users`, `POST /users`, `PATCH /users/:id`; columns = `admin, manager, dispatcher, driver, unauthenticated`. Expected: admin → 200/201 (PATCH on an existing own-fleet user); manager/dispatcher/driver → 403; no token → 401. Login/refresh/logout listed as "no JWT" rows with expected success/failure from §8.
- Extends the README §8 matrix for the P2 endpoints only. Future phases extend the same table (R-6).

### 11. Fleet-scoping test (`services/api/scope_test.go`)

- Fixture: fleet A with admin A, fleet B with user B (inserted via SQL or the store). Admin A: `GET /users` excludes B; `PATCH /users/<B id>` → 404; `PATCH` with a forged token whose `fleet_id` is B → 401 (signature). Roadmap testing item "token for fleet A gets 404 on fleet B resources".

### 12. `admin_audit` trigger tests (`internal/audit/audit_test.go`) — R-7

- Insert succeeds; `UPDATE` rejected; `DELETE` rejected; `TRUNCATE` rejected; each asserted with the check-violation SQLSTATE `23514` and the trigger's message.

### 13. Seed tool (`tools/seed/`)

- `seed.Run(ctx, db, Options{AdminEmail, FleetName, Timezone}, passwordSource) (Result, error)`; `main.go` wires flags, `term.IsTerminal(stdin)`, and exit codes (GF-23).
- Makefile: `seed` target becomes `seed: ## bootstrap first fleet + admin (D-05); SEED_ARGS="--admin-email you@example.com"` and runs `$(GO) run ./tools/seed $(SEED_ARGS)`. The `seed-demo` stub stays (P23).
- Tests (unit): flag / email / timezone validation; password policy runs before any DB access. Tests (integration): first run creates exactly one fleet (with timezone) and one admin; second run exits 0 with no change in row counts; two concurrent runs create one fleet. No default credentials exist: the only password input paths are the TTY prompt and stdin, and a run with no stdin password and no TTY exits 2.

### 14. API service (`services/api/`)

- `server.go`: `NewServer(Deps) *echo.Echo` with routes from §8–§9; `Deps{DB *sql.DB, Issuer *auth.Issuer, Clock func() time.Time, Throttle *auth.Throttle}`.
- `main.go`: `-addr` flag (default `127.0.0.1:10010`); `config.Load()` (refuses a short `JWT_SECRET` before opening the DB); opens the DB with `database/sql` + lib/pq; `Ping`; does **not** run migrations (D-13).
- Tests: a harness in `services/api/harness_test.go` (`startServer(t) baseURL`) listens on `127.0.0.1:0` and serves the real `NewServer`. Used by §8–§11 tests.

### 15. Docs and feature spec

- `specs/features/02-auth-core.md` (this file) is written by the persist step before code. Its sections: Scope · Plan · Requirements · Validation · Testing depth · SPEC-AMEND repeat · Out of scope.
- `CLAUDE.md` (**implement scope**; keep < 200 lines), only facts Claude would get wrong otherwise:
  - `make seed` is real now: `SEED_ARGS="--admin-email <email>"`, password via stdin or TTY prompt, exit 0 when already seeded.
  - Local API run: `DATABASE_URL=… JWT_SECRET=… go run ./services/api` → `127.0.0.1:10010`.
  - `JWT_SECRET` must be ≥ 32 bytes or the API refuses to start.
  - `make help` line for `seed` updated.
- `deploy/PORTS.md`, `README.md`, compose, `.github/workflows/ci.yml`: **unchanged** (CI already runs `make test` and the migrate round trip; the round trip covers 002–004 through `db/roundtrip_test.go`).

## Requirements

Binding and carried verbatim from `tech-stack.md` §11: D-01 … D-06, GF-2, GF-5, GF-7, GF-14, GF-18. New decisions GF-19 … GF-29 are recorded here only (no new mission §7 or tech-stack §11 rows are required). Zero open decisions.

### Reused — binding (tech-stack §11, verbatim)

- **D-01** — Users table `users(id, fleet_id, email CITEXT UNIQUE, password_hash, role, driver_id NULL, disabled_at)`; login by email only. *Rationale (tech-stack): §5 has no users table; §2.1.2 needs one. Global email uniqueness lets login skip fleet selection. SPEC-AMEND (additive table).*
- **D-02** — Refresh tokens: opaque 32-byte random, stored as SHA-256 in `refresh_tokens`; rotated on every use; reuse of a rotated token revokes all of that user's tokens; 7-day TTL; httpOnly, Secure, SameSite=Strict cookie on path `/auth`. *Rationale (tech-stack): §8 requires refresh tokens; rotation + reuse detection limits theft; Postgres storage keeps revocation durable. SPEC-AMEND (additive table and endpoints).*
- **D-03** — Access JWT: HS256 signed with `JWT_SECRET` (startup refuses < 32 bytes); claims `sub, fleet_id, role, exp` (15 min). WS closes at `exp`. *Rationale (tech-stack): §8 JWT 15 min; fixed algorithm avoids alg confusion.*
- **D-04** — Password hashing argon2id (64 MiB, t=3, p=2); minimum 12 characters; in-memory login throttle 5 failures per 15 min per (email, IP). *Rationale (tech-stack): Modern KDF; spec silent on policy; single-instance throttle is enough for MVP and is noted as such.*
- **D-05** — First fleet: `make seed` runs `tools/seed`, which creates one fleet and one admin only when `fleets` is empty; password read from a no-echo TTY prompt or stdin; no default credentials; `--timezone` (default UTC). *Rationale (tech-stack): §12 requires `make seed`; no committed credentials.*
- **D-06** — `GET/POST/PATCH /users` (admin only) added to §6. **SPEC-AMEND.** *Rationale (tech-stack): §1 admin needs user management; §6 omits it.*
- **GF-2** — `admin_audit(id, fleet_id, actor_id, action, target, at)` append-only table; every admin write records the actor (P2, P3). **SPEC-AMEND.** *Rationale (tech-stack): README §8 "admin actions logged with actor" had no owning table or phase.*
- **GF-5** — Migrations: sequential zero-padded `NNN_snake_name.up.sql` / `.down.sql` in `db/migrations/`, starting `001`, no timestamps; single transaction-friendly scripts (no `BEGIN`/`COMMIT`, no `CREATE INDEX CONCURRENTLY`); forward-only once merged to `main`; `make migrate` exits non-zero when `schema_migrations.dirty = true`, recovery via `make migrate MIGRATE_ARGS="force <version>"` after manual schema inspection, never by SQL edits to `schema_migrations`. *Rationale (tech-stack): One feature branch per phase makes sequential numbers safe; golang-migrate's dirty flag is the only safe signal.*
- **GF-14** — `fleets` baseline is exactly README §5 (`id BIGSERIAL PK`, `name TEXT NOT NULL`); fleet timezone arrives as an additive migration in P2. *Rationale (tech-stack): Baseline stays within roadmap P1 scope.* → P2 adds `fleets.timezone` in migration `004` (see §1).

### Closed in P2

- **GF-7 — closed.** `seed` was a reserved stub in P1 that exited 1 naming P2. In P2 the stub is replaced by the real `make seed` (GF-23). `seed-demo` stays a stub until P23.
- **GF-18 — closed by GF-19.** The application driver is chosen in P2: `database/sql` + `github.com/lib/pq` (GF-19).

### New in P2 (GF-19 … GF-29)

- **GF-19 — application DB driver: `database/sql` + `github.com/lib/pq`.** No pgx. *Rationale: lib/pq is already pinned, migrate already uses it (GF-18), and P2 needs only basic types and BYTEA. Closes the GF-18 deferral.*
- **GF-20 — JWT library `github.com/golang-jwt/jwt/v5`.** Parse with `jwt.WithValidMethods([]string{"HS256"})`, `jwt.WithExpirationRequired()`, and an injected `jwt.WithTimeFunc`. `alg=none` and any non-HS256 signature are rejected. *Rationale: pins the algorithm at parse time (D-03) and makes expiry testable with the injected clock.*
- **GF-21 — HTTP framework `github.com/labstack/echo/v4`.** Use the latest stable v4 at implementation time; the exact version is recorded in `go.mod`. *Rationale: tech-stack §1 "Go + Echo".*
- **GF-22 — migrations from Go use the `github.com/golang-migrate/migrate/v4` library** (`database/postgres` driver over lib/pq, `iofs` source over `db.Migrations`). Used by tests and by the seed tool's self-check. Library version = whatever `go.mod` resolves; the transitive `v4.20.1` noted during exploration must be confirmed and kept consistent. The CLI pin `MIGRATE_VERSION := v4.18.3` in the Makefile is not changed. *Rationale: tests and the seed self-check run the same migrations as the CLI without shelling out; the CLI pin stays as is.*
- **GF-23 — seed tool.** `tools/seed` is a `package main` that delegates to an importable `seed.Run(ctx, db, opts, passwordSource)`. `make seed` runs `$(GO) run ./tools/seed $(SEED_ARGS)`; the Makefile stub is replaced (GF-7 closed). Flags: `--admin-email` (required, `net/mail` parse), `--fleet-name` (default `FleetOps`), `--timezone` (default `UTC`, validated with `time.LoadLocation`). Password: if stdin is a TTY, a no-echo prompt via `golang.org/x/term`, asked twice; otherwise one line from stdin. Never a flag or env var. Policy-checked before any DB write. Idempotency: check `count(fleets)` before prompting (exit 0 "already seeded; no changes" without prompting), then re-check under `LOCK TABLE fleets IN EXCLUSIVE MODE` inside the insert transaction so concurrent runs create at most one fleet. Bootstrap writes no `admin_audit` row (no actor exists yet; GF-2 covers admin *API* writes). Exit codes: 0 success or already seeded, 1 runtime error, 2 usage/validation error. *Rationale: a Go entrypoint keeps policy and idempotency in one tested place; the password never appears in argv or the environment.*
- **GF-24 — API entrypoint `services/api`** (README §11 layout). `main` takes `-addr` (default `127.0.0.1:10010`, the PORTS.md reserved host port for local `go run`). No new env name beyond `DATABASE_URL` and `JWT_SECRET` (both already in tech-stack §5). Container port 8080 is not used in P2. *Rationale: matches the reserved host port; no new config surface.*
- **GF-25 — login throttle.** In-memory, single-instance (accepted risk, D-04). Fixed window of 15 min that starts at the first failure of a key. Key = (lowercased trimmed email, `RemoteAddr` host). `X-Forwarded-For` is ignored (no proxy in MVP). The check happens *before* the password check: once a key has ≥ 5 failures in its window, the next attempt returns 429 `rate_limited` with `Retry-After`, even with a correct password. Failures include unknown email, wrong password, and disabled user. Success does not reset the counter. Clock is injected as `func() time.Time`. Expired entries are swept at most once per minute to bound memory. *Rationale: a fixed window is simple to test with a fake clock; ignoring `X-Forwarded-For` prevents trivial bypass when no proxy is trusted.*
- **GF-26 — refresh-token lifecycle.** Cookie name `fleetops_refresh`, `Path=/auth`, `HttpOnly`, `Secure` (always), `SameSite=Strict`, `Max-Age=604800`. Each token expires 7 days after its own issuance; each rotation issues a fresh 7-day token (sliding; no absolute family cap in P2, noted as backlog). Logout revokes the presented token's family (`family_id`) and clears the cookie. Rotation is one atomic `UPDATE … WHERE rotated_at IS NULL AND revoked_at IS NULL AND expires_at > now RETURNING …`. Zero rows: if the hash exists and is already rotated, that is reuse → revoke all of that user's tokens (D-02) → 401. A disabled user presenting a valid token → revoke the family, commit, 401. Revocation writes are committed even on refusal (the transaction is not rolled back). Known accepted behavior: a concurrent double-submit of the same refresh cookie is treated as reuse (logs the user out); not mitigated in P2. *Rationale: a single conditional UPDATE makes rotation race-safe; committing revocations on refusal keeps the revocation durable.*
- **GF-27 — user admin rules.** Email trimmed and lowercased; uniqueness enforced by `CITEXT UNIQUE` → 409 `conflict`. Password policy: min 12 runes (D-04), max 256 bytes (DoS bound), no composition rules. `POST /users` takes `email, password, role`; `fleet_id` always comes from the principal, never from the body. `driver_id` column exists (D-01) but is always NULL in P2 and has no FK (the `drivers` table arrives in P3). `PATCH /users/:id` takes any of `role`, `disabled`, `password`. Disable → set `disabled_at` and revoke all that user's refresh tokens. Password reset → revoke all that user's refresh tokens. Admin cannot demote or disable themselves → 409 `self_lockout`. No DELETE in P2. Role or disable changes take effect for already-issued access tokens at expiry (≤ 15 min, D-03); this is accepted and documented. *Rationale: the body never sets the fleet; self-lockout protection keeps at least the caller's own admin path; the 15-min JWT window is the accepted cost of stateless access tokens.*
- **GF-28 — HTTP error envelope and check order.** Body `{"error":"<code>"}`. Codes: `invalid_credentials` 401, `unauthorized` 401, `forbidden` 403, `not_found` 404, `conflict` 409, `self_lockout` 409, `validation_failed` 400, `weak_password` 400, `rate_limited` 429. Order on protected routes: authentication (401) → role (403) → body validation (400) → fleet scope (404). The role check runs before body parsing, so a driver posting a malformed body gets 403. *Rationale: no information about a body is revealed to callers without the role.*
- **GF-29 — test wiring.** Integration tests call `services/api` `NewServer(Deps{DB, JWTSecret, Clock, ...})`, listen on `127.0.0.1:0`, serve through `e.Server.Serve(ln)`, and talk over real HTTP with `net/http`. Refresh cookies are read from `Set-Cookie` and replayed manually because Go's cookie jar will not send `Secure` cookies over plain HTTP. Clock is injected only for throttle and JWT expiry tests. No mocks of Postgres or the HTTP transport. Each test package starts one PostGIS container through `testinfra` (once per process via `sync.Once`), creates a fresh database per test, and migrates it with GF-22. *Rationale: real HTTP and real Postgres are the only way the cookie, transaction, and trigger behavior is actually exercised.*

Ports and env: no change to `deploy/PORTS.md` (10010 already reserved for `api`), no compose edits, no new env name. `JWT_SECRET` (startup refuses < 32 bytes) and `DATABASE_URL` only.

## Validation

Runnable on this host. Host tools: `/root/.local/go/bin/go`, `/root/.local/bin/golangci-lint`, `/root/go/bin/migrate`, wired through `deploy/.local/tools.mk`. Docker is available for testcontainers. For manual checks, a local DB runs with `docker compose -f deploy/docker-compose.yml up -d postgres` (127.0.0.1:10002); with `POSTGRES_PASSWORD` unset, the compose default applies. Secrets are generated per shell and never committed.

1. **Gates.** `make fmt-check` → exit 0, no files listed. `make lint` → exit 0. `make test` (`-race -count=1`) → exit 0 for all packages. **Pass:** all three exit 0.
2. **Migrations.** `DATABASE_URL=… make migrate` → exit 0, version 4. `make migrate MIGRATE_ARGS="down 3"` then `make migrate` → exit 0 both; `schema_migrations.dirty = false`. `db/roundtrip_test.go` passes down-to-zero then up. **Pass:** all commands exit 0 and the round trip test passes.
3. **Login → protected → refresh → old refresh rejected (roadmap #1).** Run the API with `JWT_SECRET=$(openssl rand -hex 32)`; seed the admin via `printf 'correct horse battery\n' | SEED_ARGS="--admin-email admin@example.com" make seed`; `curl -i -c jar` login → 200 with `access_token` and `Set-Cookie: fleetops_refresh=…; Path=/auth; HttpOnly; Secure; SameSite=Strict`; `GET /users` with bearer → 200; POST `/auth/refresh` with the old cookie → 200 with a new cookie; replay the *old* cookie → 401. **Pass:** all four statuses as stated, and the replayed old cookie returns 401 `unauthorized`. (The integration version in `services/api` tests is the CI gate.)
4. **Bootstrap idempotent (roadmap #2).** Run `make seed` twice with the same `SEED_ARGS`. **Pass:** the second run exits 0, prints "already seeded; no changes" to stdout, and `SELECT count(*) FROM fleets` = 1 and `users` = 1 both before and after. Running with no `SEED_ARGS` on an empty DB → exit 2 (usage).
5. **Disabled user cannot refresh (roadmap #3).** As admin, `POST /users` creates a manager; that manager logs in and keeps the refresh cookie; admin `PATCH /users/:id {"disabled":true}` → 200; the manager's refresh cookie → 401; the manager's login → 401 `invalid_credentials`. **Pass:** both 401, and `SELECT count(*) FROM refresh_tokens WHERE user_id=… AND revoked_at IS NULL` = 0.
6. **Throttle (roadmap testing).** Unit and integration tests in §5 and §8 pass. Manual: six wrong-password logins for the same email → the sixth returns 429 with `Retry-After` > 0; a correct password still returns 429 within the window. **Pass:** as stated.
7. **Fleet scoping.** The §11 test passes. Manual: `PATCH /users/<fleet-B id>` with the fleet-A admin token → 404. **Pass:** both.
8. **Role matrix (R-6).** The §10 table test passes; `go test ./services/api -run RoleMatrix -v` lists every cell. **Pass:** every cell listed and matching its expected value.
9. **`admin_audit` append-only (R-7).** The §12 test passes. Manual `psql` `UPDATE admin_audit SET action='x'` → error containing "append-only"; `DELETE` likewise. **Pass:** both error with "append-only".
10. **Audit with actor (GF-2).** After one `POST /users`, `SELECT actor_id, target, action FROM admin_audit` → one row with the admin's id, `user:<new id>`, `user.create`. **Pass:** exactly that row.
11. **No secret leakage.** `grep -R "JWT_SECRET\|password" --include=*.go` shows no literal secret values; `git diff --stat` touches no `.env`. `go test` output and error messages never print the secret (the config test asserts this). **Pass:** no literal secret in code; config test green.
12. **Scope fence.** `git diff --name-only main…HEAD` contains no `ws`, `ingest`, `vehicles`, `drivers`, `devices`, or `positions` code path (grep check: zero matches in new Go files). **Pass:** zero matches.

## Testing depth

Roadmap P2 + tech-stack §9.

- **Unit (stdlib `testing`, no DB):** password policy and hash params; JWT claims, expiry, algorithm pinning; throttle (fake clock); config; seed flag validation.
- **Integration (testcontainers PostGIS, real migrations, real HTTP):** refresh rotation; reuse revokes all of a user's tokens; expiry; disabled-user refresh; logout; login → protected → refresh; throttle over HTTP with injected clock; users CRUD; audit writes; admin_audit trigger (UPDATE / DELETE / TRUNCATE); seed idempotency and concurrency; migration round trip.
- **Role matrix (R-6):** table test over 4 roles × 3 verbs (+ unauthenticated).
- **Fleet scoping:** cross-fleet 404 and forged-claim 401.
- **Race:** `go test -race` runs everywhere (`make test`). The CI race gate in tech-stack §9 is for ingest and hub (P4+), not P2.
- **Property tests:** none in P2 (the roadmap defines none; `pgregory.net/rapid` stays out per GF-17 until P3).
- **No mocks** of Postgres, HTTP, or the clock beyond injected `func() time.Time`.

## SPEC-AMEND repeat (R-4)

Each item is an additive change to the README/tech-stack surface, recorded in `mission.md` §7 under the rows named.

- **D-06** — `GET/POST/PATCH /users` (admin only) added to the API surface (mission §7: "`GET/POST/PATCH /users` (admin)", P2).
- **D-01 / D-02** — `users` and `refresh_tokens` tables added (mission §7: P2, additive tables).
- **D-02 / D-03** — `POST /auth/refresh` and `POST /auth/logout`, cookie-based refresh (mission §7: P2, additive endpoints).
- **GF-2** — `admin_audit` append-only table added; every admin write records the actor (mission §7: P2, P3).
- **GF-14** — `fleets.timezone` added via migration `004` (tech-stack GF-14: "fleet timezone arrives as an additive migration in P2").

No new mission §7 or tech-stack §11 rows are required by P2. GF-19 … GF-29 are implementation decisions recorded in this feature spec only.

## Out of scope

- Any P3+ behavior: fleet registry, vehicles / drivers / devices, device keys, pairing, ingest, WS hub, map, jobs, geofences, alerts, reports.
- Compose edits; README edits (R-8); `deploy/PORTS.md` edits (no port changes); MinIO; OSRM.
- Absolute refresh-token lifetime cap; multi-instance throttle (Redis); concurrent-refresh double-submit mitigation; `GET /users/:id`; DELETE users.
- Remote push, merge to `main`, PR creation (user-triggered later).
- Implementation of any of the above in this run: the persist step writes this spec only. Code, migrations, Makefile, and CLAUDE.md changes happen in the implement step, starting at Plan group 1.
