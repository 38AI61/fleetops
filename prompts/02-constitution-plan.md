# Constitution — plan (plan mode)

Goal: prepare, do not execute, the plan for this repo's constitution: `specs/constitution/{mission.md, tech-stack.md, roadmap.md}`. Do NOT write any spec files in this run — produce the complete plan content only (plan mode persists it to your plan file automatically).

Context (read before planning):
- `README.md` (repo root) is the binding product spec. Derive everything strictly from §1–§9. Do not deviate from the pinned stack (§4) or architecture (§3).
- `deploy/PORTS.md` + `deploy/docker-compose.yml` already exist (Phase 0 scaffold done): host ports 10002–10005 active, 10006/10007 + 10010–10013 reserved, range 10000–10019. The roadmap must stay consistent with this allocation.
- Testing quality standard (binding): behavior-verifying tests, not test-count theater; depth follows the §9 NFR table — property tests on the signature problems (batch upsert / flush semantics / idempotent transitions), testcontainers integration on the ingest + job paths, one Playwright happy-path E2E, k6 load in the harden phase. Roadmap phases must name their testing depth.
- SDD layout: mission.md = vision, audience, scope (§1–§2). tech-stack.md = dev + deploy tech, constraints, testing preferences (§3–§4, §12 env, §15 deferred). roadmap.md = ordered SMALL phases, each one feature loop with: scope summary, validation approach, testing depth. MVP scope (§2.1) decomposes across phases; §2.2 stretch is explicitly excluded from the MVP roadmap (may appear as an unscheduled backlog section); §2.3 out of scope.

Known ambiguity areas to resolve as decision IDs (D-01, D-02, …) — each decision gets a short rationale: users/identity storage for §2.1 auth (README §5 core tables have no users table), first-fleet bootstrap, MinIO placement vs the Phase 0 compose skeleton (photo proof is MVP), alert rule configuration storage (§5 has an alerts table but no rules table), refresh-token storage, OSRM data provenance + where preprocessing happens, email stub boundary, simulator phase placement (§10 puts it in phase 2), CI + golang-migrate placement (§10 phase 0 mentions them; the Phase 0 scaffold deliberately excluded them — they belong to an early implementation phase), monthly partition retention mechanics, WS room cap / presence TTL as config, device API key auth for `/ingest/position`. Find further ambiguities yourself; resolve every one; zero open decisions.

Plan must contain:
1. Outline of each of the three files (section headings + what goes in each, one line per section).
2. The roadmap phase list in build order: each phase = title, one-sentence scope, validation approach, testing depth. Phases small enough for one feature branch each. Phase numbering may follow README §10 but the decomposition is yours — keep phases small.
3. The complete decision-ID table (ID, decision, one-line rationale).
4. Self-gate checklist items you will apply against README §1–§9.

Out of scope: writing spec files, code, git, compose changes.
Report: the complete plan content.