# P24 implement part 2 — finish G7 and G8, then validate (acceptEdits pointer)

Goal: complete roadmap phase P24 implementation. This is a continuation: part 1 ran out of turns. The committed feature spec `specs/features/24-cloud-profile.md` remains the complete instruction; this prompt only points and reports the exact resume point.

Context: read `CLAUDE.md`, `specs/features/24-cloud-profile.md` (Plan G0–G8, Requirements GF-347…GF-355, Validation (a)–(n)), and `git log --oneline` + `git status` first.

Resume point (verified by the orchestrator):
- Committed: spec commit + G1 (e402bb0), G2 (15371b1), G3 (a017dd4), G4 (3a9adb0), G5 (255a2a9), G6 (1559bf3).
- G7 PARTIAL, uncommitted in the tree: `.github/workflows/ci.yml` modified and `deploy/cloud/ci_test.go` untracked. Review them against the spec's G7 (image job, deploy job, TestCIWorkflowShape), fix as needed, run `make test ./deploy/cloud/...` scope (or the package test), and commit as G7.
- G8 NOT STARTED: `docs/cloud.md`, CLAUDE.md rows (≤199 lines; prune existing rows to pay), `deploy/cloud/smoke.mjs`, Makefile targets `cloud-smoke` (+ confirm `cloud-image`, `cloud-seed`, `pages-test` exist from earlier groups — add any that are missing).

Then: run Validation (a)–(n) from the spec in order, including the docker build in (f) (`make cloud-image` or the docker build command) and the container binary check. Record every command + output tail. Append a `## Validation results and deviations` section at the end of `specs/features/24-cloud-profile.md` in the P23 spec style (numbered checks, deviations D1… with reasons), and commit it as `Add P24 validation record`.

One commit per task group, imperative messages. Never print or commit a secret value; the Neon URI file is off-limits; `deploy/.local/` stays gitignored; `deploy/docker-compose.yml`, `README.md`, `db/migrations/`, `e2e/` untouched; no `git push`; CLAUDE.md ≤ 199 lines.

Out of scope: the orchestrator's S0–S9 smoke (needs the Neon URI — never read it); Render/Neon/Pages wiring; push.

Report: files per remaining group, every validation command + output tail, final `git log --oneline` for the branch, CLAUDE.md line count, and any deviation.