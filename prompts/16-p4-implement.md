# P4 implement — position gateway (auto-mode pointer)

Goal: implement roadmap phase P4 exactly per the committed feature spec.

Context: read `specs/features/04-position-gateway.md` — it is the complete instruction (plan task groups 1–13, requirements GF-52…GF-67, validation items 1–10). Also read `specs/constitution/roadmap.md` §1 (R-1…R-9) and §3 P4, `specs/constitution/tech-stack.md` §6 (schema), §7.1, §8 (messaging, D-16 stream config), §9 (testing), §11 (D-15, D-16, D-18, D-29, GF-12), `AGENTS.md`, `CLAUDE.md`. Those files are the source of truth; this prompt only points.

Method: work the task groups in order (1 → 13). Commit after each task group with an imperative message (small scoped commits; tests live beside code). Compose changes are limited to exactly what GF-62 names (`-js` on the nats service, postgres image tag `16` → `16-3.4`); never run `docker compose up`. No new migration; `db/` untouched.

Research: if behavior of a library (nats.go `jetstream.PublishAsync` and `WithPublishAsyncMaxPending`, the testcontainers NATS module, `github.com/google/uuid` `NewV7`) is unclear, use a subagent to investigate and report a summary only — never read long docs into the main context.

Host facts: Go at `/root/.local/go/bin/go`, golangci-lint at `/root/.local/bin/golangci-lint` (Makefile wires them via `deploy/.local/tools.mk`); migrate CLI at `/root/go/bin/migrate`; Docker available for testcontainers (card-free pulls; `nats:2` may need pulling — `docker pull nats:2` is fine). `make test` runs `-race`; the ingest integration tests need PostGIS and NATS containers.

Done when: every Validation item (1–10) in the feature spec passes, run by you on this host, with the command and its outcome quoted in your report. Fix failures before finishing; if a spec item proves wrong or ambiguous, stop that item and report it — do not silently deviate.

Out of scope: anything not in the phase spec (no P5+ work — no buffer flush, no DB writes, no `positions`, no `vehicles_latest`, no partitions, no consumer, no presence, no fanout, no WS, no map, no simulator, no MinIO, no metrics; no `FLEETOPS_*` env; no README edits, no `deploy/PORTS.md` edits, no MinIO, no Redis changes, no merge to main, no push). The `CLAUDE.md` update in task group 12 is in scope (mechanical facts only, keep < 200 lines).

Report: files per task group, every validation command + its output, final `git log --oneline` for the branch, and any deviations flagged explicitly.