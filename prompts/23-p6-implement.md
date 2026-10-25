# P6 implement — ingest consumer & partitions (auto-mode pointer)

Goal: implement roadmap phase P6 exactly per the committed feature spec.

Context: read `specs/features/06-consumer-partitions.md` — it is the complete instruction (task groups G0–G7, requirements GF-86…GF-99, validation items 1–8). Also read `specs/constitution/roadmap.md` §1 (R-1…R-9) and §3 P6, `specs/constitution/tech-stack.md` §3 (repo layout: `services/ingest/` is a pinned Go service), §8 (messaging — §8.2 JetStream config), §9 (testing), §11 (D-14, D-16), `specs/features/05-batch-writer.md` (the P5 decisions your code builds on: GF-70…GF-84), `AGENTS.md`, `CLAUDE.md`. Those files are the source of truth; this prompt only points.

Method: work the task groups in order (G0 → G7). Commit after each task group with an imperative message (small scoped commits; tests live beside code). Never fold unrelated groups into one commit. `services/api` is untouched in P6 — the consumer binary is `services/ingest` (GF-94).

Research: if behavior of a library (nats.go jetstream consumer/fetch/NakWithDelay/Term APIs, prometheus client_golang registration, lib/pq error codes, testcontainers) is unclear, use a subagent to investigate and report a summary only — never read long docs into the main context.

Host facts: Go at `/root/.local/go/bin/go`, golangci-lint at `/root/.local/bin/golangci-lint` (the Makefile wires them via `deploy/.local/tools.mk`); migrate CLI at `/root/go/bin/migrate`; Docker available for testcontainers (card-free pulls; `postgis/postgis:16-3.4` and `nats:2` are already cached). `make test` runs `-race`; the P6 tests need PostGIS and NATS JetStream containers, never `docker compose up`. Tests may take minutes: run them with generous timeouts, one package at a time.

Done when: every Validation item (1–8) in the feature spec passes, run by you on this host, with the command and its outcome quoted in your report. Fix failures before finishing; if a spec item proves wrong or ambiguous, stop that item and report it — do not silently deviate.

Out of scope: anything not in the phase spec (no P7 work — no Redis, no presence, no WS, no `position.batch` publish, `FlushResult.Stored` untouched as a seam; no P22 work — no `/metrics` route, no dashboards, no compose, no port binding; no gateway change, GF-97 stands; no README edits, no `deploy/PORTS.md` edits; no migration; no merge to main, no push). The CLAUDE.md change in GF-94 is in scope (mechanical facts only: the `go run ./services/ingest` local-run line, keep < 200 lines). No new env names. The only new Go module dependency is `github.com/prometheus/client_golang`.

Report: files per task group, every validation command + its output, final `git log --oneline` for the branch, and any deviations flagged explicitly.