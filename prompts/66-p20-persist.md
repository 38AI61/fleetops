# P20 persist — write the feature spec (acceptEdits pointer)

Goal: persist the approved P20 plan as the feature spec `specs/features/20-eta-slip-osrm.md` and commit it. Write exactly one new file; change nothing else.

The approved plan is at `/root/.claude/plans/p20-plan-eager-puzzle.md` (read it with the --add-dir access granted).

Steps:
1. Read the plan file.
2. Copy it verbatim to `specs/features/20-eta-slip-osrm.md` with ONLY these header adjustments:
   - Line 1 stays `# Feature 20 — ETA slip & OSRM (P20)`.
   - Line 3 becomes: `Status: planned on \`phase-20-eta-slip-osrm\`, not implemented, 2026-10-28. Validation results and deviations are appended at the end when the phase runs.`
   - Line 4 becomes: `Source plan: /root/.claude/plans/p20-plan-eager-puzzle.md (approved).`
   - The plan's own title line and `Source plan:` line are replaced by the two lines above (do not duplicate them).
3. Verify: title line 1, Status on line 3, Source plan on line 4, sections present in order (Scope · Plan · Requirements · Validation · Testing depth · SPEC-AMEND repeat · Out of scope), GF range GF-303 onward, highest prior decision referenced is GF-302, and no `## Validation results and deviations` section yet (results are appended later, after the last group).
4. Commit ONLY this file as `Add P20 feature spec`.

Do not: write code, run gates, touch other files, edit the plan file itself, or add SPEC-AMEND rows.

Report: the commit hash, the GF range verified, and the section list found.