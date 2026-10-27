# P16 persist — write the feature spec from the approved plan (pointer)

Goal: write `specs/features/16-playwright-happy-path.md` in this repo from the approved plan at `/root/.claude/plans/p16-plan-melodic-church.md` (the newest `p16-plan-*.md` in `/root/.claude/plans/`), then commit it. Nothing else.

Context: you are the persist step of roadmap phase P16 (Playwright happy path) of this repo. The plan was produced by a plan-mode run and is approved. Persist it verbatim as the feature spec.

1. Find the plan: the newest file matching `/root/.claude/plans/p16-plan-*.md`. If none exists, stop and report that — do not guess or reconstruct.
2. Write its content verbatim to `specs/features/16-playwright-happy-path.md` (this repo's spec convention: title `# Feature 16 — Playwright happy path (P16)`, then the `Status:` and `Source plan:` header lines exactly like `specs/features/15-driver-pwa-offline-queue.md` lines 1–7 — copy that header shape, updating phase number, branch `phase-16-playwright-happy-path`, and today's date). If the plan text already carries the title/header, keep the plan's own title but make the header lines match the convention.
3. Sanity gates before committing (fix by minimal edit, then proceed): sections are exactly Scope · Plan · Requirements · Validation · Testing depth · SPEC-AMEND repeat · Out of scope; decision numbering starts at GF-247 and every GF id is new (no reuse of GF-1…GF-246 for new decisions; cited reuse of older GFs is fine); no open decisions; the scope fence in Validation does not touch `internal/`, `services/`, `db/`, `apps/web/`, `apps/driver-pwa/`, `README.md`, compose, `PORTS.md` (the planned mission §7 + tech-stack §11 SPEC-AMEND rows are the one sanctioned constitution edit and are already inside the plan's fence — leave them as written).
4. Commit exactly: `git add specs/features/16-playwright-happy-path.md` and commit with message `Add P16 feature spec`. Do not commit anything else (do not `git add -A`).
5. Report: the commit hash, the spec's section list, the GF id range used, and `git status --short` output.

Out of scope: any other file, any code change, any git operation beyond the add/commit above, branch switches, merges.