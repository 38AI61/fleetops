# P7 implement — fanout, presence, WS hub (auto-mode pointer)

Goal: implement roadmap phase P7 exactly per the committed feature spec.

Context: read `specs/features/07-fanout-ws.md` — it is the complete instruction (task groups G0–G9, requirements GF-100…GF-125, validation items 1–13). Also read `specs/constitution/roadmap.md` §1 (R-1…R-9) and §3 P7, `specs/constitution/tech-stack.md` §3 (repo layout: `services/ws-hub/` is a pinned Go service), §5 (env names + tunables), §7.3 (WS protocol), §8 (messaging split — §8.1 D-17, §8.3 GF-4), §9 (testing), §11 (D-03, D-17, D-20, D-21, D-22, D-41), `specs/features/06-consumer-partitions.md` (the P6 decisions your code builds on: GF-86…GF-99 — the consumer loop, the `FlushResult.Stored` seam, the metrics pattern), `AGENTS.md`, `CLAUDE.md`. Those files are the source of truth; this prompt only points.

Method: work the task groups in order (G0 → G9). Commit after each task group with an imperative message (small scoped commits; tests live beside code). Never fold unrelated groups into one commit. The spec's test names are binding. Commit with `git add <paths> && git commit -m "<imperative message>"` — never fold prompts/ or specs/ into implementation commits (they are already committed on this branch).

Research: if library behavior is unclear (redis/go-redis v9 Pub/Sub, the `Set`+`Get` option, testcontainers redis module, coder/websocket Accept/Dial/Close/Ping/CloseRead/SetReadLimit, nats.go core subscribe), use a subagent to investigate and report a summary only — never read long docs into the main context.

Host facts: Go at `/root/.local/go/bin/go` and golangci-lint at `/root/.local/bin/golangci-lint` are already wired via `deploy/.local/tools.mk` (gitignored); migrate CLI at `/root/go/bin/migrate`. Docker is available for testcontainers (card-free pulls; `postgis/postgis:16-3.4`, `nats:2`, `redis:7` are already cached). `make test` runs `-race`. Integration tests may take minutes: run them with generous timeouts, one package at a time.

Done when: every Validation item (1–13) in the feature spec passes, run by you on this host, with the command and its outcome quoted in your report. Fix failures before finishing; if a spec item proves wrong or ambiguous, stop that item and report it — do not silently deviate.

Out of scope: anything not in the phase spec (no P8 work — no `/map/vehicles`, no simulator, no device→vehicle map; no P9 UI; no P10+ producers — synthetic NATS publishes in tests only; no P22 — no `/metrics` route, no dashboards, no compose, no port binding beyond the ws-hub's own listener; no k6; no migrations — `db/` untouched; no README, `deploy/`, or `specs/constitution/` edits; CLAUDE.md only per G9 mechanical rows; no merge to main, no push).

Report: files per task group, every validation command + its output, final `git log --oneline` for the branch, and any deviations flagged explicitly.