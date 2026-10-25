# P1 — persist feature spec (acceptEdits)

Goal: persist the approved P1 plan into the feature spec and commit it SEPARATELY from code. Do NOT implement anything — no Go code, no migrations, no Makefile, no CI files yet.

Context (read first):
- The approved plan is at `/root/.claude/plans/p1-toolchain-composed-pebble.md` — it is the complete instruction: task groups T0–T8 (§"Task groups"), decisions GF-5…GF-18 + reused D-IDs (§"Decisions"), requirements (§"Requirements"), validation V0–V9 (§"Validation"), testing depth (§"Testing depth"), self-gate checklist (§"Self-gate checklist"). This prompt only points at it; do not restate or alter its decisions.
- `specs/constitution/roadmap.md` §3 P1 and `specs/constitution/tech-stack.md` §2/§3/§9/§11 are the binding constitution. `AGENTS.md` rules apply. Git identity is already configured (Musa / musa@localhost) — do not change it.

Write:
1. `specs/features/01-toolchain-ci-migrations.md` — ONE file per D-40 containing exactly the sections the plan's T0 names: Scope, Plan (T1–T8 in build order, each with files + tests), Requirements (with decision IDs), Validation (V0–V9, runnable, explicit pass conditions), Testing depth, SPEC-AMEND repeat (GF-12), Out of scope. Copy the plan's content faithfully — do not drop or water down validation pass conditions.
2. Constitution rows per D-40 and the plan's T0: add the **GF-12 SPEC-AMEND row** to `specs/constitution/mission.md` §7 (table row, matching the existing style, phase P1) and the **GF-5…GF-18 rows** to the gap-fill table in `specs/constitution/tech-stack.md` §11 (matching the existing GF table style). Do not touch any other constitution content.
3. Commit, in this order:
   - First commit: `prompts/04-p1-plan.md` + `prompts/05-p1-persist.md` (both exist in `prompts/`) with message `Add P1 plan prompt` (provenance).
   - Second commit: the feature spec + the two constitution row updates, with message exactly `Add P1 feature spec`.
   - Do NOT commit anything else. `git status` must be clean after (except untracked `deploy/.local/` if present — leave it untracked).

Self-gate before committing (do it yourself, quote outcome in your report): the spec contains all six sections; V0–V9 all present with pass conditions; every GF-5…GF-18 decision appears in Requirements; mission §7 has the GF-12 row; tech-stack §11 has GF-5…GF-18; no P2+ content (no auth, users, seed logic, endpoints); nothing contradicts roadmap P1.

Done when: files written, two commits made as specified, `git status` clean, `git log --oneline -3` shows both commits. Report: files with line counts, self-gate outcome, git log.

Out of scope: any implementation (Go code, db/, internal/, Makefile, .golangci.yml, .github/, CLAUDE.md/PORTS.md edits), README edits, compose edits.