# P16 — Playwright happy path · feature spec plan

Status: approved plan, persisted 2026-10-27. Branch `phase-16-playwright-happy-path`.

Source plan: `/root/.claude/plans/p16-plan-melodic-church.md` (approved).

Target file: `specs/features/16-playwright-happy-path.md` (to be persisted verbatim on branch `phase-16-playwright-happy-path`). Decisions GF-247…GF-259. Highest existing decision is GF-246.

## Scope

P16 adds the one end-to-end test the MVP allows: `e2e/happy-path.spec.ts`. It drives the full job lifecycle across the two apps:

dispatcher login → create job → assign → driver accepts → en_route → picked_up → deliver with photo → job shows delivered with proof.

The spec runs against compose infrastructure (postgres, redis, nats, minio) plus host processes for the Go services (api :10010, ws-hub :10011, ingest, no listener) and the two Vite dev servers (web :10012, driver-pwa :10013). A CI job `e2e` brings compose up, migrates, seeds, starts the stack, runs the spec, tears down, and uploads the Playwright report on failure.

Out of the scope of this phase: any second E2E, any app source change, any backend change, any compose change, any README change.

Roadmap validation item "green in CI on this branch" is recorded as NOT RUN on this host (see Validation (d)).

## Plan

Build order. Each group names its tests. Commands are run from the repo root.

**G0 — Preflight.**
- Baseline gates green on the phase branch before any change:
  - Go: `make fmt-check`, `make lint`, `make test`. `make test` needs Docker for PostGIS.
  - `apps/web`: `npm --prefix apps/web run typecheck`, `npm --prefix apps/web test`, `npm --prefix apps/web run build`.
  - `apps/driver-pwa`: `npm --prefix apps/driver-pwa run typecheck`, `npm --prefix apps/driver-pwa test`, `npm --prefix apps/driver-pwa run build`.
- Pin the Playwright version: run `npm view @playwright/test version` at G0 and use that exact version. Web research gave conflicting numbers (1.64.1 from Wikipedia, 1.60.0 from a registry snapshot, 1.62.1 from a tracker), so the registry value at G0 decides.
- Check host capability: `docker compose version`, Go toolchain (`go1.27.3`), Node 22 LTS, Chromium available for Playwright. If Docker or Chromium is missing on this host, record it in the G0 result. Validation (a) then becomes NOT RUN with the reason; no workaround is planned.
- Confirm the API routes the spec calls exist and match: `POST /users`, `PATCH /users/:id`, `POST /drivers`, `POST /vehicles`, `POST /devices`, `POST /vehicles/:id/pairings`, `POST /ingest/position`, `GET /jobs`, `GET /jobs/:id/nearest`. Confirm the postgres user and database name from the compose `postgres` service env (the `DATABASE_URL` in the generated env file is derived from those).
- Tests: the existing suites above are the baseline. No new tests in G0.

**G1 — E2E runtime wiring.**
- Files:
  - `e2e/package.json` and `e2e/package-lock.json`: `@playwright/test` pinned exactly (GF-255). Own lockfile; no root workspace.
  - `e2e/playwright.config.ts` (GF-255).
  - `e2e/scripts/stack.sh` with `up` and `down` subcommands (GF-253, GF-254, GF-257).
  - `Makefile` targets (GF-257): `e2e` (runs `e2e-stack-up`, then `e2e-test`, then `e2e-stack-down` in a trap), `e2e-stack-up`, `e2e-stack-down`, `e2e-test`. Each has a `## description` so `make help` lists it.
  - Root `.gitignore`: add `e2e/playwright-report/` and `e2e/test-results/`. `deploy/.local/` is already ignored (D-12); `node_modules/` is already ignored.
- `stack.sh up`, in order:
  1. Generate `deploy/.local/e2e.env` with `JWT_SECRET` and `DEVICE_KEY_SALT` (each `openssl rand -hex 32`), `E2E_ADMIN_EMAIL`, and `E2E_ADMIN_PASSWORD` (`openssl rand -hex 16`). Overwritten each run.
  2. `docker compose -f deploy/docker-compose.yml down -v` then `up -d --wait postgres redis nats minio` (GF-252, GF-253).
  3. `make migrate` with `DATABASE_URL` set (D-13: migrations are not run at boot).
  4. Seed: `printf '%s\n' "$E2E_ADMIN_PASSWORD" | SEED_ARGS="--admin-email $E2E_ADMIN_EMAIL" make seed` (GF-250).
  5. Build the binaries `services/api`, `services/ws-hub`, `services/ingest` into `deploy/.local/e2e/bin/`, then start each with the env from CLAUDE.md's env table (`DATABASE_URL`, `JWT_SECRET`, `DEVICE_KEY_SALT`, `NATS_URL=nats://127.0.0.1:10004`, `REDIS_URL=redis://127.0.0.1:10003`, `MINIO_ENDPOINT=127.0.0.1:10006`, `MINIO_KEY`, `MINIO_SECRET`). The api gets `-addr 127.0.0.1:10010`, the ws-hub `-addr 127.0.0.1:10011`. PIDs go to `deploy/.local/e2e/pids`, logs to `deploy/.local/e2e/logs/`.
  6. Start the Vite dev servers in the background: `npm --prefix apps/web run dev` (10012, `strictPort`) and `npm --prefix apps/driver-pwa run dev` (10013, `strictPort`).
  7. Readiness: TCP-connect loops on 10010, 10011, 10012 and 10013, 60 s each. The ingest process has no listener, so readiness for it is "process still alive after 5 s".
  8. On any failure, `stack.sh down` runs and the script exits non-zero.
- `stack.sh down`: kill the PIDs, verify ports 10010–10013 are free, `docker compose ... down` (keep volumes; the next `up` resets them with `-v`).
- `e2e-test`: source `deploy/.local/e2e.env`, `cd e2e && npx playwright test`.
- Tests:
  - `make e2e-stack-up` exits 0 and all four ports answer.
  - `make e2e-stack-down` leaves no listener on 10010–10013 and no process in the PID file.
  - `cd e2e && npx playwright test --list` shows exactly one test (the fence check, GF-257).

**G2 — The happy-path spec.**
- `e2e/happy-path.spec.ts`: one file, one `test()` (GF-248). Helpers stay inside this file.
- Setup, via the API as admin, all identities unique per run (`Date.now()` suffix) (GF-250):
  1. `POST /auth/login` as `E2E_ADMIN_EMAIL` to get an admin JWT.
  2. `POST /users` dispatcher, `POST /users` driver, `POST /drivers` (returns the driver id), `PATCH /users/:id {"driver_id"}` to link the driver user.
  3. `POST /vehicles` (plate `E2E-<run>`, active).
  4. `POST /devices {"kind":"tracker"}` and `POST /devices {"kind":"pwa"}`. The pwa key comes back once and is kept in memory.
  5. `POST /vehicles/:id/pairings {device_id, driver_id}` for the tracker device.
  6. `POST /ingest/position` with `X-Device-Key` = tracker key, one fix near the pickup point. Ingest is asynchronous, so the spec does not wait here. The wait happens in UI step 4 below: the assignment panel candidate list is polled for up to 60 s, which covers ingest latency inside the test.
- Sequence (dispatcher context baseURL `http://127.0.0.1:10012`, driver context baseURL `http://127.0.0.1:10013`, driver context `isMobile` with geolocation permission):
  1. Dispatcher: `/login`, fill label `Email` and `Password`, click button `Sign in`, expect URL `/map`.
  2. Dispatcher: `/jobs/new`, click the canvas `.new-job-map .map-canvas` once (pickup) and once (dropoff), expect text `Pickup:` and `Dropoff:` to show coordinates, click button `Create job`, expect URL `/jobs`.
  3. Dispatcher: find the job id as the highest id in `GET /jobs?state=created` (admin API) and click `Select job <id>`.
  4. Dispatcher: inside `section[aria-label="Assignment"]`, wait for the radio `Select E2E-<run>` (poll up to 60 s, reloading the candidate list), check it, click `Assign`, expect the row state `assigned`.
  5. Driver: `/setup-key`, fill `Device key` with the pwa key, click `Save device key`, expect URL `/today`. (Decision: type the key into the real KeySetup input rather than writing localStorage; this exercises D-30.)
  6. Driver: `/login` (label `Email` and `Password`, button `Sign in`), expect URL `/today`, expect item `Job <id>` with label `Assigned`.
  7. Driver: click `Accept`, expect `Accepted`. Click `Start`, expect `En route`. Click `Pick up`, expect `Picked up`.
  8. Driver: click the link `Deliver` (role `link`), expect URL `/jobs/<id>/deliver`. Set the file input `#proof-photo` to the photo bytes (step 9 below), click `Confirm delivery`, expect URL `/today`.
  9. Photo bytes: a 1×1 PNG base64 literal in the spec, written to a temp file (GF-248). Nothing binary is committed. The server sniffs the type, so this is a valid image.
  10. Assertions after delivery:
      - Dispatcher `/jobs`: the row for the job shows state `delivered`.
      - Admin API `GET /jobs`: the job has `delivered_at` set and a non-null `proof_photo_url` (GF-259).
- Tests: the spec is the test. It runs twice in a row with a reset between runs (G5).

**G3 — CI job.**
- `.github/workflows/ci.yml`: add a new job `e2e` beside `gates`. Triggers unchanged (push all branches, PR to main). No workflow-level concurrency change (GF-256).
- Steps:
  1. `actions/checkout@v4`.
  2. `actions/setup-go@v5` with `go-version-file: go.mod`, `cache: true`. Then `make tools`.
  3. `actions/setup-node@v4` with Node 22 and npm caches for `apps/web/package-lock.json`, `apps/driver-pwa/package-lock.json`, `e2e/package-lock.json`.
  4. `npm --prefix apps/web ci --include=dev`, `npm --prefix apps/driver-pwa ci --include=dev`, `npm --prefix e2e ci`.
  5. `(cd e2e && npx playwright install --with-deps chromium)`.
  6. `make e2e` (stack up, test, stack down via trap).
  7. On failure: `actions/upload-artifact@v4` with `e2e/playwright-report/` and `e2e/test-results/`, retention 7 days.
- `timeout-minutes: 30`. `needs` not set, so it runs in parallel with `gates`.
- Tests: YAML parses (`python3 -I` with PyYAML if available, or `actionlint` if installed); the step list mirrors the `make e2e` steps locally.

**G4 — CLAUDE.md rows and constitution ledger.**
- `CLAUDE.md`: add an e2e section with exactly: how to run locally (`make e2e`), what CI runs (the `e2e` job, same make targets), where the report lands (`e2e/playwright-report/`). Nothing else (GF-257). Keep `CLAUDE.md` under 200 lines.
- Constitution ledger (GF-247): add one row to `specs/constitution/mission.md` §7 and one entry to `specs/constitution/tech-stack.md` §11. Nothing else under `specs/constitution/`.
- Tests: `wc -l CLAUDE.md` < 200; the ledger row ID matches the tech-stack §11 entry.

**G5 — Full validation run.**
- Run the stack from clean: `make e2e` twice, with the stack reset each time (validation (a)).
- Run the existing gates again (validation (b)).
- Run the scope fence and secret checks (validation (e), (g)).
- Record results honestly in the feature spec's status line.

## Requirements

### Carried from existing decisions (cite, do not restate)

- **D-37** testing tools: one Playwright spec in MVP. No second E2E.
- **D-39** repo layout: `e2e/` holds the Playwright spec.
- **D-12** `deploy/.local/` holds generated local artifacts and is gitignored.
- **D-30** driver device key: the PWA identity is a `devices` row with `kind=pwa`. An admin issues the key once. KeySetup consumes it.
- **D-13** migrations are applied by `make migrate`, never at app boot.
- **R-1, R-2** (roadmap §1): one feature loop per phase, one branch per phase. The one-E2E fence is a roadmap-level rule.
- **mission §2** dispatcher and driver rows; **mission §4** demo stays manual.
- **README §9** testing row (create→assign→deliver); the roadmap P16 scope (L113) is the binding flow.
- Driver-flow contracts: GF-175 (assign needs driver_id), GF-184 (driver identity via `users.driver_id`), GF-185 (today semantics), GF-188 (accept/start/pickup empty body), GF-200 (deliver contract: multipart `photo`, `lat`, `lng`), GF-196 (`proof_photo_url` on every job object), GF-209/211/213/214 (nearest), GF-216 (assign reuses `POST /jobs/:id/assign`), GF-218 (`/jobs` and `/jobs/new` routes).
- Open GF-192 hub identity binding stays open and belongs to P23. The hub is started but the happy path does not depend on it.

### New decisions (GF-247 onward)

**GF-247 — SPEC-AMEND for the CI e2e job.** P16 adds one SPEC-AMEND row, ID GF-247, type addition, phase P16. The `e2e` CI job runs the Go services and the Vite dev servers as host processes against compose infrastructure. The constitution already says the Playwright job "brings compose up" (tech-stack §9 CI row). It does not describe a host-process service stack, the browser install, or a compose subset. Row goes in mission §7 and a matching entry in tech-stack §11. README reference: §9 (testing). *Why:* the constitution is the source of truth, and the process in mission §7 requires a row for any deviation.

**GF-248 — One spec file; contexts and selectors.** `e2e/happy-path.spec.ts` contains one `test()`. Dispatcher and driver run in two browser contexts inside that test: dispatcher desktop, driver `isMobile` with geolocation. Selectors use role and label (`getByLabel`, `getByRole`) against the existing markup in `apps/web` and `apps/driver-pwa`. The proof photo is a 1×1 PNG literal written to a temp file. No binary is committed. *Why:* one file keeps the fence visible; role and label selectors don't depend on CSS; no fixture binary means no lockfile-style churn.

**GF-249 — Happy-path step sequence.** Steps are exactly those in G2, items 1–10, in that order. Each step maps to an existing UI action or API call. The spec adds no test-only input or endpoint. *Why:* the roadmap scope is the lifecycle; a fixed sequence makes failures easy to locate.

**GF-250 — Provisioning.** `make seed` with `--admin-email` only, password from stdin. The spec creates dispatcher, driver, vehicle, tracker and pwa devices, pairing, position, and driver link through the admin API, with per-run unique identities. `make seed-demo` and `--demo-devices` are not used (demo creates 50 vehicles and nothing the spec needs). `deploy/.local/devices.json` is not used. *Why:* the seed creates only an admin (`internal/seed/seed.go`). The API path exercises the real D-30 issuance and needs no file.

**GF-251 — Ports.** Web is host 10012 and driver-pwa is host 10013 (CLAUDE.md table, `apps/web/vite.config.ts`, `apps/driver-pwa/vite.config.ts`, `deploy/PORTS.md`). The roadmap and task brief say 5173/5174; those are Vite defaults and container-port labels, not host ports. No `PORTS.md` change. *Why:* the reserved host ports are the ones that exist; the brief's numbers would collide with nothing but would not match the configs.

**GF-252 — State reset.** `stack.sh up` runs `docker compose down -v` before `up`, so every run starts with empty volumes. Per-run unique identities make a rerun safe even without a reset. *Why:* a clean database per run removes seed drift; unique identities protect a rerun during local debugging.

**GF-253 — Compose subset.** `docker compose up -d --wait postgres redis nats minio`. `osrm` is not started. It needs a map extract that CI does not have and the happy path does not route. `--wait` would fail on the osrm restart loop, so the subset is required. No compose edit (no `docker-compose.yml` change). *Why:* the happy path needs postgres, redis, nats and minio only.

**GF-254 — Service startup.** Go services are built to `deploy/.local/e2e/bin/` and started as background processes. PIDs in `deploy/.local/e2e/pids`, logs in `deploy/.local/e2e/logs/`. Env is from `deploy/.local/e2e.env`, generated per run (`JWT_SECRET`, `DEVICE_KEY_SALT` at 32 bytes or more; admin password). Readiness: TCP connect on 10010, 10011, 10012, 10013; ingest by process-alive check. Migrate runs before any service (D-13). *Why:* the Go services have no container images in this repo; host processes match CLAUDE.md's local-run contract.

**GF-255 — Playwright toolchain.** `e2e/` is its own npm package with its own lockfile, not a root workspace. `@playwright/test` pinned exactly to the G0 `npm view` value. Chromium only. `playwright.config.ts`: no `webServer` (servers are started by `stack.sh`); `baseURL` set per browser context; `workers: 1`; `retries: 0`; trace, screenshot and video `retain-on-failure`; `outputDir: test-results`; HTML report to `playwright-report`. *Why:* one orchestration path serves local and CI; Playwright-managed servers can't wait on compose and migrate; retries would hide flakes the validation must expose.

**GF-256 — CI job.** Job name `e2e`. Steps as in G3. `timeout-minutes: 30`. Browsers installed each run with `--with-deps chromium`; no browser cache (simpler, and the job budget is generous). Artifact upload only on failure, 7-day retention. *Why:* matches the existing gates job pattern (setup-go, make tools) and keeps the failure evidence without bloating every run.

**GF-257 — Make targets and CLAUDE.md.** `make e2e` is the single entry point locally and in CI (CI calls `make e2e`). Targets: `e2e`, `e2e-stack-up`, `e2e-stack-down`, `e2e-test`. CLAUDE.md gets exactly three rows: local run, CI job, report location. *Why:* CLAUDE.md says CI runs the same make targets as local gates.

**GF-258 — Map-click pickup and dropoff.** The spec clicks the MapLibre canvas for pickup and dropoff; there is no text input. If WebGL is unavailable in the headless runner, the spec fails and the phase stops with a report; no test-only input is added. *Why:* the roadmap scope forbids app source changes for tests; a failing test is the honest signal.

**GF-259 — Proof assertion.** Delivered is asserted two ways: the dispatcher `/jobs` row shows `delivered`, and the admin API job has `delivered_at` and a non-null `proof_photo_url`. The driver Today list does not show terminal jobs (GF-185), so it is not asserted. No UI change. *Why:* the proof is not rendered in the web UI; the API is the only place it is visible.

## Validation

Runnable on this host (pass conditions):

- **(a)** `make e2e` exits 0 against compose + local services, run at least twice in a row, with `stack.sh up` resetting compose state between runs. Pass: two consecutive exit 0 with the same spec. *If Docker or Chromium is missing on this host, record (a) as NOT RUN with the reason.*
- **(b)** Existing gates unchanged and green: `make fmt-check`, `make lint`, `make test`; `npm --prefix apps/web run typecheck|test|build`; `npm --prefix apps/driver-pwa run typecheck|test|build`. Expected: zero Go diff and zero frontend-source diff outside `e2e/`.
- **(c)** `.github/workflows/ci.yml` parses as YAML. The step list mirrors `make e2e`.
- **(d)** Roadmap item "green in CI on this branch": recorded as NOT RUN on this host (no GitHub Actions here). This is an honest follow-up; CI is the only proof for it.
- **(e)** Scope fence: `git diff --name-only main...HEAD` touches only: `e2e/` (spec, config, package files, lockfile, scripts), `.github/workflows/ci.yml`, `Makefile`, `.gitignore` (two lines), `CLAUDE.md`, `specs/features/16-playwright-happy-path.md`, `specs/constitution/mission.md` (one ledger row, GF-247), `specs/constitution/tech-stack.md` (one §11 entry, GF-247), `prompts/`. The intersection with `README.md`, `deploy/PORTS.md`, `deploy/docker-compose.yml`, `db/`, `internal/`, `services/`, `tools/`, `apps/web/`, `apps/driver-pwa/` is empty. Any other `specs/constitution/` edit is out of scope.
- **(f)** `wc -l CLAUDE.md` < 200.
- **(g)** No secrets in the diff: grep the diff for `password`, `api_key`, `fo_dev_`, and any literal JWT or salt. Expected: no hits except the variable names.
- **(h)** `cd e2e && npx playwright test --list` lists exactly one test.

## Testing depth

| Layer | Tool | Cases | Binding |
|---|---|---|---|
| Playwright E2E | Playwright (pinned, Chromium) | One spec `e2e/happy-path.spec.ts`: dispatcher login → create → assign → driver accept/en_route/picked_up → deliver with photo → delivered with proof | **Binding** (roadmap §3 P16, D-37). No second E2E in MVP. |
| Go unit and integration | `go test -race` (existing) | Unchanged | Existing gate, no new tests |
| Frontend unit and typecheck | Vitest + `tsc` (existing) | Unchanged | Existing gate, no new tests |
| CI gates | `gates` job (existing) | Unchanged | Existing gate |
| CI e2e | `e2e` job (new) | Runs the one spec | Binding (roadmap validation: "green in CI") |

## SPEC-AMEND repeat

P16 adds one SPEC-AMEND row, GF-247 (see Requirements). It is a CI-pipeline addition: the `e2e` job runs Go services and Vite servers as host processes and brings up a compose subset. The row is recorded in mission §7 and in tech-stack §11, per the mission §7 process. This is the only constitution edit in P16.

Open item repeated: GF-192 (hub identity binding, `users.id` vs `drivers.id`) remains open and belongs to P23. The happy path does not depend on the hub's identity behavior.

## Out of scope

- Geofences (P17+), alerts, reports (P21), push notifications (GF-1).
- Any second E2E spec, ever (roadmap "exactly one Playwright spec in MVP").
- Demo-script automation (demo stays manual, roadmap P16 note).
- k6 (P23).
- README edits (R-8: `README.md` is never edited).
- `deploy/docker-compose.yml` and `deploy/PORTS.md` edits. No delta is planned.
- `apps/web/` and `apps/driver-pwa/` source changes. If one turns out unavoidable, stop and report it instead of planning it silently.
- Backend Go or `db/` changes.
- Merge to main, push, and any P17+ content.
