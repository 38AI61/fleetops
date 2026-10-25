# P2 — implement (acceptEdits)

Goal: implement roadmap phase P2 exactly per the committed feature spec, on the current branch `phase-2-auth-core`.

Context:
- Read `specs/features/02-auth-core.md` FIRST — it is the complete instruction: task groups 1–15 in build order, decisions D-01…D-06/GF-2/GF-5/GF-14 + GF-19…GF-29, validation items 1–12, testing depth. Also read `specs/constitution/roadmap.md` §3 P2 for the binding scope. This prompt only points; the spec governs.
- The feature spec itself is already committed (`49713c5 Add P2 feature spec`). Plan group 15's spec part is done — do NOT rewrite it. Start at task group 1 and work in order. Group 15's CLAUDE.md update IS implement scope.
- Tool reality: use the paths wired through `deploy/.local/tools.mk` (`GO=/root/.local/go/bin/go`, `GOLANGCI_LINT=/root/.local/bin/golangci-lint`, `MIGRATE=/root/go/bin/migrate`); none are on PATH. `gcc` is present (`-race` works). Docker 29.1.3 runs card-free; `postgis/postgis:16-3.4` is cached locally.
- Container policy on this host: do NOT run `docker compose up` in any form. Testcontainers (via `go test`) and, for the manual curl/psql validation checks, a plain `docker run` PostGIS dev container (P1 precedent: `docker run -d --name fleetops-p2-pg -e POSTGRES_USER=fleetops -e POSTGRES_PASSWORD=fleetops-dev -e POSTGRES_DB=fleetops -p 127.0.0.1:10002:5432 postgis/postgis:16-3.4`) are both fine. Remove the dev container when done.
- Secrets: generate `JWT_SECRET` per shell (`openssl rand -hex 32`); never write a secret to any file, never commit one.
- Commit after each task group with imperative messages (e.g. `Add auth migrations 002-004`, `Add config and password hashing`, ...), keeping the `Co-Authored-By: Claude Haiku 5.5 <noreply@anthropic.com>` trailer used by the spec commit. Never commit `deploy/.local/`.

Research: if a library's current API differs from what you remember (echo v4, golang-jwt/v5, golang-migrate iofs, x/crypto argon2, x/term), investigate in a subagent and apply the summary; do not paste long docs into this session.

Validation: when task groups 1–14 are done, run every validation item 1–12 from the spec and quote each command + its outcome in your report. Items 3–10 use the dev container + curl/psql (or the integration tests where the spec names them as the gate). Item 12's grep must show zero P3+ paths.

Done when: every validation item 1–12 passes run by YOU, with commands + outcomes quoted; `git status` clean; every task group committed.

Out of scope: anything not in the phase spec (no P3+ work — no vehicles/drivers/devices, no device keys, no pairing, no ingest, no WS, no job tables; no compose edits, no README edits, no PORTS.md edits, no merge to main, no push). Migration numbering continues from 001; do not touch 001.

Report: files per task group, every validation command + outcome for items 1–12, final `git log --oneline` for the branch, `git status` (must be clean).