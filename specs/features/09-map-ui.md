# Feature 09 — Operations map UI (P9)

Status: approved plan, persisted 2026-10-26. Branch `phase-9-map-ui`.
Source plan: `/root/.claude/plans/p9-plan-snoopy-balloon.md` (approved, self-gate amendment applied). Binding constitution: `specs/constitution/roadmap.md` §1 (R-1…R-9), §3 P9, §5, `specs/constitution/tech-stack.md` §1, §2, §3, §4, §7.3, §9, §11 (D-02, D-03, D-21, D-22, D-27, D-38, D-40), `specs/constitution/mission.md` §3 (item 4) and §7 (SPEC-AMEND rows), `specs/features/08-map-api-simulator.md` (P8 decisions GF-126…GF-151 your UI consumes), `specs/features/07-fanout-ws.md` (P7 decisions GF-100…GF-125 your WS client honors), `AGENTS.md`, `CLAUDE.md`.

This spec is written before any P9 code (R-1, SDD). Implementation starts at Plan task group G0.

Decisions GF-152 onward are new in P9. Nothing here edits README, PORTS.md, compose, the constitution, or any Go code. Where a roadmap or CLAUDE.md wording conflicts with what the API and hub can actually do, the conflict is resolved in a GF entry and called out there.

## Scope

P9 delivers the first frontend: `apps/web` (Vite + React 18 + TypeScript strict, TanStack Query, React Router, MapLibre GL JS). It contains:

- Login page, in-memory access token, silent refresh through the `/auth`-scoped cookie, route guard, logout.
- Operations map: GeoJSON source with native clustering, circle layers, status colors, click-for-details card, follow-vehicle mode.
- Gray-out of stale markers after 120 s, reconciled with the server's offline edge (D-27).
- WS `/stream` client that merges `position.batch` into the TanStack Query cache and handles `presence.changed` honestly.
- Vite dev server on host port 10012 with a proxy: REST to 10010, `/stream` to 10011.
- Vitest unit and component suites, `vite build` and `tsc -b` gates, CLAUDE.md rows.

Out of scope for P9 (see the end): jobs, alerts, geofences, reports, assignment, metrics UI, E2E, driver PWA, basemap tiles, Go changes, Makefile changes.

## Plan

Build order. Each group lists its tests by name. Test names are binding.

**G0 — Preflight.**
- Run `make fmt-check lint test` on `phase-9-map-ui`. Go is untouched. Expect green. If PostGIS tests need Docker and Docker is absent, record that and continue (frontend work does not depend on it).
- Record `node -v` (expect v26.x), `npm -v` (expect 11.x), and `docker info` status.
- Tests: none (baseline gate). Output recorded in the P9 implement log.

**G1 — Scaffold `apps/web`.**
- Create `apps/web` from the Vite `react-ts` template layout by hand (no interactive scaffolder). Files: `package.json`, `vite.config.ts`, `tsconfig.json` (references), `tsconfig.app.json` (src, strict), `tsconfig.node.json` (vite.config.ts), `index.html`, `src/main.tsx`, `src/App.tsx`, `src/test/setup.ts`, `apps/web/.gitignore` (ignores `node_modules/` and `dist/`).
- Install pinned deps per GF-152. Commit `package-lock.json`.
- `vite.config.ts` per GF-153 (host, port, strictPort, proxy, Vitest jsdom environment).
- `App` mounts `QueryClientProvider` and `BrowserRouter`. Routes per GF-155.
- Tests: `App renders the login route without crashing` (Vitest smoke, jsdom).
- Gate: `npm --prefix apps/web run build` exits 0.

**G2 — Auth.**
- `src/auth/session.ts`: module-level store `{accessToken, expiresAt, claims}` with `subscribe`. No localStorage, no sessionStorage, no cookie access from JS.
- `src/auth/refresh.ts`: `singleFlightRefresh()` per GF-154. One in-flight promise; every caller awaits it.
- `src/api/client.ts`: `apiFetch(path, init)` attaches `Authorization: Bearer`, on 401 awaits `singleFlightRefresh()` and retries once, and on a second 401 calls local logout and navigates to `/login`.
- `src/auth/LoginPage.tsx`: email and password form. `POST /auth/login`. On success, store the token and decode claims. Driver role is refused per GF-154.
- Boot: `src/auth/boot.ts` calls `singleFlightRefresh()` once. Success → authenticated. 401 → unauthenticated.
- Logout: `POST /auth/logout`, then clear the store, call `queryClient.clear()`, navigate to `/login`. Local state clears even if the request fails.
- Tests: none in Vitest (roadmap P9 names no auth unit suite). Verified by the scripted REST and cookie checks in G10 (`login-cookie`, `refresh-rotation`, `proxy-paths`).

**G3 — Bbox derivation and map query.**
- `src/map/bbox.ts`: `bboxParam(bounds)` per GF-156.
- `src/map/useMapVehicles.ts`: TanStack Query hook, key `['map-vehicles', bbox]`, `placeholderData: keepPreviousData`, `staleTime: 5_000`, `refetchInterval: 15_000`. Calls `GET /map/vehicles?bbox=` through `apiFetch`.
- `src/map/useVehicleIndex.ts`: query `['vehicles']` for the device→vehicle map per GF-163. `GET /vehicles` (ops roles only), `staleTime: 60_000`, `select` builds `Map<device_id, vehicle_id>` from `paired_device_id`.
- Tests: `bboxParam clamps to world bounds and formats six decimals`; `bboxParam returns the world box when the view wraps the antimeridian`.

**G4 — Status display and gray-out (pure).**
- `src/status/display.ts`: `statusColor`, `isGrayed(v, nowMs)` (GF-160), `toFeatureCollection(vehicles, nowMs)` (GF-157). `CLIENT_STALE_MS = 120_000`.
- Tests: `statusColor maps idle, en_route, offline to the GF-157 hexes`; `isGrayed is false at exactly 120000 ms and true at 120001 ms`; `isGrayed is true for status offline regardless of age`; `isGrayed is false for negative age (clock skew)`; `toFeatureCollection sets color and grayed per vehicle`.

**G5 — Map view (thin).**
- `src/map/MapView.tsx`: owns the MapLibre instance, mounts source and layers, forwards `moveend`, `dragstart`, `zoomstart`, click events to the pure modules. Renders no logic of its own beyond wiring.
- Source per GF-158: `cluster: true`, `clusterRadius: 50`, `clusterMaxZoom: 14`, `clusterMinPoints: 2`.
- Layers: `clusters` (circle, step radius), `unclustered` (circle, `['get','color']`, opacity from `grayed`).
- Clicks: unclustered → select vehicle (opens card, enables follow). Cluster → `getClusterExpansionZoom` then `easeTo`. Empty map → deselect.
- A 1 s `useNow` hook drives re-render of the feature collection (GF-160).
- Tests: none for the map itself (jsdom has no WebGL, GF-167). Covered by `toFeatureCollection` and `isGrayed` tests (G4), `followCenter` (G7), and the build gate.

**G6 — Details card.**
- `src/details/VehicleCard.tsx`: props `{snapshot, nowMs, onClose}`. Fields exactly per GF-159.
- Tests: `VehicleCard renders exactly the GF-130 fields`; `VehicleCard shows a dash for null speed and heading`; `VehicleCard closes on Escape and on the close button`; `VehicleCard shows the stale badge when grayed`.

**G7 — Follow-vehicle (pure logic plus wiring).**
- `src/map/follow.ts`: `followCenter(prev, next)` and `nextFollowId(current, event)` per GF-161.
- `MapView` calls `map.easeTo({center, duration: 500})` (zoom unchanged) when `followCenter` returns a target.
- Tests: `followCenter returns the new position when the followed vehicle moves`; `followCenter returns null when the position is unchanged`; `nextFollowId clears follow on a user drag only`; `nextFollowId keeps follow on zoom and on programmatic moves`.

**G8 — WS client, reducer, cache merge.**
- `src/ws/reducer.ts`: pure `reduceWsEvent(state, raw, index)` per GF-163, plus `backoffDelay(attempt, rand)` per GF-162.
- `src/ws/client.ts`: socket lifecycle per GF-162. Imperative, thin, and not unit-tested. It calls the reducer and writes the result with `setQueryData` for the active bbox key.
- `src/ws/ConnectionBadge.tsx`: shows Live, Reconnecting, or Offline (text only).
- Tests: `position.batch merges points into rows by device-to-vehicle index`; `a replayed msg_id is skipped`; `an older device_ts does not overwrite a newer row`; `presence.changed does not mutate rows and requests a map refetch`; `unknown event type is ignored without throwing`; `a 200-point batch is applied atomically in one call`; `auth.ok marks the state connected`; `backoffDelay caps at 30 s and stays within jitter bounds`.

**G9 — Gates and CLAUDE.md.**
- `apps/web/package.json` scripts per GF-166. `typecheck` = `tsc -b`. `build` = `tsc -b && vite build`. `test` = `vitest run`.
- CLAUDE.md gets the rows in GF-169 and nothing else.
- Gates: `npm --prefix apps/web run typecheck`, `npm --prefix apps/web test`, `npm --prefix apps/web run build`.
- Tests: the full Vitest suite runs green (the named tests above).

**G10 — Validation run.** See Validation below. Scripted checks are written in `apps/web/scripts/check-live.mjs` (Node, no test framework, not part of `vitest run`).

## Requirements

### Carried verbatim from the constitution

- **D-27** — `vehicles.status` is the registry status; map display status `{idle, en_route, offline}` is derived server-side: `offline` if `server_ts` older than 120 s; `en_route` if the job is `en_route` or `picked_up`; otherwise `idle`. (tech-stack §11.)
- **D-38** — Frontend libraries: React Router, `idb`, `vite-plugin-pwa`, MapLibre GeoJSON circle layers with clustering (not DOM markers). Vite dev proxy: REST → 10010, `/stream` → 10011; no CORS in dev. (`idb` and `vite-plugin-pwa` are P15/P16, not P9.)
- **D-40** — CLAUDE.md is updated in the same branch for mechanical rows. No README or constitution edits.
- **D-03** — Access JWT: HS256, claims `sub, fleet_id, role, exp`, 15 min. WS closes at `exp`. The client honors this (GF-162).
- **D-21** — WS auth by first message `{"type":"auth","token":…}` within 5 s. Token `fleet_id` must match `?fleet=`. Room cap 2,000. Ping 20 s, evict after 60 s. Close at token expiry. The client honors this (GF-162).
- **D-22** — Tunables are optional `FLEETOPS_*` env vars. P9 adds none (GF-168).
- **D-02** — Refresh cookie is httpOnly, `SameSite=Strict`, path `/auth`, and rotated on every use. Reuse of a rotated token revokes the family. The client honors this with single-flight refresh (GF-154).
- Roadmap §5 freshness gate: first measured in P9, manual, p95 ≤ 2 s at 50 vehicles (GF-165).

### Mission §7 rows that apply to P9

None are added. P9 introduces no SPEC-AMEND. The rows already in §7 that this phase depends on are D-02/D-03 (refresh endpoints, cookie) and D-17 (WS carries `position.batch` and `presence.changed`).

### New decisions

**GF-152 — Dependencies and pins.** Exact versions are written into `apps/web/package.json` at G1 by running `npm view <pkg> version`, and `package-lock.json` is committed. The versions below are what the 2026-10-26 search snapshots reported. They are not registry-confirmed, so G1 must check each one and record the value it pins.

Runtime:

| Package | Pin rule | Why |
|---|---|---|
| `react`, `react-dom` | 18.3.x (latest 18) | README §4 and the roadmap say React 18. |
| `react-router` | latest 7.x | v8 is ESM-only with new baselines; keep the stable 7.x line. |
| `@tanstack/react-query` | 5.x (observed 5.101.1) | Query v5 API (`keepPreviousData`, `setQueryData`, `select`). |
| `maplibre-gl` | 5.x (observed 5.24.0) | v6 is ESM-only, requires WebGL2, and changes the default import. Deferred to avoid a mid-phase migration. |

Dev:

| Package | Pin rule | Why |
|---|---|---|
| `vite` | 8.x (observed 8.0.16) | Current major per the search snapshot. Pin the latest 8.x patch at G1. |
| `@vitejs/plugin-react` | version whose peer range covers the pinned `vite` | JSX transform and fast refresh. |
| `typescript` | latest 5.x | `tsc -b` with project references. |
| `@types/react`, `@types/react-dom` | 18.3.x | Match the React runtime. |
| `vitest` | latest 4.1.x (observed 4.1.10) | Stable line. v5 is beta at the snapshot. |
| `jsdom` | latest | Test environment (roadmap: jsdom for component tests). |
| `@testing-library/react` | 16.x | Supports React 18. |
| `@testing-library/dom` | 10.x | Required peer of `@testing-library/react` 16. |
| `@testing-library/jest-dom` | 6.x | Matchers in the details-card test. |

Rules:
- No state library (no Redux, Zustand, or Jotai). TanStack Query and module stores cover state.
- No UI or CSS framework. Plain CSS in `src/styles.css`.
- No `idb`, no `vite-plugin-pwa`, no `zod`, no lint package in P9. Lint is not a P9 gate (roadmap has no web lint gate).
- If a pinned major has a peer conflict at G1, take the newest compatible version in the same major and record it in the commit message. Changing a major is a new GF entry.

**GF-153 — Vite server and proxy.**
```ts
// apps/web/vite.config.ts (server block)
server: {
  host: '127.0.0.1',
  port: 10012,
  strictPort: true,
  proxy: {
    '/auth':     { target: 'http://127.0.0.1:10010', changeOrigin: false },
    '/map':      { target: 'http://127.0.0.1:10010', changeOrigin: false },
    '/vehicles': { target: 'http://127.0.0.1:10010', changeOrigin: false },
    '/stream':   { target: 'http://127.0.0.1:10011', ws: true, changeOrigin: false },
  },
},
```
- Vite listens on **10012 directly**, not on 5173. The hub's `OriginPatterns` allowlist (GF-110) matches the browser-visible origin, which must be `localhost:10012` or `127.0.0.1:10012`. Listening on 5173 would need a host-side forwarder to 10012, which adds a moving part for no gain. This is a deliberate deviation from the roadmap's "port 5173" wording. The browser-visible port is 10012, and CLAUDE.md already maps the web dispatcher to host 10012.
- `host: '127.0.0.1'`. A browser that resolves `localhost` to `::1` falls back to IPv4. Validation checks both hostnames.
- `changeOrigin: false` keeps the `Origin: http://localhost:10012` header intact through the proxy, so the hub's allowlist check passes.
- `/ingest` is not proxied. Devices post to the API directly, and the browser never calls it.
- Browser WS URL: `${location.protocol === 'https:' ? 'wss:' : 'ws:'}//${location.host}/stream?fleet=${fleetId}`. Browser code never hard-codes 10011. The proxy is the only route, so there is one origin and no CORS.
- `strictPort` makes a port clash fail loudly rather than moving the port.

**GF-154 — Token storage, refresh, and the 401 cycle.**
- The access token lives in the module store `src/auth/session.ts`. It is never written to localStorage, sessionStorage, IndexedDB, or any cookie readable from JS. A page reload loses it and triggers a silent refresh (below). *Why:* XSS cannot read it at rest.
- The refresh token is the httpOnly cookie. JS never reads it. The browser sends it to `/auth/refresh` and `/auth/logout` through the proxy, since path `/auth` matches.
- `singleFlightRefresh()` keeps exactly one in-flight `POST /auth/refresh`. Every caller (fetch 401, WS auth, proactive timer, boot) awaits the same promise. *Why:* D-02 revokes the whole family when a rotated token is reused, so two concurrent refreshes would log the user out.
- `apiFetch`: attach `Authorization: Bearer <access>`. On 401 (except `/auth/login`), await `singleFlightRefresh()` and retry once. A second 401 runs local logout.
- Proactive refresh: when a token is stored, `expiresAt = now + expires_in·1000` (from the login or refresh response). A timer fires `singleFlightRefresh()` at `expiresAt − 60 s`. Client-clock skew is not corrected; the 60 s margin absorbs normal skew, and the 401 retry covers the rest.
- Boot: `singleFlightRefresh()` once. Success → authenticated. 401 → show the login page. There is no retry loop at boot.
- Login response is `{access_token, token_type, expires_in}`. Claims are decoded client-side from the JWT payload (base64url, no signature check). This is UI gating only; the server enforces every route.
- **Driver role is refused in the UI.** After login, if `role === 'driver'`, show "The dispatcher console requires an operations role", call logout, and make no map or vehicle requests. *Why:* `GET /vehicles` is ops-only (registry route table), so the device→vehicle index (GF-163) cannot be built for a driver, and the map is not a driver surface in P9.
- Login failure: show "Email or password is incorrect" for 401 (`invalid_credentials`). Network failure: show "Cannot reach the server".

**GF-155 — Routes and guard.** Routes: `/login`, `/map`, and `/` (redirect to `/map`). Nothing else. Guard: while boot refresh is pending, render "Loading". No session → redirect to `/login`. Session and at `/login` → redirect to `/map`. Logout returns to `/login`.

**GF-156 — Bbox grammar, query cadence, truncation.**
- `bboxParam(bounds)`: take `map.getBounds()`. Clamp longitude to `[-180, 180]` and latitude to `[-90, 90]`. If `west > east` (the view wraps the antimeridian) or the width is ≥ 360°, send the world box `-180,-90,180,90`. Otherwise send `minLng,minLat,maxLng,maxLat` with six decimal places. This matches the API grammar of GF-128 (planar, no antimeridian split). *Why:* the API does not accept wrapped boxes; the world box is the honest answer for a wrapped view.
- Cadence: `moveend` → 400 ms trailing debounce → new bbox key. `refetchInterval` 15 s is a backstop. The WS merge (GF-163) carries realtime updates.
- `keepPreviousData` keeps markers on screen during a bbox change.
- Truncation: when the response has `truncated: true`, show a banner "Showing the first N vehicles. Zoom in to see all." where N is `vehicles.length`. The banner is the only truncation UX. No pagination.

**GF-157 — Status colors and marker display.**

| Display state | Fill | Notes |
|---|---|---|
| `idle` | `#2563eb` (blue) | Not grayed. |
| `en_route` | `#f59e0b` (amber) | Not grayed. Only appears after P10 wires `JobLookup`; the UI handles it now. |
| `offline` (server) or grayed (GF-160) | `#9ca3af` (gray), opacity 0.45 | Both mean "no recent update". |

- Unclustered circle: radius 7, stroke `#ffffff` 1.5 px.
- Cluster circle: fill `#1e3a8a`, stroke `#ffffff` 1.5 px. Radius by `point_count`: 14 for `< 10`, 18 for `< 50`, 24 for `≥ 50`.
- No text labels in P9. A symbol layer would need a glyphs endpoint that P9 does not provide. Cluster size is shown by radius only.
- `toFeatureCollection(vehicles, nowMs)` writes `properties`: `vehicle_id`, `status`, `grayed` (boolean), `color` (hex, precomputed). Layers read `['get','color']` and `['get','grayed']`. Pure, unit-tested (G4).
- Colors are distinguishable by hue and by the gray/opacity cue. Blue and amber are not confusable for common color-vision deficiencies.

**GF-158 — Cluster layer parameters.** Source: `type: 'geojson'`, `cluster: true`, `clusterRadius: 50` (MapLibre default, pixels), `clusterMaxZoom: 14` (individual vehicles are visible from zoom 15), `clusterMinPoints: 2`. Clicking a cluster calls `getClusterExpansionZoom(clusterId)` and eases to that zoom. *Why:* native clustering is the D-38 choice. A DOM-marker fallback is not allowed.
- Basemap: P9 renders no basemap tiles. The style is a background-only style, which works offline. A tile provider is an operator decision and is not in the roadmap P9 scope. Recorded as a known gap.

**GF-159 — Details card fields (GF-130 only).** The card shows exactly these fields and no others:
- `vehicle_id`
- `lat`, `lng` (six decimal places)
- `speed_kmh` and `heading_deg` (`—` when `null`)
- `status` (text: Idle, En route, Offline)
- `device_ts`, `server_ts` (local time, ISO fallback)
- `age`: `now − server_ts`, formatted as `Ns` below 60 s, otherwise `Nm Ss`
- A "Stale" badge when grayed.

The GF-130 response has no plate, model, or name. The card makes no extra fetch for them. Closes on Escape, the close button, or a click on empty map. If the selected vehicle leaves the current data set, the card keeps the last snapshot with the note "Not in current view". The snapshot is taken at selection and refreshed while the vehicle is present.

**GF-160 — Gray-out rule (reconciled with D-27 and GF-131/GF-132).**
- Client constant `CLIENT_STALE_MS = 120_000` in `src/status/display.ts`. It is a code constant, not an env var. The server threshold `FLEETOPS_PRESENCE_TTL` is not exposed to the client, and P9 adds no endpoint or Go change to expose it.
- Rule: `isGrayed(v, nowMs) = v.status === 'offline' || (nowMs − Date.parse(v.server_ts)) > 120_000`. Exactly 120,000 ms is not grayed. This matches the server boundary in GF-131 ("older than ttl" is offline, "exactly ttl" is not).
- Re-render: a `useNow(1000)` hook updates `nowMs` every second. The feature collection is rebuilt on `[vehicles, Math.floor(nowMs/1000)]`. A silent vehicle grays without any data arriving. *Why:* the gray-out must happen when updates stop, and no update is the signal.
- Consistency with the server (D-27): the server flips status to `offline` at the same 120 s. The client rule is the OR of the two. The two never fight: the client can only gray earlier, never later, than the server status.
- Degradation if `FLEETOPS_PRESENCE_TTL` is changed:
  - TTL < 120 s: the server `offline` edge fires first. The status rule grays it immediately. Consistent.
  - TTL > 120 s: the client grays at 120 s while the server still reports `idle`. The card shows the server status text and the Stale badge. This is the accepted cost of keeping README §2.1.4's 2-minute rule. Operators who change the TTL must also accept that the UI gray-out stays at 120 s.
- Clock skew: browser time is compared with the server's `server_ts` without correction. A browser clock more than 120 s off grays every vehicle. This is visible, not silent. Fix: NTP on the operator machine. Recorded as a known limitation.

**GF-161 — Follow-vehicle mode.**
- Selecting a vehicle enables follow for that vehicle (`followId = vehicle_id`). A "Stop following" button in the card disables it.
- `followCenter(prev, next)`: returns `[lng, lat]` when the followed vehicle's position changed, `null` otherwise.
- Camera: `easeTo({center, duration: 500})`. Zoom is unchanged. *Why:* the operator controls zoom; follow controls only the center.
- Exit rule (pick one, recorded): **a user drag exits follow.** `nextFollowId(current, event)` clears on `dragstart` only when `event.originalEvent` is defined (user-originated). Wheel and pinch zoom do not exit follow. Programmatic moves (no `originalEvent`) never exit follow. Deselect, Escape, and clicking empty map also exit.
- `moveend` from follow moves triggers the debounced bbox refetch (GF-156). This is bounded by the 400 ms debounce.

**GF-162 — WS client lifecycle.**
- URL per GF-153. One socket per authenticated session. `fleetId` comes from the decoded claims.
- On open, the client sends one frame: `{"type":"auth","token":<access>}`. Nothing else is ever sent. Any client data frame after auth makes the hub close with 1008 (GF-113). *Why:* the subscription is implicit in the `?fleet=` room (the roadmap's "subscribe logically").
- Client-side auth timeout: 5 s from open, matching GF-110. No `auth.ok` in time → close and count as failure.
- `auth.ok` → state connected, backoff reset.
- Close-code handling:

| Close | Action |
|---|---|
| 4401 `token expired` or `unauthorized` | `singleFlightRefresh()`, then reconnect. If refresh fails → logout, `/login`. |
| 4403 `fleet mismatch` | Fatal. Stop, show "Fleet mismatch. Sign in again.", logout. |
| 4408 `slow consumer` | Reconnect with backoff. |
| 1001 `server shutdown` | Reconnect with backoff. |
| 1000 (our own close) | No reconnect. |
| 1006, 503 (room full), any other | Reconnect with backoff. |

- Backoff: `backoffDelay(attempt, rand)` = `min(30_000, 1000·2^attempt)` with ±20% jitter (`rand` injected, so it is testable). Retries have no limit. The ConnectionBadge shows the state.
- Proactive reconnect at `expiresAt − 60 s`: open a new socket, auth it, and on its `auth.ok` close the old socket with 1000. *Why:* D-21 closes at `exp` with 4401, and mid-session re-auth is not a protocol feature (a second frame closes the socket with 1008). A new socket avoids a visible gap.

**GF-163 — Reducer signature, cache merge, dedupe.**
- Signature: `reduceWsEvent(state: WsState, raw: string, index: {deviceToVehicle: Map<number, number>}) → {state: WsState, rows: VehicleRow[] | null, effects: Array<'refetch-map' | 'refetch-vehicles'>}`. Pure. `rows` is the full next array for the active bbox key, or `null` when nothing changed.
- `WsState` = `{connected: boolean, seen: Set<string>, seenOrder: string[]}`. `seen` is a bounded ring of the last 10,000 `msg_id` values. Eviction is FIFO.
- `auth.ok`: `connected = true`.
- `position.batch`: ignore if `fleet_id` does not match the connected fleet. Otherwise, for each point in array order:
  1. Skip if `msg_id` is in `seen` (at-least-once replay, GF-103). Otherwise add it to `seen`.
  2. Resolve `device_id` through `index.deviceToVehicle`. Unknown device → add `refetch-vehicles` to effects and skip.
  3. Compare `device_ts` with the row's `device_ts`. Older → skip (out-of-order guard).
  4. Write `lat, lng, speed_kmh, heading_deg, device_ts, server_ts`. Status: if the row was `offline`, set `idle` (a fresh point revives it). Otherwise keep the status. `en_route` is only corrected by the server on refetch, since the client has no job state.
  5. After the loop, if any row changed, add `refetch-map` (debounced 2 s in the client) to reconcile status with the server.
- Atomicity: the whole batch (≤ 200 points, GF-102) is applied in one pure call, then one `setQueryData` write. One render per batch. A batch over 200 is still applied (the client does not reject).
- Cache write: `queryClient.setQueryData(['map-vehicles', activeBboxKey], rows)`. Only the active bbox key is patched. Rows outside the active box are not in the cache and are not added.
- `presence.changed`: no row mutation. Add `refetch-map` (debounced 1 s). See GF-164.
- Unknown `type` (`job.updated`, `alert.raised`, anything else), malformed JSON, or non-object: return the state unchanged with no effects and no throw. *Why:* P10/P11 add those types; P9 must tolerate them.

**GF-164 — `presence.changed` handling (honest).** `presence.changed` carries `device_id` and no `vehicle_id` (GF-105, and GF-101 on the wire). The map rows are keyed by vehicle, and the device→vehicle index exists only because `GET /vehicles` returns `paired_device_id`. P9 does not write presence into rows, because a presence `offline` for a device is not the same fact as a vehicle's display status (GF-131 derives that server-side from `server_ts`). Instead, presence triggers a debounced map refetch, so the displayed status is the server's answer. Known limitation: the presence badge per vehicle is not shown in P9. Device→vehicle resolution on the wire is deferred (GF-105 names it as a P9 concern; the deferral is recorded here, not faked).

**GF-165 — Freshness measurement.** Method: `apps/web/scripts/check-live.mjs` (Node 26, global `WebSocket`, `fetch`). It logs in, opens the hub through the proxy and direct, and collects `position.batch` points for `FLEETOPS_WINDOW_S` seconds (default 120). For each point: `lag_ms = Date.now() − Date.parse(device_ts)` at receipt. Output: count, p50, p95, max. Pass: p95 ≤ 2000 ms. The simulator and the script share one host clock, so there is no skew term. *What P9 measures:* end-to-end freshness at 50 vehicles on this host, from simulator slot to WS receipt. *What P23 finalizes:* simulator timestamps at load (k6, 2,000 msg/s), server-side metric, and a multi-host clock. The browser UI is not measured here; the script covers the transport the UI consumes.

**GF-166 — Build, typecheck, test gates.** In `apps/web/package.json`:
- `"dev": "vite"` (serves 10012 per GF-153)
- `"build": "tsc -b && vite build"` — typecheck and build in one step
- `"typecheck": "tsc -b"`
- `"test": "vitest run"`

`tsconfig.app.json` sets `strict: true`. `vitest` config lives in `vite.config.ts` with `test.environment: 'jsdom'` and `setupFiles: ['src/test/setup.ts']`. The root `Makefile` is **not** changed. CI is not changed in P9. Frontend gates run through npm, documented in CLAUDE.md.

**GF-167 — Map component boundary.** `MapView.tsx` is thin: MapLibre setup, layer mounting, and event forwarding only. All decisions live in pure modules (`status/display.ts`, `map/follow.ts`, `map/bbox.ts`, `ws/reducer.ts`) that are unit-tested. No test renders MapLibre: jsdom has no WebGL, and MapLibre v5 requires WebGL2. The map is verified by the build gate (type-checked against the real `maplibre-gl` types), by the manual visual checks in Validation, and by the scripted feed check.

**GF-168 — Config additions: none.** P9 adds no `FLEETOPS_*` variable and no §12 env name. The browser reads no env vars. Dev-only values (ports, proxy targets) live in `vite.config.ts`. CLAUDE.md's env tables are unchanged.

**GF-169 — CLAUDE.md rows (D-40, mechanical only).** Add one section, "Frontend (P9)", with these rows:
- Install: `npm --prefix apps/web ci`
- Dev server: `npm --prefix apps/web run dev` → `http://localhost:10012` (`http://127.0.0.1:10012`)
- Build (typecheck + build): `npm --prefix apps/web run build`
- Typecheck only: `npm --prefix apps/web run typecheck`
- Unit and component tests: `npm --prefix apps/web test`
- Dev proxy: `/auth`, `/map`, `/vehicles` → `127.0.0.1:10010`; `/stream` → `127.0.0.1:10011` (WebSocket)

The file stays under 200 lines (currently 98). No other CLAUDE.md change.

## Validation

Runnable on this host unless marked MANUAL. Pass conditions are explicit.

**Automated gates (must all pass):**
1. `npm --prefix apps/web ci` exits 0.
2. `npm --prefix apps/web run typecheck` exits 0 (`tsc -b` clean).
3. `npm --prefix apps/web test` exits 0. Named suites: `status/display.test.ts`, `map/bbox.test.ts`, `map/follow.test.ts`, `ws/reducer.test.ts`, `details/VehicleCard.test.tsx`, `App.test.tsx` (smoke).
4. `npm --prefix apps/web run build` exits 0 (`vite build`).
5. Go baseline unchanged: `make fmt-check lint test` green (or Docker-blocked PostGIS tests recorded).

**50-vehicle run (scripted, on this host):**

Infra by one-off `docker run` (same images and host ports as PORTS.md and compose). Compose is not started (standing rule). This is the testcontainer-equivalent path for a manual run:
```
export PGPASS=$(openssl rand -hex 16)
docker run -d --name fo-pg    -p 127.0.0.1:10002:5432 -e POSTGRES_PASSWORD=$PGPASS postgis/postgis:16-3.4
docker run -d --name fo-redis -p 127.0.0.1:10003:6379 redis:7
docker run -d --name fo-nats  -p 127.0.0.1:10004:4222 nats:2 -js
export DATABASE_URL="postgres://postgres:$PGPASS@127.0.0.1:10002/postgres?sslmode=disable"
export JWT_SECRET=$(openssl rand -hex 32)
export DEVICE_KEY_SALT=$(openssl rand -hex 32)
export NATS_URL=nats://127.0.0.1:10004 REDIS_URL=redis://127.0.0.1:10003
make tools && make migrate
export ADMIN_PW=$(openssl rand -hex 12)
printf '%s\n' "$ADMIN_PW" | SEED_ARGS="--admin-email admin@fleetops.test --demo-devices" make seed
```
Pass: seed exits 0; `deploy/.local/devices.json` exists with 50 devices (mode 0600, gitignored).

Processes (one terminal each, or background with logs under a directory outside the repo):
```
go run ./services/api                 # 127.0.0.1:10010
go run ./services/ingest              # consumes GPS_RAW, writes positions, publishes to Redis
go run ./services/ws-hub              # 127.0.0.1:10011
go run ./tools/simulator --fleet=50 --rate=5s --api http://127.0.0.1:10010
npm --prefix apps/web run dev         # 127.0.0.1:10012
```

**Scripted checks** (`node apps/web/scripts/check-live.mjs`, env `FLEETOPS_EMAIL=admin@fleetops.test FLEETOPS_PASSWORD=$ADMIN_PW FLEETOPS_WINDOW_S=120`; credentials are never written to a file):
- **C1 Login and cookie:** `POST http://127.0.0.1:10012/auth/login` returns 200 with `access_token`. `curl -i` on the same response shows `Set-Cookie` with `Path=/auth`, `HttpOnly`, `SameSite=Strict`. Pass: all four present.
- **C2 Proxy REST:** `curl -s -H "Authorization: Bearer $T" "http://localhost:10012/map/vehicles?bbox=-180,-90,180,90"` returns 200 and the GF-130 shape. Repeat on `http://127.0.0.1:10012`. Pass: both 200, both `vehicles` arrays.
- **C3 Fleet count:** within 60 s of the simulator start, the world-bbox response has `vehicles.length === 50` and `truncated === false`. Every `status` ∈ {`idle`,`en_route`,`offline`}.
- **C4 Refresh rotation:** `POST /auth/refresh` through the proxy with the cookie → 200 and a new cookie. A second call with the old cookie value → 401 (reuse detection, D-02). Pass: both as stated.
- **C5 Origin allowlist:** `curl -i --max-time 3 -H "Connection: Upgrade" -H "Upgrade: websocket" -H "Sec-WebSocket-Version: 13" -H "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==" -H "Origin: http://localhost:10012" "http://127.0.0.1:10011/stream?fleet=1"` → status line `101`. Same with `Origin: http://evil.example` → `403`. Pass: 101 and 403.
- **C6 WS auth through proxy and direct:** the script opens `ws://127.0.0.1:10011/stream?fleet=<fleet>` and `ws://localhost:10012/stream?fleet=<fleet>`, sends the auth frame, and expects `auth.ok` within 5 s on both, with `fleet_id` matching and `role` in the ops set. Pass: both.
- **C7 WS negative auth:** bad token → close 4401. Token with `?fleet=999999` → close 4403. Pass: codes exact.
- **C8 Freshness (GF-165):** collect `position.batch` for 120 s. Pass: p95 lag ≤ 2000 ms. The script prints count, p50, p95, max. Pass also requires `msg_id` uniqueness within the window and every batch ≤ 200 points.
- **C9 Unknown types tolerated:** the script records every `type` received. Pass: only `auth.ok`, `position.batch`, and `presence.changed` are seen. No exception.
- **C10 Server gray-out edge (D-27):** stop the simulator (Ctrl-C, expect exit 0 and the GF-147 summary line). Wait 135 s. `GET /map/vehicles` for the world box returns `vehicles.length === 50` and every `status === 'offline'`. Pass: all 50 offline. *Why 135 s:* the offline edge is 120 s; the margin covers the 15 s sweeper tick (GF-106).

**MANUAL (browser, not automatable here):** open `http://localhost:10012` in a browser with the stack running.
- M1: login, the map shows 50 markers in blue (idle). Expect no DOM markers (inspect shows canvas only).
- M2: zoom out to see clusters. Clusters show as dark-blue circles that grow at 10 and 50 members. Zoom in past 14 and they split.
- M3: click a marker. The card shows exactly the GF-159 fields. Escape closes it. The map follows the vehicle. Drag the map: follow stops. Zoom with the wheel: follow continues.
- M4: after the simulator stops (C10), each marker turns gray within 2 minutes of its last update, without a data refresh.
- M5: kill `ws-hub` and restart it. The badge goes Reconnecting, then Live. Positions resume.
- M6: a reload restores the session silently (no login page) through `/auth/refresh`.
- M7: markers update in place (no flicker, no full redraw) while the simulator runs.
Record each as pass or fail in the P9 implement log. The agent does not mark these pass without seeing them.

**Scope fence:** `git diff --name-only main...HEAD` lists only paths under `apps/web/`, `CLAUDE.md`, `specs/features/09-map-ui.md`, and `prompts/`. Any path under `services/`, `internal/`, `db/`, `tools/`, `deploy/`, `README.md`, `specs/constitution/`, or `go.mod` fails the gate. `apps/web/node_modules/` and `apps/web/dist/` do not appear (ignored).

**Secrets:** no token, password, cookie, or key in the repo. `ADMIN_PW`, `PGPASS`, and `JWT_SECRET` are shell-only. `deploy/.local/` stays ignored (`git check-ignore deploy/.local/devices.json` prints the path).

**CLAUDE.md length:** `wc -l CLAUDE.md` < 200.

## Testing depth

| Layer | Tool | Scope |
|---|---|---|
| Unit | Vitest | Status display and colors (GF-157), gray-out timer (GF-160), WS reducer and backoff (GF-163, GF-162), follow-camera logic (GF-161), bbox helper (GF-156) |
| Component | Vitest + Testing Library + jsdom | Details card only (GF-159) |
| Boundary | none | `MapView` is thin and is not rendered in tests (GF-167) |
| Scripted live | Node script | REST, cookie, proxy, origin allowlist, WS auth and close codes, freshness, server gray-out (GF-165, validation C1–C10) |
| Manual | browser | Visual checks M1–M7 |
| E2E | none | P16 owns Playwright. No E2E here. |

Known gaps (recorded, not hidden):
- The auth module (single-flight refresh, 401 retry) has no Vitest suite (roadmap P9 names none). Its risk is covered by C4 (rotation) and M6 (silent restore).
- jsdom cannot render WebGL. MapLibre rendering is verified only manually and by the build gate.
- The reconnect loop and proactive reconnect are verified by C6/C7 and M5, not by fake-timer tests.

## SPEC-AMEND repeat

None. P9 adds no SPEC-AMEND item. The dev port (10012) matches CLAUDE.md's existing web dispatcher row and is not a new deviation. The roadmap's "port 5173" wording is superseded by GF-153 for the browser-visible port and does not change the constitution.

## Out of scope

- Jobs UI, job state, `en_route` data (P10): the UI handles `en_route` but no backend data exists yet.
- Alerts UI and `alert.raised` rendering (P19). Geofences (P17/P18). Reports page (P21).
- Assignment panel (P13). Metrics UI (P22). Driver PWA, `idb`, `vite-plugin-pwa`, offline queue (P11/P15).
- E2E and Playwright (P16). Load testing with k6 and the 10k-connection gate (P23).
- Basemap tiles and text labels on clusters (no tile or glyph provider in the roadmap).
- Device→vehicle resolution on the wire for `presence.changed` (GF-164).
- Mobile layout, theming, production hosting of `apps/web` (P22/P23).
- Go changes of any kind. Makefile or CI changes. README, PORTS.md, compose, constitution edits. Migrations.
- MapLibre v6 migration (deferred; GF-152).
