# P6 persist — write the feature spec (acceptEdits pointer)

Goal: write the approved P6 plan to `specs/features/06-consumer-partitions.md` and commit it. No other files.

Context: read the approved plan at `/root/.claude/plans/p6-plan-majestic-quill.md` (complete, self-gated — four targeted fixes already applied to the plan file by the orchestrator: G6/GF-93/GF-94/GF-95 now wire the consumer into a new binary `services/ingest` per tech-stack §3/§4, validation item 6 and Out of scope were aligned, validation item 8 now expects the CLAUDE.md local-run line). Write it VERBATIM as `specs/features/06-consumer-partitions.md`, with only these mechanical adjustments to its Status header (keep everything else byte-for-byte, including all sections Scope · Plan · Requirements · Validation · Testing depth · SPEC-AMEND repeat · Out of scope, decisions GF-86…GF-99, task groups G0–G7, validation items 1–8):

- First line: `# Feature 06 — Ingest consumer & partitions (P6)`.
- A status line: ``Status: approved plan, persisted 2026-10-25. Branch `phase-6-consumer-partitions`.`` (backticks around the branch name, as in `specs/features/05-batch-writer.md` line 3).
- A source line: ``Source plan: `/root/.claude/plans/p6-plan-majestic-quill.md` (approved). Binding constitution: `specs/constitution/roadmap.md` §1 (R-1…R-9), §3 P6, §5, `specs/constitution/tech-stack.md` §3, §4, §6.3, §8, §9, §11 (D-14, D-16), `specs/constitution/mission.md` §7 (SPEC-AMEND rows), `AGENTS.md`, `CLAUDE.md`.` (in the P4/P5 spec source-line style).
- The sentence "This spec is written before any P6 code (R-1, SDD). Implementation starts at Plan task group G0." after the source line, as in the prior specs.
- Drop the plan draft's top note "Persist verbatim as `specs/features/06-consumer-partitions.md` (on branch `phase-6-consumer-partitions`). This plan writes no code; it is the spec content." — the spec's own status line replaces it. Keep the rest byte-for-byte.

Model the file structure exactly on `specs/features/05-batch-writer.md` (sections: Scope · Plan · Requirements · Validation · Testing depth · SPEC-AMEND repeat · Out of scope). The plan content already has these sections — carry them over. Keep the plan's numbered Validation items exactly 1–8 and its task groups exactly G0–G7.

When the file is written, commit exactly: `git add specs/features/06-consumer-partitions.md` then `git commit -m "Add P6 feature spec"`. Nothing else is committed, nothing else is modified.

Out of scope: any code, migration, Makefile, CLAUDE.md, compose, or prompt-file change; merging; pushing.

Report: the commit hash, the file's line count, and confirmation that every plan section is present.