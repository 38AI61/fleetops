# P25 implement — UI styling pass (web dispatcher + driver PWA)

The binding constitution and the feature spec are the complete instruction. Read them first and follow exactly:

- `CLAUDE.md` (mechanical rows: build/test/typecheck commands per app; stays under 200 lines) and `AGENTS.md`
- `specs/constitution/roadmap.md` §1 (R-1…R-9)
- `specs/constitution/tech-stack.md` §11 **D-38** (frontend pins — NO CSS framework, no new dependency)
- `specs/constitution/mission.md` §7 (SPEC-AMEND rule)
- `specs/features/25-ui-styling.md` — the feature spec: Scope, Plan (task groups in order), Requirements (D-38 verbatim + GF-356… with rationales), Validation (checks (a)…), Testing depth, SPEC-AMEND repeat, Out of scope.

Execution rules:

1. Work on the current branch `phase-25-ui-styling` only. Do not merge, do not push, do not touch `main`.
2. Implement the task groups in the spec's Plan order. Commit per task group with an imperative message in the repo's style (`Add …`, `Style …`, `Fix …`). The spec file's `## Validation results and deviations` section is appended at the end and committed (the P24 spec's results section is the format precedent — read its tail).
3. Scope fence (from the spec's Validation section): only `apps/web/src/**`, `apps/driver-pwa/src/**`, `apps/driver-pwa/index.html`, `specs/features/25-ui-styling.md`, and CLAUDE.md ONLY if the spec explicitly pins a mechanical row change. No behavior, route, API, handler, query, aria-attribute or visible-text changes. No new dependency in either app (D-38) — `package.json`/lockfiles must not change. Never touch `apps/web/src/map/MapView.tsx` or the MapLibre `display-*.css` chunk. README is never edited (R-8).
4. jsdom tests must stay green UNCHANGED: before changing any markup, check how the existing tests select elements (the spec's Requirements record the inventory); if a styling need would break a test selector, prefer CSS-only or choose a different approach rather than editing tests. If a test edit is genuinely unavoidable, it must be recorded as a deviation in the validation results with the reason.
5. Web dispatcher styling: cover per the spec — design tokens, shared elements (topbar/nav, `.title`, `.hint`, `.error`, buttons, table base, `:focus-visible`, disabled), login (centered card, branded header), jobs page (table, filter bar, state chips, assignment panel, Select-button states), new-job page (form layout, pickers, map integration), reports page (controls, table, CSV link/button), alert bell (badge, dropdown, kind labels, severity chips `alert-info/warning/critical`, acked state), zones page, responsive min-width behavior below 1280px.
6. Driver PWA styling: create the stylesheet + import exactly as the spec's decision pins (import from `src/main.tsx` or `<link>` in `index.html`). Mobile-first for 375–430px, thumb-sized tap targets ≥ 44px, safe-area padding via `env(safe-area-inset-*)`, cover login, today list (job cards, state chips, action buttons), deliver flow (photo capture, location readout, offline/queued/syncing banners), key setup page, status banners. The `check-pwa-build` gate must stay green.
7. Validation: run every check in the spec's Validation section yourself. Append the results with command outputs and byte sizes (web index css before 1258 B / after measured; driver PWA before: none / after measured) to `specs/features/25-ui-styling.md` under `## Validation results and deviations`. Records must be honest.
8. Done when every validation check passes or is recorded with its standing reason, both test suites are green, both builds are green, the dist rule-presence checks pass (grep `.login`, `.jobs-page`, `.new-job-page`, `.reports-page`, `.alert-bell`, `.zones-page` in `apps/web/dist/assets/*.css`; a css asset exists in `apps/driver-pwa/dist/assets/` with its rules), and the tree is clean.

Out of scope: merge, push, Pages deployment (the orchestrator deploys after merge), README, migrations, Go-side code, `.github/**`, `deploy/**`, `e2e/**`, `specs/constitution/**` edits, new dependencies, behavior changes.

Report (final message): files per task group, every command you ran with its outcome, the byte-size before/after numbers, the git log of the branch, and any deviations.