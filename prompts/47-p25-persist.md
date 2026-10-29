# P25 persist — write the feature spec (acceptEdits pointer)

Goal: persist the approved P25 plan as the feature spec `specs/features/25-ui-styling.md` and commit it. Write exactly one new file; change nothing else.

The approved plan is at `/root/.claude/plans/p25-plan-typed-adleman.md` (read it with the `--add-dir /root/.claude/plans` access granted). The plan is APPROVED as-is; answer its closing "confirm GF-363" question with yes — the child does not merge or push, the orchestrator owns both.

Steps:
1. Read the plan file.
2. Copy it verbatim to `specs/features/25-ui-styling.md` with ONLY these adjustments:
   - Line 1 becomes `# Feature 25 — UI styling pass (P25)`.
   - Line 3 becomes: `Status: planned on \`phase-25-ui-styling\`, not implemented, 2026-10-29. Validation results and deviations are appended at the end when the phase runs.`
   - Line 4 becomes: `Source plan: /root/.claude/plans/p25-plan-typed-adleman.md (approved, self-gate passed — brief deviations carried with rationale and ledger rows as listed in the plan: GF-363 no merge/push by the child, GF-364 no Go gates; GF-362 corrects the brief's .card-head fact).`
   - The plan's own title line and its `Source plan:` line are replaced by the two lines above (do not duplicate them).
   - Remove the plan's closing approval question (its final paragraph after the `---` line, beginning "Open decisions: none.") — it is conversation, not spec content. Keep everything above it verbatim, including the `---` only if the plan body itself uses separators between sections elsewhere; if the `---` belongs solely to that closing block, drop the separator too.
3. Verify: title line 1, Status on line 3, Source plan on line 4, sections present in order (Scope · Plan · Requirements · Validation · Testing depth · SPEC-AMEND repeat · Out of scope), GF range GF-356…GF-364 complete, highest prior decision referenced is GF-355, and no `## Validation results and deviations` section yet (results are appended later, after the last group).
4. Commit ONLY this file as `Add P25 feature spec`.

Do not: write code, run gates, touch other files, edit the plan file itself, or add SPEC-AMEND rows.

Report: the commit hash, the GF range verified, and the section list found.