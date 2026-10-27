# P10 implement — job core & state machine (auto-mode pointer)

Goal: implement roadmap phase P10 exactly per the committed feature spec.

Context: read `specs/features/10-job-core.md` — it is the complete instruction (task groups G0–G11, requirements GF-170…GF-183, validation items 1–9). Also read `specs/constitution/roadmap.md` §1 (R-1…R-9) and §3 P10, `specs/constitution/tech-stack.md` §6.1–§6.3, §7.1–§7.2, §8.1, §9, §11 (D-17, D-23, D-24, D-26, GF-4), `specs/features/08-map-api-simulator.md` (GF-126…GF-134 — GF-133 is the JobLookup seam your package implements), `AGENTS.md`, `CLAUDE.md` (ports, env, migration rules GF-5, test commands). Those files are the source of truth; this prompt only points.

Method: work the task groups in order (G0 → G11). Commit after each task group with an imperative message (`git add <paths> && git commit -m "..."`), each with a `Co-Authored-By: Claude Haiku 5.5 <noreply@anthropic.com>` trailer — small scoped commits, tests live beside code, never fold unrelated groups into one commit. The spec's test names are binding.

Host facts: Go at the `deploy/.local/tools.mk` paths (already wired — `make fmt-check lint test` just work); `make test` needs Docker for the PostGIS/NATS testcontainers — Docker IS available (card-free pulls; `postgis/postgis:16-3.4`, `nats:2` already cached). Compose is NEVER started (standing rule). lib/pq is the driver; `pgregory.net/rapid` is already a module dependency (see `internal/registry/pairing_property_test.go` for the repo's rapid idiom with `testinfra.NewMigratedDBFrom`). `go`/`gofmt`/`golangci-lint`/`migrate` come from the tools.mk overrides; do not install anything. Run nothing long-lived; there is no manual server run in this phase — all validation is via `go test` and `make` targets.

Validation: run every automated gate (1–9) yourself, quoting the command and its outcome. Gate 3 (`make test`) covers the full suite with `-race`; budget for testcontainer startup. If a spec item proves wrong or ambiguous, stop that item and report it — do not silently deviate; fix through the spec (edit `specs/features/10-job-core.md` in the same commit) only when the fix is mechanical and record it in the report.

Done when: validation gates 1–9 pass with the exact commands and output quoted in your report, the scope fence holds (`git diff --name-only main...HEAD` lists only paths under `db/migrations/`, `db/roundtrip_test.go`, `internal/jobs/`, `services/api/`, `CLAUDE.md`, `specs/features/10-job-core.md`, `prompts/`), the tree is clean, and no commit touches `README.md`, `deploy/`, `specs/constitution/`, `internal/hub/`, `apps/`, `go.mod`, or `go.sum`.

Out of scope: anything not in the phase spec — no driver endpoints or `/driver/jobs/today` (P11), no proofs/MinIO/delivery_geom writers (P12), no assignment UI or nearest-van (P13), no hub/fanout/presence changes, no `PATCH/DELETE /jobs`, no pagination, no README/PORTS/compose/constitution edits, no migrations beyond 009, no new env vars or make targets, no merge to main, no push.

Report: files per task group, every validation command + its output, the final `git log --oneline` for the branch, and any deviations flagged explicitly.