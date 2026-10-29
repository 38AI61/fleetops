# Phase P25 — UI styling pass (web + driver PWA) — CHILD BRIEF

## Mission
The deployed app is functional but visually unstyled on most screens: the web app's
`src/styles.css` has 113 lines covering only the map page, while login, jobs, new-job,
reports, and the alert bell use class names that have no CSS at all, and the driver PWA
ships with NO stylesheet. This phase completes the visual layer for both apps WITHOUT
changing any behavior. Musa saw the bare login page on the live deployment — this is the
fix. Keep the pinned decision D-38 (no UI framework, plain CSS) — hand-write the styles.

## Repo & standards
- Repo: /root/fullstack-portfolio/01-fleetops. Worktree + branch `phase-25-ui-styling`
  off main (c599bb3). Standard SDD loop (plan → implement → persist) via fresh
  `claude -p` sessions, pointer prompts in prompts/ (46-p25-plan.md, 47-p25-persist.md,
  48-p25-implement.md), spend JSONs to deploy/.local/p25-<step>.json.
- Consent-free: single-purpose terminal calls, no pipes to interpreters, no set -e
  bundles, no curl|sh. Tests before merge; `make fmt-check lint test` must pass.
- D-38 stays: plain CSS, no framework, no new dependencies. SPEC-AMEND if any
  constitution row needs it (list them; get the wording right the first time).

## Verified facts (from my own reads)
- Web app classes USED but UNSTYLED: login, jobs-page, new-job-page, reports-page,
  reports-controls, alert-bell, alert-dropdown, alert-badge, alert-kind, alert-acked,
  alert-error, card-head, title, hint, error. STYLED already: map-page, map-canvas,
  topbar, banner, new-job, new-job-form, new-job-map, note, stale-badge, vehicle-card,
  ws-badge.
- styles.css is imported once in src/main.tsx. The login page component is
  src/auth/LoginPage.tsx (`<main className="login">`). Pages: src/jobs/JobsPage.tsx,
  src/jobs/NewJobPage.tsx, src/reports/ReportsPage.tsx, src/alerts/AlertBell.tsx,
  src/geofences/ZonesPage.tsx, src/map/MapPage.tsx.
- Driver PWA: apps/driver-pwa/src — pages LoginPage, TodayPage, DeliverPage,
  KeySetupPage; NO css file anywhere; index.html has no stylesheet link. It needs a
  stylesheet created + imported from main.tsx (or index.html link).
- Web build: `npm --prefix apps/web run build` → dist/assets/index-*.css. Driver build:
  same pattern. Pages Functions live in apps/*/functions — NOT affected by styling.
- Design constraints: dispatcher is desktop-first (1280+), driver PWA is phone-first
  (375–430px viewport, thumb-reachable buttons, offline-friendly). No basemap tiles
  (P9 decision). Keep it professional and calm: system-ui font stack, gray/slate
  palette, one accent color, adequate spacing, visible focus states, disabled states.
- Deployment: after merge I (orchestrator) redeploy both Pages projects and verify in
  a real browser — the child does NOT deploy, but MUST leave the builds green.

## Deliverables
1. specs/features/25-ui-styling.md — scope fence (CSS + minimal className/markup
   adjustments only; NO behavior, NO route, NO API changes), design tokens (colors,
   spacing, type scale), per-page breakdown, validation.
2. Web app: complete stylesheet(s). Either extend src/styles.css or split per-page CSS
   files imported by each page — decide in plan, keep D-38. Cover: login (centered
   card, branded header), topbar/nav polish, jobs list (table, filter bar, state
   chips, nearest-vehicles panel, assign button states), new-job (form layout,
   pickers, map integration), reports (controls, table, CSV button), alert bell
   (badge, dropdown, kind labels, acked state), zones page polish, generic
   .error/.hint/.title, focus-visible, disabled, responsive min-width behavior.
3. Driver PWA: create a stylesheet (src/styles.css + import), mobile-first, cover:
   login, today list (job cards with state chips, big action buttons), deliver flow
   (photo capture button, location readout, offline banner), key setup page, status
   banners. Thumb-sized tap targets (min 44px), safe-area padding.
4. Snapshot/behavior tests still green: `make fmt-check lint test` (jsdom does not
   assert styles — verify no test asserts on absent styles), `npm --prefix apps/web
   run build`, `npm --prefix apps/driver-pwa run build`, both apps' test suites.
5. Visual smoke: build both apps and confirm the built CSS actually contains the new
   rules (grep for .login, .jobs-page, .alert-bell in dist assets). Record byte sizes
   before/after.
6. Merge to main (--no-ff) and git push origin main.

## Budget & reporting
- Cap each claude -p step at --max-budget-usd 2.5; log deploy/.local/p25-<step>.json.
- Final summary: files changed, CSS size before/after, test results, commit hashes,
  total spend.