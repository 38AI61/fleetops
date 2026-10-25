# P1 — Toolchain, CI, migrations baseline (plan mode)

Goal: prepare, do not execute, the plan for roadmap **P1 — Toolchain, CI, migrations baseline**. Do NOT write any repo file in this run — produce the complete plan content only (plan mode persists it to your plan file automatically).

Context (read before planning):
- `specs/constitution/roadmap.md` §3 **P1** is the binding scope/validation/testing definition. `specs/constitution/tech-stack.md` is binding for pins: §2 added libraries (golang-migrate D-13, golangci-lint D-13, testcontainers-go D-37, pgregory.net/rapid D-37), §3 repo layout (`db/migrations/`, Makefile targets `migrate`/`test`/`lint`/`seed`/`seed-demo`, `make migrate` never at app boot), §9 testing preferences + "CI gates (P1)" paragraph, §11 decision D-13. `specs/constitution/mission.md` §7 for SPEC-AMEND rules. `README.md` §9/§10/§12 as background. `AGENTS.md` + `CLAUDE.md` rules apply.
- Phase 0 is done and committed: monorepo layout dirs exist only as compose skeleton (`deploy/`), no Go module yet, no `services/*/` code, no `db/`, no Makefile. This phase creates the first Go code in the repo.
- Toolchain reality on this host (use exactly these paths; none are on PATH):
  - `/root/.local/go/bin/go` (go1.27.3); GOCACHE/GOMODCACHE defaults are fine.
  - `/root/.local/bin/golangci-lint` (v2.15.0).
  - `/root/.local/bin/migrate` (golang-migrate v4.18.3, CLI built from source, reports version "dev").
  - docker 29.1.3 + compose v2; `docker pull` runs without prompts; testcontainers is fine.
  - Image tag fact: `postgis/postgis:16` does NOT exist on Docker Hub (README §12 pins a non-existent tag). Real tags are compound, e.g. `16-3.4` (verified pullable). The testcontainers helper must use `postgis/postgis:16-3.4`. Record this as a decision ID (tag correction; compose fix lands later in the phase that needs compose changes — do NOT edit compose in P1).
- CI reality: the repo is local-only and `gh` is unauthenticated, so GitHub Actions workflows are committed but cannot run remotely yet. The SAME gates must be validated locally in this phase: `/root/.local/go/bin/gofmt -l` empty, `/root/.local/bin/golangci-lint run` exit 0, `/root/.local/go/bin/go test ./... -race`, and the migration round-trip. The workflow files must mirror these local gates.
- Feature-spec layout (D-40): ONE file per phase at `specs/features/01-toolchain-ci-migrations.md` containing: scope, plan/task groups in build order (each task group with its tests), requirements with decision IDs, validation checks (runnable, explicit pass conditions), testing depth.
- Keep `CLAUDE.md` < 200 lines and mechanical (P1 introduces `make` targets + tool paths — updating CLAUDE.md with those facts in the implement phase is in scope).

Ambiguities to resolve as decision IDs (GF-n continues the gap-fill series; each with rationale): Makefile `seed`/`seed-demo` targets are named in tech-stack §3 but their backing tools (`tools/seed`, demo seed) arrive in P2/P23 — decide target timing; migration versioning scheme + dirty-state handling; whether `make migrate` wraps the migrate CLI directly or a small wrapper; testcontainers helper package location (`internal/testinfra/` vs `tools/`); lint config set (which linters enabled for an almost-empty module); Go module path/name. Find further ambiguities yourself; resolve every one; zero open decisions.

Plan must contain:
1. Numbered task groups in build order, each with: files to create, what they contain, and the tests for that group. Suggested shape (adjust as you see fit): go.mod/module bootstrap → shared testcontainers helper + smoke migration round-trip test → baseline migrations (001: `postgis` extension + `fleets`) → Makefile → golangci-lint config → GitHub Actions workflows → CLAUDE.md update. Every task group MUST list its tests.
2. Requirements section: binding decisions with IDs (D-13, D-37 reused where they apply; new GF-n only for genuinely new decisions).
3. Validation section: the runnable checks with explicit pass conditions (local gates listed above + migration up→down→up on a PostGIS testcontainer + `make` targets demonstrably work).
4. Testing depth: smoke per roadmap P1 (migration round-trip test; helper exercised by it).
5. Self-gate checklist you will apply (completeness vs roadmap P1 scope sentence, honest scope — nothing from P2+, every task group has tests, validation items all runnable).

Out of scope: any P2+ behavior (auth, users, seed logic, endpoints, NATS/Redis app code), compose edits, MinIO, README edits, OSRM data, git operations beyond what implement does later.

Report: the complete plan content.