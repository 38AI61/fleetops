# P3 persist — write the feature spec (auto-mode pointer)

Goal: write the approved P3 plan to `specs/features/03-registry-pairing.md` and commit it. No other files.

Context: read the approved plan at `/root/.claude/plans/p3-plan-resilient-horizon.md` (complete, self-gated, zero open decisions). Write it VERBATIM as `specs/features/03-registry-pairing.md`, with only these mechanical adjustments to its Status header (keep everything else byte-for-byte, including all sections, decisions GF-30…GF-51, validation items, task groups):
- First line: `# Feature 03 — Registry & pairing (P3)`.
- A status line: `Status: approved plan, persisted 2026-10-25. Branch `phase-3-registry-pairing`.` (backticks around the branch name, as in `specs/features/02-auth-core.md` line 3).
- A source line: `Source plan: `/root/.claude/plans/p3-plan-resilient-horizon.md` (approved). Binding constitution: `specs/constitution/roadmap.md` §1 (R-1…R-9) and §3 P3, `specs/constitution/tech-stack.md` §3, §6, §7, §9, §11 (D-27, D-28, D-29, D-30, D-31, GF-2, GF-3, GF-5, GF-17), `specs/constitution/mission.md` §7 (SPEC-AMEND rows), `AGENTS.md`, `CLAUDE.md`.`
- The sentence "This spec is written before any P3 code (R-1, SDD). Implementation starts at Plan task group 1." after the source line, as in 02-auth-core.md.
- Drop the plan draft's preamble note about plan mode and persisting "after approval" — the spec is now the binding artifact.

Model the file structure exactly on `specs/features/02-auth-core.md` (sections: Scope · Plan · Requirements · Validation · Testing depth · SPEC-AMEND repeat · Out of scope). The plan content already has these sections — carry them over.

When the file is written and linted as clean markdown, commit exactly: `git add specs/features/03-registry-pairing.md` then `git commit -m "Add P3 feature spec"`. Nothing else is committed, nothing else is modified.

Out of scope: any code, migration, Makefile, CLAUDE.md, or prompt-file change; merging; pushing.

Report: the commit hash, the file's line count, and confirmation that every plan section is present.