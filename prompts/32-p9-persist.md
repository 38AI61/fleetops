# P9 persist — write the feature spec (acceptEdits pointer)

Goal: write the approved P9 plan to `specs/features/09-map-ui.md` and commit it. No other files.

Context: read the approved plan at `/root/.claude/plans/p9-plan-snoopy-balloon.md` (complete, self-gated — the orchestrator applied one targeted amendment to the plan file after approval: the three `docker run` infrastructure lines in the Validation section now bind to `127.0.0.1` explicitly, e.g. `-p 127.0.0.1:10002:5432`, matching the deploy/PORTS.md rule that infrastructure binds 127.0.0.1 only; everything else is unchanged). Write it VERBATIM as `specs/features/09-map-ui.md`, with only these mechanical adjustments to its header (keep everything else byte-for-byte, including all sections Scope · Plan · Requirements · Validation · Testing depth · SPEC-AMEND repeat · Out of scope, decisions GF-152…GF-169, task groups G0–G10, validation gates and C1–C10 + M1–M7):

- First line stays: `# P9 — Operations map UI (specs/features/09-map-ui.md content)` → replace the first line with exactly `# Feature 09 — Operations map UI (P9)`.
- Add a status line after it: ``Status: approved plan, persisted 2026-10-26. Branch `phase-9-map-ui`.` ` (backticks around the branch name, as in `specs/features/08-map-api-simulator.md` line 3).
- Add a source line: ``Source plan: `/root/.claude/plans/p9-plan-snoopy-balloon.md` (approved, self-gate amendment applied). Binding constitution: `specs/constitution/roadmap.md` §1 (R-1…R-9), §3 P9, §5, `specs/constitution/tech-stack.md` §1, §2, §3, §4, §7.3, §9, §11 (D-02, D-03, D-21, D-22, D-27, D-38, D-40), `specs/constitution/mission.md` §3 (item 4) and §7 (SPEC-AMEND rows), `specs/features/08-map-api-simulator.md` (P8 decisions GF-126…GF-151 your UI consumes), `specs/features/07-fanout-ws.md` (P7 decisions GF-100…GF-125 your WS client honors), `AGENTS.md`, `CLAUDE.md`.` (in the P8 spec source-line style).
- Add the sentence "This spec is written before any P9 code (R-1, SDD). Implementation starts at Plan task group G0." after the source line, as in the prior specs.
- The plan's "## Scope" heading and everything under it stays exactly as written.

Model the file structure exactly on `specs/features/08-map-api-simulator.md`. The plan's numbered validation gates and scripted checks (C1–C10) and manual checks (M1–M7) stay exactly as numbered. The plan's closing note about the four flagged decisions is NOT part of the plan content — do not copy it into the spec (it was the plan run's report preamble, already resolved by GF entries).

When the file is written, commit exactly: `git add specs/features/09-map-ui.md prompts/31-p9-plan.md` then `git commit -m "Add P9 feature spec"`. Nothing else is committed, nothing else is modified.

Out of scope: any code, migration, Makefile, CLAUDE.md, compose, or other prompt-file change; merging; pushing.

Report: the commit hash, the file's line count, and confirmation that every plan section is present.