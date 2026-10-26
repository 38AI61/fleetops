# P8 implement — map read API & GPS simulator (auto-mode pointer)

Goal: implement roadmap phase P8 exactly per the committed feature spec.

Context: read `specs/features/08-map-api-simulator.md` — it is the complete instruction (task groups G0–G6, requirements GF-126…GF-151, validation items 1–14). Also read `specs/constitution/roadmap.md` §1 (R-1…R-9) and §3 P8, `specs/constitution/tech-stack.md` §3 (repo layout — logic in `internal/`, `tools/` holds cmd binaries only), §5 (env names + tunables — `FLEETOPS_PRESENCE_TTL` exists; no new env names), §6 (data model — `vehicles_latest`, migrations 001–007), §7.1 (endpoint table), §9 (testing), §11 (D-05, D-12, D-27, D-40, GF-5, GF-8, GF-12, GF-13), `specs/features/07-fanout-ws.md` (P7 decisions your code builds on: GF-101 Redis payloads, GF-104 presence write path, GF-107 `FLEETOPS_PRESENCE_TTL` semantics), `AGENTS.md`, `CLAUDE.md`. Those files are the source of truth; this prompt only points.

Method: work the task groups in order (G0 → G6). Commit after each task group with an imperative message (small scoped commits; tests live beside code). Never fold unrelated groups into one commit. The spec's test names are binding. Commit with `git add <paths> && git commit -m "<imperative message>"` — never fold prompts/ or specs/ into implementation commits (they are already committed on this branch).

Research: if library behavior is unclear (PostGIS `ST_MakeEnvelope`/`ST_Intersects` on geography, `math/rand/v2` PCG seeding, rapid property-test helpers, Echo query binding, `EXPLAIN` via lib/pq), use a subagent to investigate and report a summary only — never read long docs into the main context.

Host facts: Go at `/root/.local/go/bin/go` and golangci-lint at `/root/.local/bin/golangci-lint` are already wired via `deploy/.local/tools.mk` (gitignored); migrate CLI at `/root/go/bin/migrate`. Docker is available for testcontainers (card-free pulls; `postgis/postgis:16-3.4` and `redis:7` are already cached). `make test` runs `-race`. Integration tests may take minutes: run them with generous timeouts, one package at a time. Compose is NOT started for tests — testcontainers only.

Done when: every Validation item (1–14) in the feature spec passes, run by you on this host, with the command and its outcome quoted in your report. Fix failures before finishing; if a spec item proves wrong or ambiguous, stop that item and report it — do not silently deviate.

Out of scope: anything not in the phase spec (no P9 UI; no P10 work — no jobs table, no job endpoints, the real `JobLookup` stays unwired; no P22 metrics; no k6; no migrations — `db/` untouched; no README, `deploy/`, or `specs/constitution/` edits; CLAUDE.md only per G6 mechanical rows GF-151; no merge to main, no push; never write any plaintext device key outside `deploy/.local/` test-scoped temp files).

Report: files per task group, every validation command + its output, final `git log --oneline` for the branch, and any deviations flagged explicitly.