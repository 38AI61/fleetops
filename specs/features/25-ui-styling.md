# Feature 25 — UI styling pass (P25)

Status: planned on `phase-25-ui-styling`, not implemented, 2026-10-29. Validation results and deviations are appended at the end when the phase runs.
Source plan: /root/.claude/plans/p25-plan-typed-adleman.md (approved, self-gate passed — brief deviations carried with rationale and ledger rows as listed in the plan: GF-363 no merge/push by the child, GF-364 no Go gates; GF-362 corrects the brief's .card-head fact).

## Scope

P25 completes the visual layer of both React apps without changing behavior.

- **Web dispatcher** (`apps/web`): extend the single `apps/web/src/styles.css` to cover login, jobs, new-job, reports, alert bell, zones, and the generic `.title`, `.hint`, `.error` classes. Desktop-first, 1280px design target.
- **Driver PWA** (`apps/driver-pwa`): create `apps/driver-pwa/src/styles.css` (none exists today) and import it from `src/main.tsx`. Phone-first, 375–430px.
- Hand-written CSS only (D-38). Markup changes are limited to added `className` attributes, one import line, and one viewport meta value (see GF-359).
- Fence: **CSS plus minimal className and markup adjustments only. NO behavior, NO route, NO API, NO dependency, NO Go change.**

Binding: `specs/constitution/tech-stack.md` §11 D-38; `specs/constitution/roadmap.md` §1 R-5, R-8, R-9; `specs/constitution/mission.md` §7 (SPEC-AMEND rule); the child brief `prompts/45-p25-styling-brief.md` (lives in the main checkout `/root/fullstack-portfolio/01-fleetops/prompts/`, not in this worktree; its facts were re-read on 2026-10-29, with one correction below). Format precedents: `specs/features/24-cloud-profile.md`, `13-dispatcher-assignment-ui.md`, `19-alerts-core.md`, `21-daily-reports-csv.md` (GFs cited, not restated).

### Verbatim constitution text (carried, not paraphrased)

- tech-stack §11 D-38 (line 297): "Frontend libraries: React Router, `idb`, `vite-plugin-pwa` (Workbox), MapLibre GeoJSON circle layers with clustering (not DOM markers, avoids Leaflet-style cost in §4). Vite dev proxy: REST → 10010, `/stream` → 10011; no CORS in dev. | §4 frontend pins; §9 freshness at thousands of markers."
- tech-stack §2 frontend rows (lines 34–37): no CSS framework is listed. D-38 pins only the libraries above. Adding any CSS framework or utility library is a new dependency and is out of scope.
- roadmap §1 R-5: "Do not change behavior the phase spec does not describe. If the spec is silent or wrong, stop and ask."
- roadmap §1 R-8: "`README.md` is never edited. Spec changes go through the spec process (mission §7)."
- roadmap §1 R-9: "Security defaults in every phase: no secrets in repo; config from env; hashed device keys and license numbers."
- mission §7 rule (line 97): "Rule: any new SPEC-AMEND gets a row here and a decision ID (D-nn from the plan, or GF-n for self-gate additions) in `tech-stack.md` §11 (D-40)."

### Baseline facts (verified read-only, 2026-10-29, main c599bb3)

- **Web stylesheet.** `apps/web/src/styles.css`: 113 lines, 1680 bytes (brief figure), built to `dist/assets/index-*.css` at 1258 bytes. Imported once at `apps/web/src/main.tsx:3`.
  - Styled today: `html, body, #root`, `.map-page`, `.topbar`, `.map-canvas`, `.banner`, `.vehicle-card` (+ `h2`, `dl`, `dt`, `dd`), `.stale-badge`, `.note`, `.ws-badge`, `.new-job`, `.new-job-map` (+ `.map-canvas`), `.new-job-form`.
  - **Correction to the brief.** `.card-head` is styled, but only as `.vehicle-card .card-head`. Any use outside `.vehicle-card` is unstyled.
  - Used but unstyled: `login`, `jobs-page`, `new-job-page`, `reports-page`, `reports-controls`, `reports`, `alert-bell`, `alert-dropdown`, `alert-badge`, `alert-kind`, `alert-acked`, `alert-error`, `alert-severity`, `alert-info|warning|critical`, `zones-page`, `zones`, `zones-side`, `zones-draw`, `zones-list`, `zones-map`, `title`, `hint`, `error`.
- **Driver PWA.** No CSS file under `apps/driver-pwa/src`. No stylesheet `<link>` in `apps/driver-pwa/index.html`. No CSS import in `src/main.tsx`. **Zero `className` attributes** in `apps/driver-pwa/src` (grep-verified). Structure is semantic only: `main`, `header`, `h1`, `h2`, `ul > li`, `form`, `label`, `input`, `button`, `p[role=status|alert]`.
- **index.html viewport.** Driver `index.html` meta is `width=device-width, initial-scale=1.0`. It lacks `viewport-fit=cover`, so `env(safe-area-inset-*)` evaluates to 0 on iOS Safari.
- **Build.** `check-pwa-build.mjs` checks the manifest, `sw.js`, `registerSW.js`, and that `index.html` contains `manifest.webmanifest` and `registerSW.js`. It does not check CSS. Driver build: `tsc -b && vite build && node scripts/check-pwa-build.mjs`. Web build: `tsc -b && vite build`. The MapLibre chunk `dist/assets/display-*.css` (69808 bytes on main, brief figure) is untouched.
- **Test coupling (read-only audit, 31 web and 29 driver test files).** No test asserts on CSS, class names, `toHaveClass`, computed style, or snapshots. No test imports `styles.css`. Vitest runs with `environment: 'jsdom'` and no CSS processing. Structure-sensitive assertions that markup must not change:
  - `apps/web/src/jobs/JobsPage.test.tsx:81` `getByText('delivered').closest('tr')`
  - `apps/web/src/jobs/AssignmentPanel.test.tsx:60–61` `getAllByRole('row')` then `querySelectorAll('td')[1]`
  - `apps/web/src/details/VehicleCard.test.tsx:25–28` `querySelectorAll('dt')` order
  - `apps/driver-pwa/src/pages/TodayPage.test.tsx:42, 163` `closest('li')`, `getAllByRole('listitem')`
  - `apps/driver-pwa/src/offline/ConflictList.test.tsx:51, 61–62` `closest('li')`, listitem count of 2
  - All other assertions query copy, roles, or labels (low risk). Copy, role, and label changes are forbidden by GF-359.
- **Ledger.** Highest existing GF is GF-355 (P24 spec). New decisions start at GF-356.
- **CLAUDE.md** is 197 lines. P25 does not change it.

## Plan

Build order. Each group names its checks. No commits, no push, no merge in this phase (GF-363).

**G0 — Preflight (no code change).** Run on the phase branch worktree.
- `npm --prefix apps/web ci --include=dev` and `npm --prefix apps/driver-pwa ci --include=dev` (no-op if installed).
- Baseline: `npm --prefix apps/web test`, `npm --prefix apps/web run typecheck`, `npm --prefix apps/web run build`; `npm --prefix apps/driver-pwa test`, `npm --prefix apps/driver-pwa run typecheck`, `npm --prefix apps/driver-pwa run build`. All exit 0.
- Record baseline: `wc -c apps/web/dist/assets/*.css` (expect index 1258 B, display chunk 69808 B); driver `ls apps/driver-pwa/dist/assets/*.css` expect none.
- Logs go to `deploy/.local/p25-validation/` (gitignored). Pass: all exit 0.

**G1 — Web tokens and shared elements (`apps/web/src/styles.css`).** Add the token block on `:root` (values in GF-357). Add shared rules:
- Topbar nav polish. Links and the active nav item use the accent. `.topbar` wraps (`flex-wrap: wrap`) below 1024px.
- Generic `.title` (page heading), `.hint` (muted helper text), `.error` (danger text with `role=alert` context).
- Base `button`, `input`, `select`, `label` styling. Primary `button` uses the accent; `button:disabled` uses the disabled tokens with `cursor: not-allowed`.
- `:focus-visible` outline on `a, button, input, select, [tabindex]` (GF-357 focus rule).
- Base `table` (border-collapse, header row, muted `th`, row hover, `font-variant-numeric: tabular-nums`).
- Tests: `npm --prefix apps/web test` green, build, typecheck, and grep check (GF-362) for `:focus-visible` and `:disabled`.

**G2 — Login (`.login`, `src/auth/LoginPage.tsx`).** Centered card (max-width 360px) on `--color-bg`. Branded header (`h1` in accent weight). Stacked form fields, full-width primary submit. Error uses `.error`. Tests: `App.test.tsx` green (role button "Sign in" unchanged).

**G3 — Jobs (`.jobs-page`, `src/jobs/JobsPage.tsx`, `AssignmentPanel.tsx`, `src/status/display.ts`).**
- Page container: `overflow-x: auto` (GF-360).
- Filter bar: a horizontal flex row of labels, selects, and inputs with wrap.
- Jobs table: base `table` plus `min-width: 960px` inside `.jobs-page`.
- State chips: no web element has a state class today (inventory, `jobs/JobsPage.tsx`). Add `className="state-chip state-<state>"` to the existing state `span` (GF-359 item 2) and style it with the GF-357 chip table. Text label stays visible at all times.
- Assignment panel (`.card-head` used outside `.vehicle-card`): card with border, radio list for nearest candidates, distance right-aligned with tabular numbers.
- Assign button states: default, hover, `:disabled`, focus-visible.
- `alert-error` styled as a danger block.
- Tests: `JobsPage.test.tsx` and `AssignmentPanel.test.tsx` green. No structure changes (see pins).

**G4 — New job (`.new-job-page`, `.new-job`, `.new-job-form`, `.new-job-map`, `src/jobs/NewJobPage.tsx`, `JobCreateForm.tsx`).**
- Page container with the `.new-job` split: form column 340px, map flex-1. Below 1024px the split stacks (`flex-wrap: wrap`). Map `min-height` drops to 360px under 1024px.
- Form: labelled fields stacked with 12px gaps. Clear buttons (`Clear pickup`, `Clear dropoff`) styled as tertiary buttons.
- Map integration: `.new-job-map .map-canvas` height 100%. The `.hint` under the map explains picking.
- Tests: `JobCreateForm.test.tsx` and `NewJobPage` tests green. Map behavior untouched.

**G5 — Reports (`.reports-page`, `.reports-controls`, `.reports`, `src/reports/ReportsPage.tsx`).**
- Controls row: date input, then the CSV link styled as a secondary button (`.reports-controls a`).
- Report table: base `table` plus `min-width: 720px` inside `.reports-page`. Numeric columns use tabular numbers.
- Tests: `ReportsPage.test.tsx` green (label "Date", link "Download CSV", texts unchanged).

**G6 — Alert bell (`.alert-bell`, `.alert-badge`, `.alert-dropdown`, `.alert-kind`, `.alert-severity`, `.alert-info|warning|critical`, `.alert-acked`, `.alert-error`, `src/alerts/AlertBell.tsx`).**
- Bell button with an unread count badge (accent or critical color per GF-357). Badge is a circle with a 1.25rem minimum width.
- Dropdown: absolute, `z-index` above the map, max-height 60vh with scroll, border and card shadow.
- Kind label muted and small caps. Severity chip per the GF-357 severity table. Acked rows at `--color-text-muted` with the Ack button hidden by the component (no CSS change to behavior).
- Tests: `AlertBell.test.tsx` green (buttons "Alerts" and "Ack", role `alert`, label text unchanged).

**G7 — Zones (`.zones-page`, `.zones`, `.zones-side`, `.zones-draw`, `.zones-list`, `.zones-map`, `src/geofences/ZonesPage.tsx`).**
- Two-column grid at 1024px and up (`.zones` side 320px, map `1fr`). Stacks below 1024px.
- `.zones-draw` as a button group. `.zones-list` as bordered rows. `.zones-map` `min-height: 480px`.
- Tests: `ZonesPage.test.tsx` green (draw and save button names unchanged).

**G8 — Driver PWA stylesheet (new `apps/driver-pwa/src/styles.css`, `src/main.tsx`, `index.html`, `src/pages/TodayPage.tsx`).**
- `src/main.tsx`: add `import './styles.css'` after the existing imports (GF-358).
- `index.html`: change only the viewport meta `content` to `width=device-width, initial-scale=1.0, viewport-fit=cover` (GF-359).
- `TodayPage.tsx`: add `className` to the existing state span. Value `state-chip state-<job.state>`. This is the only class name the driver app gets (GF-359).
- Stylesheet content (mobile-first, GF-361):
  - Tokens on `:root` (GF-357 driver values).
  - `body` and `main`: system-ui stack; `main` padding uses `env(safe-area-inset-*)` on all four sides plus base spacing; `max-width: 32rem; margin: 0 auto` for tablets.
  - Headings `h1` and `h2` sizes; `header` as a sticky top row with the on-shift checkbox row.
  - `button`: min-height and min-width 44px, full-width for primary actions, `:disabled` state, `:focus-visible`.
  - `input`, `label`: min-height 44px, `font-size: 16px` (prevents iOS zoom).
  - Job list: `main ul > li` as cards (border, radius, padding, gap); the state chip uses `.state-chip` and `.state-<state>` per GF-357.
  - Banners: `p[role=status]` and `p[role=alert]` as full-width blocks (status = info tint, alert = danger tint). No class needed.
  - Deliver flow: `input[type=file]` styled as a large button via its label, the location readout as muted monospace-free text, the submit button full-width.
  - Key setup and login: single-column form, stacked fields, primary button full-width.
  - Conflict list (`ConflictList`, a `section` inside `main`): `section ul > li` cards with danger border.
- Tests: `npm --prefix apps/driver-pwa test` green (TodayPage `closest('li')` and listitem queries unchanged); typecheck; build with `check-pwa-build: ok`; grep check (GF-362) for `.state-chip`, `env(safe-area-inset`, `44px`.

**G9 — Validation pass (no code change).**
- Full web and driver suites, typechecks, and production builds (G0 commands).
- Dist rule-presence greps and byte sizes (GF-362), before and after.
- Scope fence, behavior fence, dependency fence, secret scan (Validation (f)–(j)).
- Append results to this spec under `## Validation results and deviations` (P24 convention).
- No commit, no push, no merge (GF-363). The orchestrator merges and deploys.

## Requirements

### Carried constraints (binding)

| ID | Requirement | Source |
|---|---|---|
| D-38 | Frontend libraries are pinned. No CSS framework, no utility library, no new runtime or dev dependency. Hand-written CSS only. | tech-stack §11 D-38 |
| R-5 | Do not change behavior the spec does not describe. No route, handler, query, or API change. | roadmap §1 R-5 |
| R-8 | `README.md` is never edited. | roadmap §1 R-8 |
| R-9 | No secrets in the diff. No env value written anywhere. | roadmap §1 R-9 |
| mission §7 | Any new SPEC-AMEND gets a ledger row. P25 adds none (see SPEC-AMEND repeat). | mission §7 rule |

### Design constraints (binding rows from the brief)

| ID | Constraint |
|---|---|
| DC-1 | Dispatcher is desktop-first. Design target 1280px; floor 1024px (GF-360). |
| DC-2 | Driver PWA is phone-first. Design target 375–430px. |
| DC-3 | Driver tap targets ≥ 44px (min-height and min-width). |
| DC-4 | Driver safe-area padding via `env(safe-area-inset-*)` (requires `viewport-fit=cover`, GF-359). |
| DC-5 | System-ui font stack. Gray/slate palette. One accent color (GF-357). |
| DC-6 | Visible `:focus-visible` on every interactive element. Visible `:disabled` state on every button. |
| DC-7 | No basemap tiles. The P9 decision stands. No tile layer, no tile URL, no style change to the MapLibre map. |
| DC-8 | Professional and calm. No decorative imagery, no gradients, no animations beyond a 150ms color transition. |
| DC-9 | Status is never signaled by color alone. Every chip and banner keeps its text label. |
| DC-10 | No deploy step. The orchestrator redeploys Pages after merge. |

### Verified-facts inventory

Web. Styled and unstyled are as in Baseline facts. Driver: no CSS, zero `className`, semantic selectors only.

### Decisions (GF-356…GF-364)

**GF-356 — CSS organization.** Web: extend the single `apps/web/src/styles.css` in commented sections (`/* tokens */`, `/* shared */`, `/* login */`, `/* jobs */`, and so on). No per-page CSS files. Driver: one `apps/driver-pwa/src/styles.css`. The MapLibre `display-*.css` chunk is not touched.
*Rationale:* the web app has one import in `main.tsx`, and a per-page split would add chunks and import wiring for about 10 KB of CSS. A single file keeps the tokens in one place and keeps `main.tsx` working unchanged.

**GF-357 — Design tokens.** Plain custom properties on `:root`. Web and driver each carry their own `:root` block (two apps, no shared package, no build tooling added). Values:

| Token | Value | Use |
|---|---|---|
| `--color-bg` | `#f8fafc` | page background (slate-50) |
| `--color-surface` | `#ffffff` | cards, panels, dropdown |
| `--color-border` | `#e2e8f0` | rules, card borders (slate-200) |
| `--color-border-strong` | `#cbd5e1` | input borders (slate-300) |
| `--color-text` | `#0f172a` | body text (slate-900) |
| `--color-text-muted` | `#64748b` | hints, labels, acked rows (slate-500; 4.6:1 on `#f8fafc`) |
| `--color-accent` | `#1d4ed8` | the one accent: primary buttons, links, focus ring, active nav (blue-700; 6.7:1 with white text) |
| `--color-accent-hover` | `#1e40af` | primary button hover |
| `--color-danger` | `#b91c1c` | error text, critical severity text (red-700) |
| `--color-danger-bg` | `#fef2f2` | error block background |
| `--color-warning-bg` | `#fef3c7` | existing `.banner` background, warning severity background |
| `--color-warning-text` | `#92400e` | warning text (amber-800) |
| `--color-disabled-bg` | `#e2e8f0` | disabled button background |
| `--color-disabled-text` | `#94a3b8` | disabled button text (slate-400) |
| `--space-1` … `--space-6` | `4px 8px 12px 16px 24px 32px` | spacing scale |
| `--text-sm` / `--text-md` / `--text-lg` / `--text-xl` | `0.875rem / 1rem / 1.125rem / 1.5rem` | type scale; body line-height 1.5 |
| `--font-sans` | `system-ui, -apple-system, "Segoe UI", Roboto, sans-serif` | the only family |
| `--radius` | `6px` | cards, inputs, buttons |
| `--shadow-card` | `0 2px 8px rgb(15 23 42 / 0.12)` | dropdown and vehicle card |
| focus ring | `2px solid var(--color-accent)`, `outline-offset: 2px` | every `:focus-visible` |

Job-state chips (background / text; the text label is always shown):

| State | Background | Text |
|---|---|---|
| `created` | `#f1f5f9` | `#334155` |
| `assigned` | `#e0f2fe` | `#075985` |
| `accepted` | `#ede9fe` | `#5b21b6` |
| `en_route` | `#fef3c7` | `#92400e` |
| `picked_up` | `#ffedd5` | `#9a3412` |
| `delivered` | `#dcfce7` | `#166534` |
| `failed` | `#fee2e2` | `#991b1b` |
| `cancelled` | `#e5e7eb` | `#374151` |

Alert severity chips: `info` `#e0f2fe` / `#075985`; `warning` `#fef3c7` / `#92400e`; `critical` `#fee2e2` / `#991b1b`.

*Rationale:* the state and severity hues are status signals, not the accent. The accent appears only on the list in the token table, so "one accent" holds. Each pair meets WCAG AA for normal text on its background. Status is never color-only (DC-9).

**GF-358 — Driver stylesheet mechanics.** Create `apps/driver-pwa/src/styles.css` and import it from `src/main.tsx` (`import './styles.css'`), the same pattern as the web app. Do not add a `<link>` in `index.html`.
*Rationale:* the import keeps `index.html` unchanged apart from the viewport meta (GF-359). Vite injects the built CSS link into `index.html`, and the driver's `vite-plugin-pwa` `workbox.globPatterns` already include `**/*.css`, so the stylesheet is precached and offline-ready. Verify precaching in G9 with a grep of `dist/sw.js` or `dist/assets/*.css` in the precache manifest. `check-pwa-build.mjs` is not changed. It checks only `index.html` string presence, which stays true.

**GF-359 — Markup-touch policy.** Binding for G1–G8.
- **Allowed:**
  1. Add a `className` attribute to an existing element. New names only. No rename or removal of an existing `className`.
  2. Add `className` to the driver job-state span in `TodayPage.tsx` (`state-chip state-<state>`), and to the web state element if G3 finds none.
  3. Add the one `import './styles.css'` line in driver `main.tsx`.
  4. Change the driver `index.html` viewport meta `content` to add `viewport-fit=cover`. Nothing else in that file changes.
- **Forbidden:**
  - Changing any tag name, adding or removing an element, or reordering children.
  - Adding a wrapper element. None is expected, because every layout is reachable by CSS on existing elements.
  - Changing text content, `aria-*`, `role`, `id`, `name`, `type`, `htmlFor`, `placeholder`, `href`, `disabled`, `hidden`, `data-testid`, props, or event handlers.
  - `display: contents` anywhere. `display: block` on `table`, `tr`, or `td`. Both drop table or list semantics in some browsers, so they are excluded.
- Pinned structure (from the test audit): table rows and cells in `JobsPage`, `AssignmentPanel`, and `ReportsPage`; `dl`, `dt`, `dd` in `VehicleCard`; `ul`, `li` in `TodayPage` and `ConflictList`. Styling for these is CSS on the existing elements.
- Mechanical enforcement: validation check (j) (every changed `.tsx` line is a `className` addition or the import line).

**GF-360 — Responsive min-width (dispatcher).** Design for 1280px. Floor 1024px. No mobile layout for the dispatcher.
- Below 1280px nothing is redesigned. Below 1024px the following apply, and nothing else changes:
  - `.topbar` wraps.
  - `.new-job` stacks (`flex-wrap`). Map `min-height` drops to 360px.
  - `.zones` stacks to one column.
  - `.jobs-page` and `.reports-page` get `overflow-x: auto`. Their tables keep `min-width` (960px and 720px), so the page scrolls horizontally rather than squeezing columns.
- One `@media (max-width: 1023px)` block in web `styles.css` holds all breakpoint rules.
- Phones are not a dispatcher target. Below 1024px the dispatcher is usable but not designed for.

**GF-361 — Driver mobile-first behavior.** Base styles target 375–430px with no media query needed for that range. A `@media (min-width: 640px)` block only centers the `main` column (`max-width: 32rem`) for tablets. Buttons use `min-height: 44px` (DC-3). Inputs use `font-size: 16px`, which prevents iOS zoom on focus. Full-width primary actions sit at the bottom of their form or card, within thumb reach. Safe-area padding (DC-4) applies to `main`.

**GF-362 — "Verified built CSS" method and byte accounting.** Done when all of these hold:
- The web dist CSS contains each class selector in the Scope list. Check: `grep -o '\.login\b' apps/web/dist/assets/index-*.css` (and `.jobs-page`, `.new-job-page`, `.reports-page`, `.alert-bell`, `.alert-dropdown`, `.alert-severity`, `.zones-page`, `:focus-visible`, `:disabled`) each exit 0.
- The driver dist contains a CSS asset: `ls apps/driver-pwa/dist/assets/*.css` exits 0, and the file contains `.state-chip`, `env(safe-area-inset`, and `44px`.
- Byte sizes before and after (`wc -c`): web index CSS (1258 B before), driver CSS (none before), MapLibre `display-*.css` (69808 B before; must be byte-identical after).
- Class names survive Vite's CSS minifier (it does not rename). G0 confirms with the baseline grep on `.topbar`, `.vehicle-card`.
- Results are recorded in the spec's validation section. Byte accounting is evidence, not a pass gate.
*Note:* grep proves rule presence, not appearance. Visual verification is a manual orchestrator step after deploy and is not a gate in this phase (no visual-regression tooling, D-38).

**GF-363 — No merge, no push; the orchestrator owns merge and deploy.** The brief's deliverable 6 ("merge `--no-ff` to main and `git push origin main`") and its pre-merge `make fmt-check lint test` are not done by the child. The child commits nothing unless the implementer is asked, pushes nothing, and merges nothing. The orchestrator merges `phase-25-ui-styling` into `main` with `--no-ff`, pushes, and redeploys both Pages projects (brief deliverable 6, reworded).
*Rationale:* the user's plan instruction is "no push". The merge and push are outward-facing, so they belong to the orchestrator.

**GF-364 — Gates, CLAUDE.md, and fence.** No Go file changes, so `make fmt-check lint test` is not run (the brief's deliverable 4 Go gate is excluded). Its replacement is validation (f), which proves that no Go path changed. CLAUDE.md is not changed (no env, endpoint, make target, or port change). `specs/constitution/**` and `README.md` are not changed. No `package.json` or lockfile changes.

## Validation

Runnable checks on this host, in order. Logs go to `deploy/.local/p25-validation/` (gitignored). Each check records its command, exit code, and an output tail.

(a) **Web suite green, unchanged tests.** `npm --prefix apps/web test` exit 0; `git diff --quiet main...HEAD -- 'apps/web/**/*.test.ts' 'apps/web/**/*.test.tsx'` exit 0.

(b) **Driver suite green, unchanged tests.** `npm --prefix apps/driver-pwa test` exit 0; the same test-file `git diff --quiet` check for `apps/driver-pwa`.

(c) **Typechecks.** `npm --prefix apps/web run typecheck` and `npm --prefix apps/driver-pwa run typecheck` exit 0.

(d) **Production builds with the PWA gate.** `npm --prefix apps/web run build` exit 0. `npm --prefix apps/driver-pwa run build` exit 0, last line `check-pwa-build: ok`.

(e) **Built CSS contains the new rules (GF-362).** Web: each class in the list in GF-362 greps in `apps/web/dist/assets/index-*.css`. Driver: `apps/driver-pwa/dist/assets/*.css` exists and contains `.state-chip`, `env(safe-area-inset`, `44px`. Driver precache: `grep -l 'styles' apps/driver-pwa/dist/sw.js` or the precache manifest lists the css asset.

(f) **Scope fence.** `git diff --name-only main...HEAD | grep -Ev '^(apps/web/src/|apps/driver-pwa/src/|apps/driver-pwa/index.html$|specs/features/25-ui-styling\.md$|prompts/)'` prints nothing.

(g) **Forbidden paths untouched.** `git diff --quiet main...HEAD -- README.md CLAUDE.md db internal services cmd deploy .github e2e specs/constitution apps/web/package.json apps/web/package-lock.json apps/driver-pwa/package.json apps/driver-pwa/package-lock.json apps/driver-pwa/scripts apps/web/functions apps/driver-pwa/functions` exit 0. (Covers the Go tree, dependencies, PWA check script, Pages Functions, and CLAUDE.md.)

(h) **Behavior fence.** `git diff -U0 main...HEAD -- 'apps/*/src/**/*.tsx' | grep '^[+-]' | grep -v '^+++\|^---' | grep -v 'className'` prints only the driver `main.tsx` import line (`import './styles.css'`). Every other changed `.tsx` line is a `className` addition. Tag, text, role, handler, and route changes make this non-empty.

(i) **Tag and structure fence.** `git diff -U0 main...HEAD -- 'apps/*/src/**/*.tsx' | grep -E '^-' | grep -v '^---'` prints only lines that carry an old `className` or the removed `className` value. Nothing else is removed. (Replaced lines are checked by (h).)

(j) **No new dependency, no secrets.** `git diff main...HEAD | grep -E 'postgres(ql)?://|neon\.tech|BEGIN [A-Z ]*PRIVATE KEY|(JWT_SECRET|DEVICE_KEY_SALT|MINIO_SECRET|MINIO_KEY|SEED_PASSWORD)='` prints nothing. Check (g) already covers the lockfiles.

(k) **Byte accounting.** `wc -c apps/web/dist/assets/index-*.css apps/web/dist/assets/display-*.css` and `wc -c apps/driver-pwa/dist/assets/*.css` after the build. Compare with the G0 baseline. `display-*.css` must match its baseline byte count exactly.

(l) **CLAUDE.md unchanged.** `wc -l CLAUDE.md` prints 197.

## Testing depth

| Layer | Scope | Tooling | Where | Check |
|---|---|---|---|---|
| Component and unit (web) | Existing 31 test files unchanged; green | Vitest + Testing Library (jsdom) | `apps/web/src` | (a) |
| Component and unit (driver) | Existing 29 test files unchanged; green | Vitest + Testing Library (jsdom) | `apps/driver-pwa/src` | (b) |
| Typecheck | Both apps | `tsc -b` | both apps | (c) |
| Build | Production builds; driver PWA artifact gate | `vite build`; `check-pwa-build.mjs` | both apps | (d) |
| Static (built CSS) | Rule presence by class and property; byte accounting | `grep -o`, `wc -c` | `dist/assets/*.css` | (e), (k) |
| Static (diff fences) | Scope, forbidden paths, markup-only changes, no secrets, no deps | `git diff`, `grep` | repo | (f)–(j) |
| Config | CLAUDE.md line budget | `wc -l` | `CLAUDE.md` | (l) |
| Visual regression | **Not run.** No new tool (D-38). Manual visual check is an orchestrator step after deploy | none | none | none |
| Go | **Not run.** No Go file changes (GF-364) | none | none | none |

Test flakiness policy (P23 precedent): an isolated re-run of the affected suite before failing anything.

## SPEC-AMEND repeat

**Expected: NO.** P25 adds no ledger row. A stylesheet is not a schema, env, endpoint, or dependency change. D-38 already pins the frontend libraries, and no CSS framework is among them. A new runtime or dev dependency would be a SPEC-AMEND row and is forbidden here regardless (GF-364, check (g)). No tech-stack §11 row is added, and no mission §7 row is added. The only ledger-adjacent records are the GF-356…GF-364 decisions in this spec.

## Out of scope

- Behavior, route, API, query, handler, or prop changes (R-5). Any markup change beyond GF-359's allowed list.
- New dependencies of any kind, including CSS frameworks, utility libraries, icon fonts, web fonts, and visual-regression tools (D-38).
- Changes to the MapLibre `display-*.css` chunk, map style, or map layers. No basemap tiles (DC-7).
- Service worker, manifest, and `check-pwa-build.mjs` changes beyond what the build produces. The script is not extended to check CSS.
- `README.md` edits (R-8), `specs/constitution/**` edits, `CLAUDE.md` edits, `roadmap.md` edits.
- Go-side changes, Go gates, and Go tests (GF-364).
- Pages Functions (`apps/*/functions`), Pages routing, and deployment (the orchestrator redeploys after merge).
- Merge and push (GF-363). `e2e/**` changes.
- Light or dark theme, dispatcher mobile layout, animations beyond the 150ms color transition, and any content beyond the brief.
- Driver `theme-color` meta and PWA icons (unchanged).

## Validation results and deviations

Run on 2026-10-29 on `phase-25-ui-styling`, host Node from the repo's `npm` scripts. Logs for each check are in `deploy/.local/p25-validation/` (gitignored). The G0 baseline ran before any change; the final run ran after the last code commit (`db825dd`).

### Check results

1. **G0 baseline (before any change).** `npm --prefix apps/web test`: exit 0 (18 files, 94 tests). `typecheck`: exit 0. `build`: exit 0. Driver `test`: exit 0 (19 files, 129 tests). Driver `typecheck`: exit 0. Driver `build`: exit 0, last line `check-pwa-build: ok`. Baseline CSS: web `index-yPgmLzVN.css` **1258 B**, `display-B2k4QVOw.css` **69808 B**, driver none (`ls dist/assets/*.css` matched 0 files).
2. **(a)** Web suite: `npm --prefix apps/web test` exit 0, 18 files, 94 tests. `git diff --quiet main...HEAD -- 'apps/web/**/*.test.ts' 'apps/web/**/*.test.tsx'`: exit 0 (no test file changed). The test count is 18 files, not the 31 this spec's Testing depth section states. See D1.
3. **(b)** Driver suite: `npm --prefix apps/driver-pwa test` exit 0, 19 files, 129 tests. The same `git diff --quiet` check for the driver tests: exit 0. The count is 19 files, not 29. See D1.
4. **(c)** `npm --prefix apps/web run typecheck` exit 0. `npm --prefix apps/driver-pwa run typecheck` exit 0.
5. **(d)** `npm --prefix apps/web run build` exit 0. `npm --prefix apps/driver-pwa run build` exit 0, last line `check-pwa-build: ok`.
6. **(e) Built CSS, web.** `grep -o` on `dist/assets/index-DFChPy_F.css` finds `.login`, `.jobs-page`, `.new-job-page`, `.reports-page`, `.alert-bell`, `.alert-dropdown`, `.alert-severity`, `.zones-page`, `:focus-visible`, `:disabled`, plus `.topbar`, `.vehicle-card` (G0 class-survival check) and `.state-chip`. Each matched.
   **Built CSS, driver.** `dist/assets/index-W-_P_XWO.css` exists and contains `.state-chip`, `env(safe-area-inset`, and `44px`. **Precache:** `dist/sw.js` lists `assets/index-W-_P_XWO.css` (grep match in `sw.js`).
7. **(f) Scope fence.** `git diff --name-only main...HEAD | grep -Ev '<fence>'`: empty. The changed paths are `apps/driver-pwa/{index.html,src/main.tsx,src/pages/TodayPage.tsx,src/styles.css}`, `apps/web/src/{styles.css,jobs/JobsPage.tsx,jobs/AssignmentPanel.tsx,geofences/ZonesPage.tsx}`, and `specs/features/25-ui-styling.md`.
8. **(g) Forbidden paths.** `git diff --quiet main...HEAD -- README.md CLAUDE.md db internal services cmd deploy .github e2e specs/constitution apps/web/package.json apps/web/package-lock.json apps/driver-pwa/package.json apps/driver-pwa/package-lock.json apps/driver-pwa/scripts apps/web/functions apps/driver-pwa/functions`: exit 0. No dependency change, no lockfile change, no `MapView.tsx` change.
9. **(h) Behavior fence.** The added (`+`) `.tsx` lines that do not contain `className`, over `apps/*/src/*.tsx` and `apps/*/src/**/*.tsx`, are exactly one line: `+import './styles.css'` (driver `main.tsx`). The literal command also prints the `-` lines of the three replaced elements (below, check (i)); this is expected. The glob `apps/*/src/**/*.tsx` does not match files directly under `src/`, so the check was widened to include `apps/*/src/*.tsx`. See D4.
10. **(i) Tag and structure fence.** Removed (`-`) `.tsx` lines are three, each replaced by a line that adds a `className`: `<span>{STATE_LABELS…}</span>` (TodayPage), `<button … onClick={save}>` (ZonesPage Save zone), `<td>{j.state}</td>` (JobsPage). Nothing else was removed. Tag names, text, `aria-*`, `role`, handlers and props are unchanged. The one `AssignmentPanel` change is `+className="primary"` on the Assign button, which adds a line and removes none.
11. **(j) Secrets and dependencies.** `git diff main...HEAD | grep -E 'postgres(ql)?://|neon\.tech|BEGIN [A-Z ]*PRIVATE KEY|(JWT_SECRET|DEVICE_KEY_SALT|MINIO_SECRET|MINIO_KEY|SEED_PASSWORD)='`: empty.
12. **(k) Byte accounting.**

    | Asset | Before | After |
    |---|---|---|
    | web `index-*.css` | 1258 B (`index-yPgmLzVN`) | 13388 B (`index-DFChPy_F`) |
    | web `display-*.css` (MapLibre) | 69808 B | **69808 B** (byte-identical count; hash `display-B2k4QVOw` unchanged) |
    | driver `index-*.css` | none | 6132 B (`index-W-_P_XWO`) |

13. **(l) CLAUDE.md.** `wc -l CLAUDE.md`: **197**. Unchanged.

### Deviations

- **D1 (spec count).** Testing depth and check (a)/(b) say 31 web and 29 driver test files. The repo has 18 web and 19 driver `*.test.ts(x)` files (`find ... -name "*.test.ts*" | wc -l`). The counts were wrong in the spec. The check that matters is the unchanged-test `git diff --quiet`, which passes. No test was edited.
- **D2 (no active-nav highlight).** The topbar links are plain `Link`s with no `aria-current` and no `NavLink`. An active-item style would need a new attribute or a wrapper, both forbidden by GF-359. The topbar links use the accent colour only.
- **D3 (no pick-map hint on the new-job page).** The Plan asks for a `.hint` under the map. `NewJobPage` has no element there, and adding a `<p className="hint">` adds an element (GF-359). Not added. The clear buttons, form layout and map split are styled.
- **D4 (diff glob).** Check (h) as written with `apps/*/src/**/*.tsx` does not match `apps/driver-pwa/src/main.tsx` (root of `src/`). The fence was rerun with `apps/*/src/*.tsx` as well. Result: the only non-className added line is the import. Recorded so the next run does not miss the file.
- **D5 (no location readout on the deliver page).** The Plan asks for "the location readout" styling. `DeliverPage.tsx` gets the position (`geolocation`, `lat`/`lng`) and shows only its error notice. It renders no readout element, so no readout class exists. Not added, same reason as D3.
- **D6 (state cell is a `td`).** The web state element is `<td>{j.state}</td>` (no span exists). GF-359 item 2 allows a class on the web state element, so `className` went on the `td`. The chip is therefore a coloured cell, not a pill. The label stays visible (DC-9).
- **D7 (`.primary` class).** The Plan says primary buttons use the accent. The base `button[type=submit]` rule covers the forms. Two non-submit buttons, Save zone (`ZonesPage`) and Assign (`AssignmentPanel`), got `className="primary"` (GF-359 item 1, a new name). This is the only markup addition beyond the Plan's list.
- **D8 (extra rules not in the Plan).** Driver: `button` `transition: none` under `prefers-reduced-motion: reduce` (DC-8 keeps motion to 150ms; this removes it when the user opts out). Web: the `:has(> .alert-acked)` muted-row rule uses `:has()`, which needs a current browser. Chip, row-hover and alert-severity hex values are literals from GF-357 (row hover `#f1f5f9` is slate-100, not a token). Recorded, not changed.
- **D9 (topbar wrap).** `.topbar { flex-wrap: wrap }` is in the base rules, not the `max-width: 1023px` block, so it wraps at every width. This meets the "wraps below 1024px" requirement and does not change the desktop layout at 1280px.
- **D10 (commit grouping).** The login (G2) and jobs (G3) CSS were both written into `styles.css` before committing. They were split into two commits (`1f38228`, `03ac25b`) by writing the G2-only file state, so each group has its own commit. The G6 `!important` fix was amended into `8ad0f6e` before the branch was shared.
- **D11 (untracked files).** `prompts/46-p25-plan.md`, `prompts/47-p25-persist.md` and `prompts/48-p25-implement.md` were untracked when the session started. They are the user's and were left untouched. The tree is clean apart from them. Scope check (f) allows `prompts/`.
- **D12 (not run).** Visual check and the manual orchestrator step (GF-362 note): not run. No visual-regression tooling (D-38). Rule presence is proven by grep, not appearance.

### Commits on `phase-25-ui-styling`

```
b48b4fb Add P25 feature spec                       (pre-existing)
c932fcb Style web tokens and shared elements        (G1)
1f38228 Style login page                            (G2)
03ac25b Style jobs page, state chips and assignment panel   (G3)
954cda2 Style new job page form and map split       (G4)
97370b1 Style reports page controls and table       (G5)
8ad0f6e Style alert bell badge, dropdown and severity chips (G6)
8a3712e Style zones page panels and zone list       (G7)
9ca9df1 Style assign button and dispatcher layout below 1024px (G7/G8 web)
db825dd Style driver PWA mobile layout and import stylesheet   (G8)
```

Merge, push and Pages deployment are not done here (GF-363). This record is committed by the child.
