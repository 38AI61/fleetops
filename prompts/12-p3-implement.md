# P3 implement — fleet registry & pairing (auto-mode pointer)

Goal: implement roadmap phase P3 exactly per the committed feature spec.

Context: read `specs/features/03-registry-pairing.md` — it is the complete instruction (plan task groups 1–15, requirements GF-30…GF-51, validation items 1–14). Also read `specs/constitution/roadmap.md` §1 (R-1…R-9) and §3 P3, `specs/constitution/tech-stack.md` §6 (schema), §9 (testing), §11 (D-27, D-28, D-29, D-30, D-31, GF-2, GF-3, GF-5, GF-17), `AGENTS.md`, `CLAUDE.md`. Those files are the source of truth; this prompt only points.

Method: work the task groups in order (1 → 15). Commit after each task group with an imperative message (the spec's Plan section names each group; small scoped commits, tests live beside code). Migration numbering continues from 005; do not touch 001–004.

Research: if behavior of a library (`pgregory.net/rapid` API, testcontainers, Echo binding `DisallowUnknownFields`) is unclear, use a subagent to investigate and report a summary only — never read long docs into the main context.

Host facts: Go at `/root/.local/go/bin/go`, golangci-lint at `/root/.local/bin/golangci-lint` (Makefile wires them via `deploy/.local/tools.mk`); migrate CLI at `/root/go/bin/migrate`; Docker available for testcontainers (card-free pulls). `make test` runs `-race`; the property test and integration tests need PostGIS containers.

Done when: every Validation item (1–14) in the feature spec passes, run by you on this host, with the command and its outcome quoted in your report. Fix failures before finishing; if a spec item proves wrong or ambiguous, stop that item and report it — do not silently deviate.

Out of scope: anything not in the phase spec (no P4+ work — no ingest endpoint, no device-key HTTP auth, no `last_seen_at` updates, no NATS, no positions, no map, no WS, no jobs, no geofences, no alerts; no compose edits, no README edits, no `deploy/PORTS.md` edits, no MinIO, no Redis, no merge to main, no push). The `CLAUDE.md` update in task group 14 is in scope (mechanical facts only, keep < 200 lines).

Report: files per task group, every validation command + its output, final `git log --oneline` for the branch, and any deviations flagged explicitly.