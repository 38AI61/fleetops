# P5 persist — write the feature spec (acceptEdits pointer)

Goal: write the approved P5 plan to `specs/features/05-batch-writer.md` and commit it. No other files.

Context: read the approved plan at `/root/.claude/plans/p5-plan-spicy-taco.md` (complete, self-gated — two fixes already applied to the plan file by the orchestrator: the GF-77 positions INSERT now has ten `$1…$10` array placeholders, and GF-80 states empty string = default). Write it VERBATIM as `specs/features/05-batch-writer.md`, with only these mechanical adjustments to its Status header (keep everything else byte-for-byte, including all sections Scope · Plan · Requirements · Validation · Testing depth · SPEC-AMEND repeat · Out of scope, decisions GF-68…GF-84, validation items 1–14, task groups 1–10):

- First line: `# Feature 05 — Batch writer core (P5)`.
- A status line: ``Status: approved plan, persisted 2026-10-25. Branch `phase-5-batch-writer`.`` (backticks around the branch name, as in `specs/features/04-position-gateway.md` line 3).
- A source line: ``Source plan: `/root/.claude/plans/p5-plan-spicy-taco.md` (approved). Binding constitution: `specs/constitution/roadmap.md` §1 (R-1…R-9) and §3 P5, `specs/constitution/tech-stack.md` §3, §5, §6.2, §8, §9, §11 (D-14, D-15, D-18, D-19, D-22), `specs/constitution/mission.md` §7 (SPEC-AMEND rows), `AGENTS.md`, `CLAUDE.md`.` (as in the P4 spec's source line).
- The sentence "This spec is written before any P5 code (R-1, SDD). Implementation starts at Plan task group 1." after the source line, as in 04-position-gateway.md.
- Drop the plan draft's header note "Status: plan, persisted 2026-10-25." line — the spec's own status line replaces it. Keep the rest of the header paragraph (the binding constitution sentence) in the source line exactly as given above.

Model the file structure exactly on `specs/features/04-position-gateway.md` (sections: Scope · Plan · Requirements · Validation · Testing depth · SPEC-AMEND repeat · Out of scope). The plan content already has these sections — carry them over. Keep the plan's numbered Validation items exactly 1–14 and its task groups exactly 1–10.

When the file is written, commit exactly: `git add specs/features/05-batch-writer.md` then `git commit -m "Add P5 feature spec"`. Nothing else is committed, nothing else is modified.

Out of scope: any code, migration, Makefile, CLAUDE.md, compose, or prompt-file change; merging; pushing.

Report: the commit hash, the file's line count, and confirmation that every plan section is present.