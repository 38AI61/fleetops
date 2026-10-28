# P18 persist — write the feature spec from the approved plan (pointer)

Goal: write `specs/features/18-geofence-evaluator.md` in this repo from the approved plan at `/root/.claude/plans/p18-plan-rustling-moonbeam.md` (the newest `p18-plan-*.md` in `/root/.claude/plans/`), then commit it. Nothing else.

Context: you are the persist step of roadmap phase P18 (Geofence evaluator) of this repo. The plan was produced by a plan-mode run and is approved. Persist it verbatim as the feature spec.

1. Find the plan: the newest file matching `/root/.claude/plans/p18-plan-*.md`. If none exists, stop and report that — do not guess or reconstruct.
2. Write its content verbatim to `specs/features/18-geofence-evaluator.md` (this repo's spec convention: title `# Feature 18 — Geofence evaluator (P18)`, then the `Status:` and `Source plan:` header lines exactly like `specs/features/17-geofence-registry-drawing.md` lines 1–4 — copy that header shape, updating phase number, branch `phase-18-geofence-evaluator`, and today's date; leave the plan's own `Status:` line if it duplicates the header, keeping one Status line total).
3. Sanity gates before committing (fix by minimal edit, then proceed): sections are exactly Scope · Plan · Requirements · Validation · Testing depth · SPEC-AMEND repeat · Out of scope; decision numbering starts at GF-278 and every GF id is new (no reuse of GF-1…GF-277 for new decisions; cited reuse of older GFs is fine); no open decisions; the scope fence sanctions no constitution edit (no mission §7 SPEC-AMEND row is planned — D-33 and the P17 trigger-timing row already cover P18; a migration would only appear via the GF-280 stop rule, which needs user approval and is out of scope for this persist).
4. Commit exactly: `git add specs/features/18-geofence-evaluator.md` and commit with message `Add P18 feature spec`. Do not commit anything else (do not `git add -A`).
5. Report: the commit hash, the spec's section list, the GF id range used, and `git status --short` output.

Out of scope: any other file, any code change, any git operation beyond the add/commit above, branch switches, merges.