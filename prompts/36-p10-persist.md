# P10 persist — write the feature spec (acceptEdits pointer)

Goal: write the approved P10 plan to `specs/features/10-job-core.md` and commit it. No other files.

Context: read the approved plan at `/root/.claude/plans/p10-plan-quizzical-shamir.md` (complete, self-gated by the orchestrator — no amendments were needed). Write it VERBATIM as `specs/features/10-job-core.md`, with only these mechanical adjustments to its header (keep everything else byte-for-byte, including all sections Scope · Plan · Requirements · Validation · Testing depth · SPEC-AMEND repeat · Out of scope, decisions GF-170…GF-183, task groups G0–G11):

- Replace the first line `# Feature 10 — Job core & state machine (P10)` with exactly `# Feature 10 — Job core & state machine (P10)` (it already matches the final spec title — keep it).
- Add a status line after it: ``Status: approved plan, persisted 2026-10-26. Branch `phase-10-job-core`.`` (backticks around the branch name, as in `specs/features/08-map-api-simulator.md` line 3).
- Add a source line: ``Source plan: `/root/.claude/plans/p10-plan-quizzical-shamir.md` (approved, self-gate passed with no amendments). Binding constitution: `specs/constitution/roadmap.md` §1 (R-1…R-9, esp. R-7), §3 P10, §5 (append-only gate), `specs/constitution/tech-stack.md` §6.1–§6.3, §7.1–§7.2, §8.1, §9, §11 (D-17, D-23, D-24, D-26, GF-4), `specs/constitution/mission.md` §7 (D-23 and D-26 SPEC-AMEND rows), `specs/features/08-map-api-simulator.md` (GF-133 JobLookup seam your package implements), `AGENTS.md`, `CLAUDE.md`.`` (in the P8 spec source-line style).
- Add the sentence "This spec is written before any P10 code (R-1, SDD). Implementation starts at Plan task group G0." after the source line, as in the prior specs.
- The plan's "## Scope" heading and everything under it stays exactly as written.

Model the file structure exactly on `specs/features/08-map-api-simulator.md`. The plan's numbered task groups and tests stay exactly as named.

When the file is written, commit exactly: `git add specs/features/10-job-core.md prompts/35-p10-plan.md` then `git commit -m "Add P10 feature spec"` with a `Co-Authored-By: Claude Haiku 5.5 <noreply@anthropic.com>` trailer, matching the prior spec commits. Nothing else is committed, nothing else is modified.

Out of scope: any code, migration, Makefile, CLAUDE.md, compose, or other prompt-file change; merging; pushing.

Report: the commit hash, the file's line count, and confirmation that every plan section (Scope · Plan · Requirements · Validation · Testing depth · SPEC-AMEND repeat · Out of scope) is present.