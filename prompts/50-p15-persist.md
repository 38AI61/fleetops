# P15 persist — write the feature spec from the approved plan (pointer)

Goal: write `specs/features/15-driver-pwa-offline-queue.md` in this repo from the approved plan at `/root/.claude/plans/p15-plan-hazy-finch.md` (the newest `p15-plan-*.md` in `/root/.claude/plans/`), then commit it. Nothing else.

Context: you are the persist step of roadmap phase P15 (driver PWA offline queue) of this repo. The plan was produced by a plan-mode run and is approved. Persist it verbatim as the feature spec.

1. Find the plan: the newest file matching `/root/.claude/plans/p15-plan-*.md`. If none exists, stop and report that — do not guess or reconstruct.
2. Write its content verbatim to `specs/features/15-driver-pwa-offline-queue.md` (this repo's spec convention: title `# Feature 15 — Driver PWA offline queue (P15)`, then the `Status:` and `Source plan:` header lines exactly like `specs/features/14-driver-pwa-online.md` lines 1–7 — copy that header shape, updating phase number, branch `phase-15-driver-pwa-offline-queue`, and today's date). If the plan text already carries the title/header, keep the plan's own title but make the header lines match the convention.
3. Sanity gates before committing (fix by minimal edit, then proceed): sections are exactly Scope · Plan · Requirements · Validation · Testing depth · SPEC-AMEND repeat · Out of scope; decision numbering starts at GF-236 and every GF id is new (no reuse of GF-1…GF-235 for new decisions; cited reuse of older GFs is fine); no open decisions; scope fence in Validation does not touch `internal/`, `services/`, `db/`, `apps/web/`, `README.md`, compose, PORTS.md.
4. Commit exactly: `git add specs/features/15-driver-pwa-offline-queue.md` and commit with message `Add P15 feature spec`. Do not commit anything else (do not `git add -A`).
5. Report: the commit hash, the spec's section list, the GF id range used, and `git status --short` output.

Out of scope: any other file, any code change, any git operation beyond the add/commit above, branch switches, merges.