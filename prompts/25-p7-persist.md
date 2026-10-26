# P7 persist — write the feature spec (acceptEdits pointer)

Goal: write the approved P7 plan to `specs/features/07-fanout-ws.md` and commit it. No other files.

Context: read the approved plan at `/root/.claude/plans/p7-plan-nifty-tide.md` (complete, self-gated — three targeted fixes already applied to the plan file by the orchestrator: G0 now confirms the sweeper's data source correctly — `vehicles_latest` has no `device_id` column and the sweeper joins `vehicles` for `paired_device_id`; GF-106's sweep SQL was corrected to that JOIN shape; the G8 roadmap-Validation-3 test is named `TestSilentDeviceOfflineOnceEndToEnd`). Write it VERBATIM as `specs/features/07-fanout-ws.md`, with only these mechanical adjustments to its Status header (keep everything else byte-for-byte, including all sections Scope · Plan · Requirements · Validation · Testing depth · SPEC-AMEND repeat · Out of scope, decisions GF-100…GF-125, task groups G0–G9, validation items 1–13):

- First line stays: `# Feature 07 — Fanout, presence & WS hub (P7)`.
- Add a status line after it: ``Status: approved plan, persisted 2026-10-25. Branch `phase-7-fanout-ws`.` ` (backticks around the branch name, as in `specs/features/06-consumer-partitions.md` line 3).
- Add a source line: ``Source plan: `/root/.claude/plans/p7-plan-nifty-tide.md` (approved). Binding constitution: `specs/constitution/roadmap.md` §1 (R-1…R-9), §3 P7, §5, `specs/constitution/tech-stack.md` §1, §2, §3, §4, §5, §7.3, §8, §9, §11 (D-03, D-17, D-20, D-21, D-22, D-41, GF-4, GF-5, GF-8, GF-13), `specs/constitution/mission.md` §7 (SPEC-AMEND rows), `specs/features/06-consumer-partitions.md` (P6 decisions your code builds on), `AGENTS.md`, `CLAUDE.md`.` (in the P6 spec source-line style).
- Add the sentence "This spec is written before any P7 code (R-1, SDD). Implementation starts at Plan task group G0." after the source line, as in the prior specs.
- The plan draft's "## Scope" heading and everything under it stays exactly as written (the plan already has the full section set: Scope · Plan · Requirements · Validation · Testing depth · SPEC-AMEND repeat · Out of scope).

Model the file structure exactly on `specs/features/06-consumer-partitions.md`. Keep the plan's numbered Validation items exactly 1–13 and its task groups exactly G0–G9.

When the file is written, commit exactly: `git add specs/features/07-fanout-ws.md` then `git commit -m "Add P7 feature spec"`. Nothing else is committed, nothing else is modified.

Out of scope: any code, migration, Makefile, CLAUDE.md, compose, or prompt-file change; merging; pushing.

Report: the commit hash, the file's line count, and confirmation that every plan section is present.