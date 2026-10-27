# P17 persist — write the feature spec from the approved plan (pointer)

Goal: write `specs/features/17-geofence-registry-drawing.md` in this repo from the approved plan at `/root/.claude/plans/p17-plan-wiggly-glacier.md` (the newest `p17-plan-*.md` in `/root/.claude/plans/`), then commit it. Nothing else.

Context: you are the persist step of roadmap phase P17 (Geofence registry & drawing) of this repo. The plan was produced by a plan-mode run and is approved. Persist it verbatim as the feature spec.

1. Find the plan: the newest file matching `/root/.claude/plans/p17-plan-*.md`. If none exists, stop and report that — do not guess or reconstruct.
2. Write its content verbatim to `specs/features/17-geofence-registry-drawing.md` (this repo's spec convention: title `# Feature 17 — Geofence registry & drawing (P17)`, then the `Status:` and `Source plan:` header lines exactly like `specs/features/16-playwright-happy-path.md` lines 1–7 — copy that header shape, updating phase number, branch `phase-17-geofence-registry-drawing`, and today's date; leave the plan's own `Status:` line if it duplicates the header, keeping one Status line total). If the plan text already carries the title/header, keep the plan's own title but make the header lines match the convention.
3. Sanity gates before committing (fix by minimal edit, then proceed): sections are exactly Scope · Plan · Requirements · Validation · Testing depth · SPEC-AMEND repeat · Out of scope; decision numbering starts at GF-260 and every GF id is new (no reuse of GF-1…GF-259 for new decisions; cited reuse of older GFs is fine); no open decisions; the scope fence in Validation covers the planned mission §7 SPEC-AMEND row as the one sanctioned constitution edit (leave it as written — the base `geofences` table follows README §5 and D-26/D-33 are already registered amendments, so no tech-stack edit is planned).
4. Commit exactly: `git add specs/features/17-geofence-registry-drawing.md` and commit with message `Add P17 feature spec`. Do not commit anything else (do not `git add -A`).
5. Report: the commit hash, the spec's section list, the GF id range used, and `git status --short` output.

Out of scope: any other file, any code change, any git operation beyond the add/commit above, branch switches, merges.