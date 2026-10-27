# P14 plan: driver PWA, online

Status: approved plan, persisted 2026-10-27. Branch `phase-14-driver-pwa-online`.

Source plan: `/root/.claude/plans/p14-plan-immutable-gray.md` (approved).

## Scope

P14 (roadmap §3) ships `apps/driver-pwa`: an installable React PWA for drivers that works against the existing API while online. It covers the app shell and manifest, first-run device-key setup (D-30), today's jobs, one-tap accept/start/pickup, photo-backed delivery, and position posting with the device key while a shift is active.

Facts checked in this plan that correct the roadmap or the proposal:

- The roadmap says the dev port is 5174 and the validation says "local API at 5174". PORTS.md and tech-stack §4 reserve `driver-pwa` at **10013**. P14 uses 10013 (GF-225).
- The driver principal needs no backend change. `resolveDriver` joins `users.driver_id` to `drivers` in the principal's fleet (`internal/jobs/driver.go:16-29`). The JWT carries only `sub`, `fleet_id` and `role`. GF-227.
- `POST /ingest/position` takes `X-Device-Key` only. `ts` is **required** (RFC3339, from 24 h in the past to 5 min in the future, GF-56). The body cap is 4096 bytes and an oversize body returns 400, not 413. Every key failure returns 401 `unauthorized` (GF-53). Full body contract in GF-232.
- The device key binds to a driver through `vehicle_pairings.driver_id` (GF-33), not a `devices` column. A phone must be paired to a vehicle before its positions are accepted. P14 does not change this; the manual validation includes the pairing step.
- `apps/web` has no `@testing-library/user-event`. P14 uses `fireEvent`, like `apps/web` (GF-233).
- The proposal's dev proxy list was too broad. The driver app calls only `/auth`, `/driver` and `/ingest`. GF-225 narrows it.

In scope (roadmap P14 only):

1. `apps/driver-pwa` scaffold: Vite React build on its own dev/preview origin, with `vite-plugin-pwa` manifest and app-shell service worker (D-38).
2. Key setup from the admin-issued one-time key (D-30): validation, storage, and the rejected-key path.
3. Today's jobs: `GET /driver/jobs/today`.
4. One-tap transitions when online: accept, start and pickup via the P11 endpoints; deliver via the P12 multipart endpoint.
5. Photo capture for delivery.
6. Position posting with the device key while a shift is active.
7. Validation: manual install and one-tap flow against the local API (manual follow-up, not a CI gate).
8. Testing depth: Vitest unit for transition mapping and key validation (binding), plus component tests.

Out of scope: see the final section.

## Plan

Build order. Every group ends with its tests passing. No group touches `internal/`, `services/`, `db/`, `tools/`, `deploy/` or `apps/web/`.

**G0 — Preflight (no code).**
- On `phase-14-driver-pwa-online`: run the baseline gates and record results in the Execution record: `make fmt-check`, `make lint`, `make test`, then `npm --prefix apps/web ci --include=dev`, `npm --prefix apps/web run typecheck`, `npm --prefix apps/web test`, `npm --prefix apps/web run build`. If Docker is unavailable, `make test` cannot prove PostGIS tests, so record it as a skipped check, not green.
- Confirm `apps/driver-pwa/` does not exist. Confirm the highest GF id is GF-223 (`grep -ho 'GF-[0-9]*' specs/features/*.md | sort -u | tail`).
- Re-confirm the contracts in the Requirements section by reading `internal/ingest/http.go` and `validate.go`, `internal/jobs/http.go` and `driver.go`, and `internal/devicekey/key.go`. Confirm the refresh-token route `apps/web/src/auth/` uses, so the PWA can reuse it.
- Confirm that `internal/jobs/transitions.go` driver edges equal D-23 (`accept`, `start`, `pickup`, `deliver`, `fail` from `accepted|en_route|picked_up`).
- Tests: none. This group is checks only.
- Pass: every item above is recorded with its result. Any mismatch with the Requirements stops the plan until it is resolved in this spec.

**G1 — Scaffold (`apps/driver-pwa`).**
- Files: `package.json` (scripts in GF-224), `package-lock.json`, `tsconfig.json` + `tsconfig.app.json` + `tsconfig.node.json` (mirror `apps/web`), `index.html`, `vite.config.ts` (GF-225, GF-226), `src/main.tsx`, `src/App.tsx` (shell only), `src/test/setup.ts`, `public/icons/icon-192.png` and `icon-512.png`, `scripts/check-pwa-build.mjs` (GF-234).
- Icons: generate once from a small SVG with whatever rasterizer the host has (ImageMagick or `rsvg-convert`). If none exists, write solid PNGs with a one-off Node `zlib` script kept outside the repo. Commit the PNGs, not the script.
- Tests: `src/App.test.tsx` renders the shell heading. The build check asserts the artifacts (GF-234).
- Pass: gates in Validation §2 are green, and `dist/` holds the manifest, `sw.js` and the registration file. G1 confirms the emitted file names; the GF-226 mode is fixed.

**G2 — API client and session.**
- Files: `src/api/client.ts` (mirrors `apps/web/src/api/client.ts`: `ApiError{status, code}`, `sendJson`, `errorCode` fallback `'http_error'`), `src/api/driver.ts` (`login`, `listTodayJobs`, `transitionJob`, `deliverJob`), `src/api/ingest.ts` (`postPosition(key, body)`), `src/api/types.ts` (`DriverJob`, `JobState`), `src/session/session.ts` (access token in memory only; role read from the JWT payload for UI gating only, as `apps/web/src/auth/session.ts` does).
- Tests: `src/api/client.test.ts` (stubbed `fetch`): error code mapping, `http_error` fallback, a 401 clears the session.
- Pass: unit tests green.

**G3 — Key setup (binding unit + screen).**
- Files: `src/device/key.ts` (pure `validateDeviceKey(raw)`, `loadDeviceKey()`, `saveDeviceKey(key)`, `clearDeviceKey()`), `src/pages/KeySetupPage.tsx`.
- Tests: `src/device/key.test.ts` (BINDING unit, matrix in Testing depth). `src/pages/KeySetupPage.test.tsx`: invalid key blocked with a message and no storage write or request; valid key stored trimmed.
- Pass: both green.

**G4 — Login and route gating.**
- Files: `src/pages/LoginPage.tsx` (email and password to `POST /auth/login`), route guard in `src/App.tsx`.
- Routes: `/login`, `/setup-key`, `/today` (default for `/`), `/jobs/:id/deliver`.
- Gating: no session → `/login`; session but no device key → `/setup-key`; role not `driver` → a blocked message, no job calls.
- Tests: `src/pages/LoginPage.test.tsx` (401 `invalid_credentials` message; non-driver role blocked). `src/App.test.tsx` extended with the three gating redirects.
- Pass: green.

**G5 — Transition mapping (binding unit) and Today's jobs.**
- Files: `src/jobs/actions.ts` (pure `actionsFor(state)`, GF-229), `src/jobs/queries.ts` (`useTodayJobs` with `refetchInterval: 30_000` and refetch on focus; `useJobTransition` invalidating the today query on success), `src/pages/TodayPage.tsx`.
- Tests: `src/jobs/actions.test.ts` (BINDING unit, table in Testing depth). `src/pages/TodayPage.test.tsx` (mocked `src/api/driver`): each state shows the correct button; tapping calls `transitionJob(id, 'accept'|'start'|'pickup')` with `{}`; the button is disabled while pending; `{"jobs":[]}` shows the empty message; a 409 shows a message and refetches.
- Pass: green.

**G6 — Delivery flow with photo.**
- Files: `src/jobs/deliver.ts` (pure `checkPhotoFile(file)` and `buildDeliverForm(photo, lat, lng)`), `src/pages/DeliverPage.tsx` (GF-231).
- Tests: `src/jobs/deliver.test.ts` (size and type checks, form field names). `src/pages/DeliverPage.test.tsx`: submit blocked until a photo is chosen; submit blocked with a message when geolocation fails; success returns to `/today`; 409, 413 and 503 map to messages.
- Pass: green.

**G7 — Position reporter (shift active).**
- Files: `src/position/payload.ts` (pure `fixToIngestBody(coords, timestampMs)`), `src/position/shift.ts` (on-shift flag in localStorage), `src/position/PositionReporter.tsx` (GF-232), with the shift toggle on the Today header and the reporter mounted in the authenticated layout.
- Tests: `src/position/payload.test.ts` (null speed and heading → 0; `ts` is ISO from the fix timestamp; field set). `src/position/PositionReporter.test.tsx` (fake timers and stubbed `navigator.geolocation` and `fetch`): posts on each tick while on shift with `X-Device-Key` and the body; posts nothing after toggling off; a 503 is dropped and the next tick posts; a 401 clears the key and calls `onKeyRejected`; a hidden page skips ticks.
- Pass: green.

**G8 — CLAUDE.md rows, full validation, execution record.**
- Edit `CLAUDE.md`: add a driver-pwa block (GF-235). Keep the env table unchanged.
- Run the full Validation list. Record results in the spec's Execution record.
- Manual follow-up (Validation §5) is noted, not run in CI.
- Pass: every automated check in Validation is green and the scope fence holds.

## Requirements

### Carried from existing decisions (cite, do not restate)

- **D-29** (device keys): `fo_dev_<device_id>_<secret>`, secret 32 random bytes base64url, stored as HMAC-SHA256 with `DEVICE_KEY_SALT`, shown once. P14 only consumes the key. It never issues, rotates or revokes one.
- **D-30** (PWA identity): "the phone is a `devices` row with `kind=pwa` bound to a driver; the admin issues the key on a pairing screen, shown once, and the driver enters it on first run. The PWA posts positions with that key." P14 implements the last two sentences. The binding is via `vehicle_pairings.driver_id` (GF-33).
- **D-38** (frontend libraries): `vite-plugin-pwa` (Workbox) for the service worker and manifest; Vite dev proxy with REST to 10010 and no CORS in dev.
- **D-23** (job edges): `assigned→accepted`, `accepted→en_route`, `en_route→picked_up`, `picked_up→delivered`, `{accepted,en_route,picked_up}→failed`. Drivers accept, start, pickup, deliver and fail their own jobs. Single source `internal/jobs/transitions.go`.
- **D-21**: P14 opens no WebSocket. Live `job.updated` is not part of the P14 scope. Polling covers freshness (GF-230).
- **R-6**: not triggered. P14 adds no endpoint.
- **GF-31** (P3): device-key format rule `^fo_dev_([1-9][0-9]*)_[A-Za-z0-9_-]{43}$`.
- **GF-33** (P3): device binding through `vehicle_pairings.driver_id`.
- **GF-53** (P4): every device-key failure returns 401 `unauthorized` with one body.
- **GF-56** (P4): `ts` window, 24 h past to 5 min future.
- **GF-60** (P4): `busy` 503 when the publish gate is full.
- **GF-173** (P10) and **GF-196** (P12): job object shape, including `proof_photo_url`.
- **GF-153** (P9): browser-visible origin convention, applied in GF-225.
- **GF-110** (P7): hub origin allowlist. P14 does not touch the hub.
- **GF-192** (P13): the hub identity binding remains open. P14 does not depend on it because polling is used.
- Mission §2 (driver row) and §4 (demo: driver accepts and completes a job with photo) define the driver needs. P14 adds no need beyond them.

### Verified contract facts (from research, G0 re-checks)

- `POST /ingest/position`: header `X-Device-Key`. Body fields `lat`, `lng`, `speed`, `heading`, `accuracy`, `ts`, all required; unknown fields rejected. Ranges: lat ±90, lng ±180, speed 0–300, heading 0–360, accuracy 0–10000. Responses: 204 (no body) success; 401 `unauthorized` for any key failure; 400 `validation_failed` for body or range failures; 503 `busy`.
- `GET /driver/jobs/today` → 200 `{"jobs":[...]}`. An unlinked driver gets `{"jobs":[]}`. Job fields: `id`, `state`, `pickup`, `dropoff` as `{lat,lng}`, `window_start`, `window_end`, `notes`, `size_class`, `weight_kg`, `failure_reason`, `delivered_at`, `proof_photo_url`.
- Accept, start, pickup: `POST` with an empty body or `{}`. Any other field is 400.
- Fail: `POST {"reason"}`, trimmed, 1–1000 runes (not exposed in P14, GF-229).
- Deliver: `POST` multipart `photo`, `lat`, `lng`. Photo ≤ 5242880 bytes; type sniffed as JPEG, PNG or WebP. 409 for any state other than `picked_up` (a replay of `delivered` is 200). 413 `payload_too_large`. 503 `proofs_unavailable`. 404 for wrong driver or id.
- `POST /auth/login`: `{"email","password"}` → 200 `{"access_token","token_type","expires_in"}`. 401 `invalid_credentials`. 429 `rate_limited`.

### New decisions (GF-224 onward)

**GF-224 — Workspace and toolchain.** New self-contained `apps/driver-pwa/` with its own `package.json` and lockfile, and no workspace tool and no shared package with `apps/web`. Pins mirror `apps/web` exactly: React 18.3.1, react-dom 18.3.1, Vite 8.3.5, `@vitejs/plugin-react` 6.1.2, TypeScript 5.9.3, Vitest 4.1.12, jsdom 30.1.3, `@testing-library/react` 16.3.3, `@testing-library/dom` 10.4.2, `@testing-library/jest-dom` 6.10.0, `@types/react` 18.3.31, `@types/react-dom` 18.3.7, `@tanstack/react-query` 5.105.0, `react-router` 7.18.4 (the package name is `react-router`), `vite-plugin-pwa` 2.0.0 (dev dependency; supports Vite 8 per its peer range). Scripts: `dev` = `vite`; `typecheck` = `tsc -b`; `test` = `vitest run`; `preview` = `vite preview`; `build` = `tsc -b && vite build && node scripts/check-pwa-build.mjs`. *Why:* the same toolchain keeps upgrades in one place; the app is small enough that copying a helper beats a workspace tool. Cross-app reuse is not part of P14; P15 decides it.

**GF-225 — Dev and preview origin, proxies.** Dev and preview both bind `127.0.0.1:10013` with `strictPort: true`. The origin is 10013, not the roadmap's 5174, because PORTS.md and tech-stack §4 reserve 10013 for this app and the roadmap's 5174 predates that reservation. Proxies (in both `server.proxy` and `preview.proxy`, since Vite does not inherit one from the other): `/auth`, `/driver`, `/ingest` → `http://127.0.0.1:10010`. No `/stream`, `/jobs`, `/vehicles` or `/map`: P14 opens no WebSocket and the driver app calls only driver routes. The GF-110 hub origin allowlist is unchanged in P14. If a later phase adds a WebSocket from this origin, it must add `127.0.0.1:10013` to the allowlist. *Why:* the browser-visible origin follows GF-153, and the narrower proxy set means the driver app cannot reach dispatcher routes even in dev.

**GF-226 — PWA plugin, manifest and caching.** `vite-plugin-pwa` 2.0.0 with `strategy: 'generateSW'` (Workbox). `registerType: 'autoUpdate'`. `injectRegister: 'script'`, so the registration is a static `registerSW.js` and no component imports `virtual:pwa-register`, which keeps jsdom tests free of virtual-module mocks. `devOptions.enabled: false`, so the service worker runs only in `build` and `preview`. Manifest: `name` "FleetOps Driver", `short_name` "FleetOps", `start_url` "/", `scope` "/", `display` "standalone", `orientation` "portrait", `theme_color` "#1f2937", `background_color` "#ffffff", icons `192x192` and `512x512` with purpose `any`. No maskable icon in P14; the install prompt only requires the two PNGs. Workbox: `globPatterns` `**/*.{js,css,html,svg,png,webmanifest}`; no `runtimeCaching` (app shell only, no API caching); `navigateFallback` `/index.html` with `navigateFallbackDenylist` `[/^\/(auth|driver|ingest)\//]` so the API paths never resolve to the shell. *Why:* `autoUpdate` avoids a prompt UI that P14 does not need. The accepted cost is that a new deployed build can reload a page mid-form. P15 owns the queue that removes that risk; P14 records it as a known limitation.

**GF-227 — Identity model.** The driver logs in with email and password through `POST /auth/login`. The access token stays in memory only, the same as `apps/web/src/auth/session.ts`. Refresh uses the same refresh route `apps/web` uses (confirmed in G0). Every job call uses the JWT. The device key is used only for `POST /ingest/position` and never as a job credential. A role other than `driver` gets a blocked screen and no job calls. *Why:* the principal already resolves through `users.driver_id` (`internal/jobs/driver.go:16-29`), so the backend needs **no change** in P14. An unlinked driver gets an empty today list and 404 on transitions; the app shows the empty message and a generic error. The GF-192 hub identity mismatch affects live `/stream` delivery only, and P14 uses polling, so it does not block the app.

**GF-228 — Key setup: validation, storage, rejection.**
- Validation is a pure function `validateDeviceKey(raw)`. It trims leading and trailing whitespace, then tests `^fo_dev_([1-9][0-9]*)_[A-Za-z0-9_-]{43}$` (GF-31). Internal whitespace, a leading zero in the id, a wrong secret length, a bad prefix and any forbidden character fail. The result is `{ok: true, key}` or `{ok: false, reason}` with reason `empty` or `format`. No network call.
- Storage: `localStorage` under `fleetops.driver.deviceKey.v1`, holding the trimmed key. The key belongs to the device, so logout does not clear it. *Why:* IndexedDB adds no safety for a string secret that must be sent in a header; P15 may revisit storage. The XSS exposure of `localStorage` is accepted for P14 and recorded in CLAUDE.md.
- No server verification endpoint exists. The first `POST /ingest/position` is the check. On a 401, the reporter clears the stored key, stops, and routes to `/setup-key` with the message "Device key was rejected. Enter the key your dispatcher issued." Rotated and revoked keys take the same path. A key rotated by an admin fails with 401 and the driver re-enters the new one.
- Key setup is reachable from Today as a "Device key" link, so a driver can replace a key without a 401.

**GF-229 — Transition state module.** `actionsFor(state)` is a pure map from job state to driver one-tap actions:
- `assigned` → `accept` (POST `/driver/jobs/:id/accept`)
- `accepted` → `start` (POST `…/start`)
- `en_route` → `pickup` (POST `…/pickup`)
- `picked_up` → `deliver` (opens the DeliverPage, multipart POST `…/deliver`)
- `created`, `delivered`, `failed`, `cancelled`, and any unknown string → no action.

Fail is **not exposed** in P14. Its reason is required and the roadmap's P14 scope names one-tap transitions only. The endpoint and the D-23 edges stay in place, so P15 or a later phase can add it. The module covers the fail edges in its table with no action, so the test proves the omission is deliberate. This module is the reuse point for P15.

**GF-230 — Routes, freshness.** Routes: `/login`, `/setup-key`, `/today`, `/jobs/:id/deliver`, with `/` redirecting to `/today`. Today's list uses TanStack Query with `refetchInterval: 30_000` and refetch on window focus. A successful transition or delivery invalidates the today query. No WebSocket, no optimistic update, and no offline cache (P15). *Why:* polling is the README §3 fallback and needs no hub change.

**GF-231 — Photo capture and delivery submit.** `<input type="file" name="photo" accept="image/jpeg,image/png,image/webp" capture="environment">`. The camera opens on mobile without `getUserMedia`, which keeps the flow jsdom-testable. Client checks in `checkPhotoFile`: the declared MIME is in the three types and the size is ≤ 5242880 bytes. These are UX pre-checks only; the server sniffs the bytes and is authoritative. Submit is disabled until a photo is chosen. On submit, `lat` and `lng` come from one `getCurrentPosition` call (`enableHighAccuracy: true`, `timeout: 10000`); if it fails, submit is blocked with a message. The form is built with `buildDeliverForm(photo, lat, lng)`, which uses the field names `photo`, `lat`, `lng`. Responses: 200 → back to `/today`; 409 → "Job changed; refreshed" plus refetch; 413 → "Photo is too large"; 503 → "Proof storage is unavailable. Try again."

**GF-232 — Position reporter.**
- Shift model: a boolean "On shift" toggle on the Today header, persisted in `localStorage` under `fleetops.driver.onShift.v1` (`'1'` or `'0'`), default off. The toggle is disabled with the message "Set up the device key first" when no key is stored. *Why:* the roadmap says "while shift active", and a client flag is the only signal the PWA has.
- Cadence: a `setInterval` tick every 10 s. Each tick makes one `getCurrentPosition` call (`enableHighAccuracy: true`, `maximumAge: 5000`, `timeout: 8000`). A geolocation error skips the tick and sets a status line.
- Payload (`fixToIngestBody`): `lat`, `lng` from the coordinates; `accuracy` from `coords.accuracy`; `speed` and `heading` from the coordinates, with `null` or NaN mapped to `0` (the ingest schema requires numbers); `ts` as `new Date(position.timestamp).toISOString()`. The body is always ≤ 4096 bytes.
- Request: `POST /ingest/position` with `Content-Type: application/json` and header `X-Device-Key`. No JWT.
- Responses: 204 → ok. 503 `busy` or a network error → dropped silently, next tick retries. 400 `validation_failed` → dropped and the status line shows "Position rejected" (a bug signal, not retried). 401 → GF-228 path.
- Visibility: ticks are skipped while `document.visibilityState !== 'visible'`. P14 does no background tracking. The reporter resumes on the next visible tick.

**GF-233 — Testing approach.** Vitest with `jsdom`, `setupFiles: src/test/setup.ts` (jest-dom matchers and `cleanup`), and `NODE_ENV=test` to pin React's dev build, mirroring `apps/web/vite.config.ts`. Components use `fireEvent` from `@testing-library/react` (no `user-event`). The API module is mocked with `vi.mock`. `fetch` and `navigator.geolocation` are stubbed with `vi.stubGlobal`. The reporter uses fake timers. No real service worker, camera or geolocation runs in tests. *Why:* the `apps/web` idioms are proven here, and a new dev dependency is not justified for P14.

**GF-234 — Build artifact check.** `scripts/check-pwa-build.mjs` (zero dependencies) runs at the end of `build` and exits non-zero with a named failure: `dist/manifest.webmanifest` parses as JSON, has `name`, `start_url`, `display: "standalone"` and icons at `192x192` and `512x512`; `dist/sw.js` exists and is non-empty; `dist/registerSW.js` exists; `dist/index.html` links `manifest.webmanifest` and references `registerSW.js`. *Why:* the roadmap's build check asserts the PWA artifacts, and a machine check is the only thing P14 can assert without a browser.

**GF-235 — CI and CLAUDE.md.** No CI change in P14. The driver-app gates run on the host, as the `apps/web` gates do; wiring CI is a gate change outside roadmap §5 and outside the scope fence. Add to `CLAUDE.md` a `## Driver PWA (P14)` block with: the dev and preview origin `http://127.0.0.1:10013`; the scripts; the proxy set; the `localStorage` device-key caveat; the on-shift flag key. The env table stays unchanged (no new Go env names). The file must stay under 200 lines (136 today).

## Validation

Automated checks, run on this host. Each has a pass condition.

1. **Backend untouched and green.** `make fmt-check`, `make lint`, `make test` (`-race -count=1`). Pass: all exit 0. The PostGIS tests need Docker. If Docker is unavailable, record the skip; do not report green.
2. **`apps/web` gates unchanged.** `npm --prefix apps/web ci --include=dev`, `npm --prefix apps/web run typecheck`, `npm --prefix apps/web test`, `npm --prefix apps/web run build`. Pass: all exit 0.
3. **`apps/driver-pwa` gates (new).**
   - `npm --prefix apps/driver-pwa ci --include=dev`: exit 0.
   - `npm --prefix apps/driver-pwa run typecheck`: exit 0.
   - `npm --prefix apps/driver-pwa test`: exit 0. The BINDING units (`actions.test.ts`, `key.test.ts`) appear in the output and pass.
   - `npm --prefix apps/driver-pwa run build`: exit 0. `dist/` contains `manifest.webmanifest`, `sw.js` and `registerSW.js`, and `dist/index.html` references them (asserted by GF-234).
4. **Scope fence and secrets.**
   - `git diff --name-only main...HEAD | grep -vE '^(apps/driver-pwa/|CLAUDE.md$|specs/features/14-driver-pwa-online.md$|prompts/)'` → empty output. This rules out `README.md`, `deploy/PORTS.md`, `deploy/docker-compose.yml`, `specs/constitution/`, `db/`, `internal/`, `services/`, `tools/` and `apps/web/`.
   - `git check-ignore apps/driver-pwa/node_modules apps/driver-pwa/dist` → both ignored; `git status --porcelain` shows no `node_modules` or `dist`.
   - `git diff main...HEAD | grep -oE 'fo_dev_[0-9]+_[A-Za-z0-9_-]{43}' | sort -u` → only the fixture form `fo_dev_12_` followed by 43 `A`. No real key appears in the diff.
   - `wc -l CLAUDE.md` → below 200.
5. **Manual follow-up (roadmap validation; not a CI gate, not automated).** Recorded in the Execution record when run:
   - Start compose and the API per CLAUDE.md, seed, and create a driver user linked to a driver.
   - Issue a `kind=pwa` device, pair it to a vehicle with that driver (`vehicle_pairings`), and keep the key shown once.
   - `npm --prefix apps/driver-pwa run build && npm --prefix apps/driver-pwa run preview`, then open `http://127.0.0.1:10013` in Chrome and install.
   - Log in, enter the key, turn the shift on, and run one job through assigned → accepted → en_route → picked_up → delivered with a photo. Pass: each tap moves the job state in the API, the delivery photo and proof record exist, and `positions` rows for the device appear.

## Testing depth

| Layer | Tool | Cases | Binding |
|---|---|---|---|
| Unit: transition mapping | Vitest, `src/jobs/actions.test.ts` | Table: `assigned`→accept; `accepted`→start; `en_route`→pickup; `picked_up`→deliver; `accepted`, `en_route`, `picked_up` → fail edge present, no action; `created`, `delivered`, `failed`, `cancelled`, `"bogus"` → none. Asserts the table covers every D-23 driver edge. | **Yes** (roadmap) |
| Unit: key validation | Vitest, `src/device/key.test.ts` | Valid key; valid with surrounding whitespace (trimmed); bad prefix (`fo_tracker_…`); non-numeric id (`fo_dev_abc_…`); leading zero (`fo_dev_0…`); secret 42 and 44 chars; forbidden chars (`+`, `/`, `=`, `.`); internal whitespace; empty. Each row asserts `ok` and `reason`. | **Yes** (roadmap) |
| Unit: client and payload | Vitest | `client.test.ts`: `ApiError` code mapping, `http_error` fallback, 401 clears session. `payload.test.ts`: null speed/heading → 0, `ts` ISO, field set. `deliver.test.ts`: photo type and size, form field names. | No (supporting) |
| Component: Today | Testing Library | Button per state; tap calls `transitionJob` with `{}`; disabled while pending; empty list; 409 message and refetch. | Roadmap names TodayPage |
| Component: KeySetup | Testing Library | Invalid key blocked with message, no write, no request; valid key stored trimmed. | Roadmap names KeySetup |
| Component: Deliver | Testing Library | Submit blocked until photo; blocked when geolocation fails; success returns to `/today`; 409/413/503 messages. | Roadmap names DeliverFlow |
| Component: PositionReporter | Testing Library, fake timers | Posts each tick while on shift with `X-Device-Key` and body; nothing after off; 503 dropped and next tick posts; 401 clears key and calls `onKeyRejected`; hidden page skips. | Roadmap names PositionReporter |
| Component: login and gating | Testing Library | 401 `invalid_credentials` message; non-driver blocked; three redirects. | No (supporting) |
| Build artifact | `node scripts/check-pwa-build.mjs` (in `build`) | Manifest fields and icons; `sw.js`; `registerSW.js`; `index.html` links. | Roadmap build check |
| Manual | Chrome install and one-tap run | §Validation 5. | Roadmap validation (manual) |

No real service worker, camera or geolocation in automated tests. No Playwright (P16).

## SPEC-AMEND repeat

P14 adds **no new SPEC-AMEND row**. Every endpoint it consumes exists: `POST /auth/login` (P2), `GET /driver/jobs/today` and the accept, start, pickup and fail routes (P11), `POST /driver/jobs/:id/deliver` (P12), `POST /ingest/position` (P4). D-29 and D-30 were amended in P3 (GF-3). D-38 already pins `vite-plugin-pwa`. The PWA is new frontend surface, recorded here as GF-224 to GF-235.

The GF-192 hub identity binding remains open and belongs to P23, as stated in P13. P14 does not touch the hub and does not depend on it.

## Out of scope

- Offline queue, `idb`, and service-worker data caching (P15). The app shell is cached; API responses are not.
- Playwright (P16).
- Geofences (P17+).
- Reports (P21).
- Push notifications (GF-1 backlog).
- Fail UI (GF-229): the endpoint stays; the UI is deferred.
- Live `job.updated` over WebSocket (polling only; GF-230).
- Maskable icon (GF-226).
- Backend Go or `db/` changes. None are needed (GF-227). If a later research pass finds one, it must be scope-fenced in this spec's Validation §4 with a reason.
- Changes to `README.md`, `deploy/PORTS.md`, `deploy/docker-compose.yml`, `specs/constitution/`, the hub origin allowlist, or the dispatcher web app.
- Starting compose, merging to `main`, pushing, CI wiring.
- Any P15+ content.

## Execution record

Run on branch `phase-14-driver-pwa-online` at G7 (`0e9104a`) plus the G8 changes (CLAUDE.md block, this record). Host has Docker, so PostGIS tests ran (no skips).

Automated checks (exit codes):

| # | Check | Result |
|---|---|---|
| 1 | `make fmt-check` | 0 |
| 1 | `make lint` | 0 (0 issues) |
| 1 | `make test` | 0 (24 packages ok, 0 FAIL, 0 skip) |
| 2 | `npm --prefix apps/web ci --include=dev` | 0 (clean install) |
| 2 | `npm --prefix apps/web run typecheck` | 0 |
| 2 | `npm --prefix apps/web test` | 0 (68 tests) |
| 2 | `npm --prefix apps/web run build` | 0 |
| 3 | `npm --prefix apps/driver-pwa ci --include=dev` | 0 (clean install) |
| 3 | `npm --prefix apps/driver-pwa run typecheck` | 0 |
| 3 | `npm --prefix apps/driver-pwa test` | 0 (12 files, 82 tests; BINDING `actions.test.ts` 13 rows and `key.test.ts` 17 rows pass) |
| 3 | `npm --prefix apps/driver-pwa run build` | 0 (`check-pwa-build: ok`) |
| 4 | scope fence `git diff --name-only main...HEAD \| grep -vE …` | empty |
| 4 | `git check-ignore apps/driver-pwa/node_modules apps/driver-pwa/dist` | both ignored; no `node_modules` or `dist` in `git status` |
| 4 | `fo_dev_` key-shaped strings in diff | none (fixtures are built with `'A'.repeat(43)`) |
| 4 | `wc -l CLAUDE.md` | 148 (< 200) |

Manual follow-up (Validation §5): not run. It needs compose, a seeded API, a `kind=pwa` device, and a Chrome install, none of which ran here. Recorded as open.

Deviations from the spec text:

- GF-226: the installed `vite-plugin-pwa` 2.0.0 option is `strategies`, not `strategy`. The value is still `'generateSW'`.
- Test setup (`src/test/setup.ts`): Node 26 defines an experimental `localStorage` global that is undefined without `--localstorage-file`, and it hides jsdom's. The setup installs an in-memory `Storage` only when the global is not functional.
- Files beyond the G1–G7 lists: `api/types.ts`, `api/ingest.ts`, `api/driver.test.ts`, `api/queryClient.ts`, `session/refresh.ts`, `session/logout.ts`, `session/boot.ts` (the refresh-cookie boot the spec's GF-227 implies), `routes/guards.tsx`, `position/geo.ts`, `position/shift.ts`, `test/session.ts`, `test/render.tsx`.
- `useJobTransition` and `useDeliverJob` invalidate the Today query on settle (success or error), so a 409 refetches.
- The position reporter mounts in `RequireDeviceKey`, so it stops while the driver is on `/setup-key`.

Known limitations carried into P15: the `autoUpdate` reload can interrupt a form (GF-226), and `localStorage` holds the device key (GF-228).
