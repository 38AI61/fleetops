# P19 persist — write the feature spec (acceptEdits pointer)

Goal: persist the approved P19 plan as the feature spec `specs/features/19-alerts-core.md` and commit it. Write exactly one new file; change nothing else.

The approved plan is at `/root/.claude/plans/p19-plan-tranquil-avalanche.md` (read it with the --add-dir access granted).

Steps:
1. Read the plan file.
2. Copy it verbatim to `specs/features/19-alerts-core.md` with ONLY these header adjustments:
   - Line 1 stays `# Feature 19 — Alerts core (P19)`.
   - Line 3 becomes: `Status: planned on \`phase-19-alerts-core\`, not implemented, 2026-10-28. Validation results and deviations are appended at the end when the phase runs.`
   - Line 4 becomes: `Source plan: /root/.claude/plans/p19-plan-tranquil-avalanche.md (approved).`
   - The plan's own `# Feature 19 — Alerts core (P19)` title line and `Source plan:` line are replaced by the two lines above (do not duplicate them).
3. Verify: title line 1, Status on line 3, Source plan on line 4, sections present in order (Scope · Plan · Requirements · Validation · Testing depth · SPEC-AMEND repeat · Out of scope), GF range GF-290…GF-302, highest prior decision referenced is GF-289, and no `## Validation results and deviations` section yet (results are appended later, after G9).
4. Commit ONLY this file as `Add P19 feature spec`.

Do not: write code, run gates, touch other files, edit the plan file itself, or add SPEC-AMEND rows.

Report: the commit hash, the GF range verified, and the section list found.