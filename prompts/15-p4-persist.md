# P4 persist — write the feature spec (auto-mode pointer)

Goal: write the approved P4 plan to `specs/features/04-position-gateway.md` and commit it. No other files.

Context: read the approved plan at `/root/.claude/plans/p4-plan-distributed-quail.md` (complete, self-gated, zero open decisions). Write it VERBATIM as `specs/features/04-position-gateway.md`, with only these mechanical adjustments to its Status header (keep everything else byte-for-byte, including all sections, decisions GF-52…GF-67, validation items 1–10, task groups 1–13):

- First line: `# Feature 04 — Position gateway (P4)`.
- A status line: ``Status: approved plan, persisted 2026-10-25. Branch `phase-4-position-gateway`.`` (backticks around the branch name, as in `specs/features/03-registry-pairing.md` line 3).
- A source line: ``Source plan: `/root/.claude/plans/p4-plan-distributed-quail.md` (approved). Binding constitution: `specs/constitution/roadmap.md` §1 (R-1…R-9) and §3 P4, `specs/constitution/tech-stack.md` §3, §4, §5, §6, §7, §8, §9, §11 (D-15, D-16, D-18, D-29, GF-12), `specs/constitution/mission.md` §7 (SPEC-AMEND rows), `AGENTS.md`, `CLAUDE.md`.` (as in the P3 spec's source line).
- The sentence "This spec is written before any P4 code (R-1, SDD). Implementation starts at Plan task group 1." after the source line, as in 03-registry-pairing.md.
- Drop the plan draft's header note "Status: plan, not yet persisted. Target path when persisted: …" — the spec is now the binding artifact.

Model the file structure exactly on `specs/features/03-registry-pairing.md` (sections: Scope · Plan · Requirements · Validation · Testing depth · SPEC-AMEND repeat · Out of scope). The plan content already has these sections — carry them over. Keep the plan's numbered Validation items exactly 1–10 and its task groups exactly 1–13.

When the file is written, commit exactly: `git add specs/features/04-position-gateway.md` then `git commit -m "Add P4 feature spec"`. Nothing else is committed, nothing else is modified.

Out of scope: any code, migration, Makefile, CLAUDE.md, compose, or prompt-file change; merging; pushing.

Report: the commit hash, the file's line count, and confirmation that every plan section is present.