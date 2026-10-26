# P8 persist — write the feature spec (acceptEdits pointer)

Goal: write the approved P8 plan to `specs/features/08-map-api-simulator.md` and commit it. No other files.

Context: read the approved plan at `/root/.claude/plans/p8-plan-cozy-sphinx.md` (complete, self-gated — the orchestrator applied four targeted amendments to the plan file after approval: GF-85 is reclassified as a P5 spec-local decision and is no longer in the binding list; GF-142 now pins seeded waypoints — `RunDemo` generates and writes each vehicle's four-waypoint loop into `devices.json` and the simulator only reads them; GF-137's schema and reader rules now include `waypoints`; the Scope line and G2 tests were updated to match). Write it VERBATIM as `specs/features/08-map-api-simulator.md`, with only these mechanical adjustments to its header (keep everything else byte-for-byte, including all sections Scope · Plan · Requirements · Validation · Testing depth · SPEC-AMEND repeat · Out of scope, decisions GF-126…GF-151, task groups G0–G6, validation items 1–14):

- First line stays: `# Feature 08 — Map read API & GPS simulator (P8)`.
- Add a status line after it: ``Status: approved plan, persisted 2026-10-26. Branch `phase-8-map-api-simulator`.` ` (backticks around the branch name, as in `specs/features/07-fanout-ws.md` line 3).
- Add a source line: ``Source plan: `/root/.claude/plans/p8-plan-cozy-sphinx.md` (approved, self-gate amendments applied). Binding constitution: `specs/constitution/roadmap.md` §1 (R-1…R-9), §3 P8, `specs/constitution/tech-stack.md` §3, §4, §5, §6, §7.1, §9, §11 (D-05, D-12, D-27, D-40, GF-5, GF-8, GF-12, GF-13), `specs/constitution/mission.md` §7 (SPEC-AMEND rows), `specs/features/07-fanout-ws.md` (P7 decisions GF-100…GF-125 your code builds on), `AGENTS.md`, `CLAUDE.md`.` (in the P7 spec source-line style).
- Add the sentence "This spec is written before any P8 code (R-1, SDD). Implementation starts at Plan task group G0." after the source line, as in the prior specs.
- The plan's "## Scope" heading and everything under it stays exactly as written.

Model the file structure exactly on `specs/features/07-fanout-ws.md`. Keep the plan's numbered Validation items exactly 1–14 and its task groups exactly G0–G6.

When the file is written, commit exactly: `git add specs/features/08-map-api-simulator.md` then `git commit -m "Add P8 feature spec"`. Nothing else is committed, nothing else is modified.

Out of scope: any code, migration, Makefile, CLAUDE.md, compose, or prompt-file change; merging; pushing.

Report: the commit hash, the file's line count, and confirmation that every plan section is present.