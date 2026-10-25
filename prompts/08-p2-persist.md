# P2 — persist feature spec (acceptEdits)

Goal: persist the approved P2 plan into the feature spec and commit it SEPARATELY from code. Do NOT implement anything — no Go code, no migrations, no Makefile change yet.

Context (read first):
- The approved plan is at `/root/.claude/plans/p2-auth-vivid-bumblebee.md` — it is the complete instruction: decisions (reused D-01…D-06/GF-2 verbatim + new GF-19…GF-29), Plan task groups 1–15, Requirements, Validation items 1–12, Testing depth, Self-gate checklist, Out of scope. This prompt only points at it; do not restate or alter its decisions.
- `specs/constitution/roadmap.md` §3 P2 and `specs/constitution/tech-stack.md` §3/§6.3/§7/§9/§11 are the binding constitution. `AGENTS.md` rules apply. Git identity is already configured (Musa / musa@localhost) — do not change it.
- The plan's §15 says the feature spec is written during implementation — that part is DONE by this run instead (SDD: spec before code). Implement will start at the plan's task group 1. In the spec's Plan section, keep task groups 1–15 in build order; note that task group 15's CLAUDE.md update stays in implement scope.

Write:
1. `specs/features/02-auth-core.md` — ONE file per D-40, sections: Scope (roadmap P2 quote), Plan (task groups 1–15 in build order, each with files + tests), Requirements (reused D-01…D-06, GF-2, GF-5, GF-7 closed, GF-14, GF-18 closed by GF-19; new GF-19…GF-29 — ALL of them, each with its decision and rationale), Validation (items 1–12 from the plan, runnable, explicit pass conditions), Testing depth, SPEC-AMEND repeat (per R-4: D-06 /users; D-01/D-02 users + refresh_tokens; D-02/D-03 /auth/refresh + /auth/logout + cookie; GF-2 admin_audit; GF-14 fleets.timezone), Out of scope. Copy the plan's content faithfully — do not drop or water down validation pass conditions or decisions.
2. Do NOT touch any constitution file: the plan explicitly records GF-19…GF-29 in the feature spec only; no new mission §7 or tech-stack §11 rows are required. Do NOT edit CLAUDE.md, Makefile, or any code file.
3. Commit exactly one commit: `Add P2 feature spec` containing only `specs/features/02-auth-core.md`. `prompts/07-p2-plan.md` and `prompts/08-p2-persist.md` are already committed — do not re-commit them. `git status` must be clean after.

Self-gate before committing (do it yourself, quote outcome in your report): the spec has all sections (Scope, Plan, Requirements, Validation, Testing depth, SPEC-AMEND repeat, Out of scope); GF-19…GF-29 each appear in Requirements; Validation has all 12 items with pass conditions; roadmap P2 scope sentence appears in Scope; R-6 role-matrix group and R-7 trigger group present; no P3+ content (no vehicles/drivers/devices, no device keys, no pairing, no ingest, no WS, no job tables); nothing contradicts roadmap P2 or tech-stack pins.

Done when: file written, one commit made as specified, `git status` clean, `git log --oneline -2` shows the spec commit. Report: file path + line count, self-gate outcome, git log.

Out of scope: any implementation (Go code, migrations, Makefile, tools/, services/, internal/), CLAUDE.md edits, README edits, compose edits, merge to main.