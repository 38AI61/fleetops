# P5 implement — batch writer core (auto-mode pointer)

Goal: implement roadmap phase P5 exactly per the committed feature spec.

Context: read `specs/features/05-batch-writer.md` — it is the complete instruction (plan task groups 1–10, requirements GF-68…GF-84, validation items 1–14). Also read `specs/constitution/roadmap.md` §1 (R-1…R-9) and §3 P5, `specs/constitution/tech-stack.md` §6.2 (positions schema), §8 (messaging context), §9 (testing), §11 (D-14, D-15, D-18, D-19, D-22), `AGENTS.md`, `CLAUDE.md`. Those files are the source of truth; this prompt only points.

Method: work the task groups in order (1 → 10). Commit after each task group with an imperative message (small scoped commits; tests live beside code). The partition helper (group 3) may be committed together with the buffer (group 4) if you prefer fewer commits — never fold unrelated groups into one commit.

Research: if behavior of a library (pgregory.net/rapid check API, lib/pq array params, testcontainers PostGIS) is unclear, use a subagent to investigate and report a summary only — never read long docs into the main context.

Host facts: Go at `/root/.local/go/bin/go`, golangci-lint at `/root/.local/bin/golangci-lint` (the Makefile wires them via `deploy/.local/tools.mk`); migrate CLI at `/root/go/bin/migrate`; Docker available for testcontainers (card-free pulls; `postgis/postgis:16-3.4` is already cached). `make test` runs `-race`; the P5 tests need only PostGIS containers, never `docker compose up`. Tests may take minutes: run them with generous timeouts, one package at a time.

Done when: every Validation item (1–14) in the feature spec passes, run by you on this host, with the command and its outcome quoted in your report. Fix failures before finishing; if a spec item proves wrong or ambiguous, stop that item and report it — do not silently deviate.

Out of scope: anything not in the phase spec (no P6+ work — no JetStream consumer, no ack/NAK, no partition ensure-ahead, no retention, no metrics, no Redis, no presence, no WS, no position.batch publish, no compose changes, no MinIO; no README edits, no `deploy/PORTS.md` edits; no merge to main, no push). The `CLAUDE.md` update in task group 9 is in scope (mechanical facts only: the three FLEETOPS_* tunables, keep < 200 lines). No new env names beyond the three FLEETOPS_* names. No new Go module dependency.

Report: files per task group, every validation command + its output, final `git log --oneline` for the branch, and any deviations flagged explicitly.