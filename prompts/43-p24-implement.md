# P24 implement — cloud deployment profile (acceptEdits pointer)

Goal: implement roadmap phase P24 exactly per the committed feature spec `specs/features/24-cloud-profile.md`. Those files are the complete instruction; this prompt only points.

Context: read `CLAUDE.md`, `specs/features/24-cloud-profile.md` (the spec — Scope, Plan G0–G8, Requirements GF-347…GF-355, Validation (a)–(n)), `specs/constitution/mission.md` §7 (P24 rows just appended), `specs/constitution/tech-stack.md` §11 (GF-347…GF-355 rows). Work the task groups in build order G0 → G8. Make one commit per task group with an imperative message (the orchestrator's instruction overrides any "commits on request" line in the spec).

Research: use a subagent for unclear `coder/websocket` or `nats-server/v2/server` embedding behavior — summary only, never long docs into the main context.

Environment facts: Go is at `/root/.local/go/bin/go` (Makefile already overrides via `deploy/.local/tools.mk`); Docker is available on this host; node v26 present. Run the gates with `make fmt-check lint test` from the repo root. Docker build validation (f) is runnable here — build once after G4 and again at the end.

Done when: every Validation check (a)–(n) passes run by you, with commands + output tails quoted (the docker build in (f) included). The orchestrator separately runs the S0–S9 smoke after your step — do not attempt it (it needs the Neon URI, which you must never read or print).

Non-negotiables: never print or commit any secret value; the Neon URI file is off-limits; `deploy/.local/` stays gitignored; `deploy/docker-compose.yml`, `README.md`, `db/migrations/`, `e2e/` are untouched; no `git push`; CLAUDE.md stays under 200 lines (prune existing rows to pay for additions, per G8).

Out of scope: anything not in the spec; the S0–S9 smoke; Render/Neon/Pages account wiring; push.

Report: files per task group, every validation command + its output tail, final `git log --oneline` for the branch, and the CLAUDE.md line count.