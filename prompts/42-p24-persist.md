# P24 persist — write the feature spec and ledger rows (acceptEdits pointer)

Goal: persist the approved P24 plan as the feature spec `specs/features/24-cloud-profile.md`, append the SPEC-AMEND ledger rows it enumerates to the constitution, and commit. Change only the files named here.

The approved plan is at `/root/.claude/plans/p24-plan-compiled-leaf.md` (read it with the --add-dir access granted).

Steps:
1. Read the plan file.
2. Copy it verbatim to `specs/features/24-cloud-profile.md` with ONLY these header adjustments:
   - Line 1 becomes `# Feature 24 — Cloud deployment profile (P24)`.
   - Line 3 becomes: `Status: planned on \`phase-24-cloud-profile\`, not implemented, 2026-10-28. Validation results and deviations are appended at the end when the phase runs.`
   - Line 4 becomes: `Source plan: /root/.claude/plans/p24-plan-compiled-leaf.md (approved, self-gate passed — four brief deviations carried with rationale and ledger rows: GF-347 embedded-NATS transport replacing in-process channels, presence store swap in nats mode, GF-353 WS origin allowlist env, GF-355 lowercase GHCR image name; SPEC-AMEND rows exact and enumerated).`
   - The plan's own title line and its Status/Source-plan line are replaced by the two lines above (do not duplicate them).
3. Append the SPEC-AMEND rows exactly as given in the plan's SPEC-AMEND repeat section:
   - `specs/constitution/mission.md` §7: append the ten rows (GF-347 env / GF-347 REDIS_URL / GF-347 D-17 / GF-349 / GF-350 / GF-351 / GF-353 / GF-354 / GF-355 / D-41 release) to the table, before the `Rule:` line. Do not reword them.
   - `specs/constitution/tech-stack.md` §11: append one row per decision ID GF-347…GF-355, same style as the GF-332 row (`| ID | decision text | rationale |`), with the decision text taken from the plan's Requirements and the rationale from each GF block.
4. Verify: feature spec title line 1, Status on line 3, Source plan on line 4, sections present in order (Scope · Plan · Requirements · Validation · Testing depth · SPEC-AMEND repeat · Out of scope), GF range GF-347…GF-355, highest prior decision referenced is GF-346, and no `## Validation results and deviations` section yet (results are appended later, after the last group).
5. Commit ONLY the three files (`specs/features/24-cloud-profile.md`, `specs/constitution/mission.md`, `specs/constitution/tech-stack.md`) as `Add P24 feature spec and ledger rows`.

Do not: write code, run gates, touch other files, edit the plan file itself.

Report: the commit hash, the GF range verified, the section list found, and the number of rows appended to each constitution file.