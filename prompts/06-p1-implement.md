# P1 — implement (acceptEdits)

Goal: implement roadmap phase P1 exactly per the committed feature spec, on the current branch `phase-1-toolchain`.

Context:
- Read `specs/features/01-toolchain-ci-migrations.md` FIRST — it is the complete instruction: task groups T1–T8 in build order, decisions GF-5…GF-18, validation checks V0–V9, testing depth. Also read `specs/constitution/roadmap.md` §3 P1 for the binding scope. This prompt only points; the spec governs.
- T0 is already done: the feature spec itself is committed (`fef3fc4`). Do NOT rewrite it. Start at T1 and work in order.
- Tool reality: use the exact host paths listed in the spec's "Verified facts" (`/root/.local/go/bin/go`, `/root/.local/bin/golangci-lint`, `/root/.local/bin/migrate`); none are on PATH. Write `deploy/.local/tools.mk` per T5 (gitignored after T1 adds `deploy/.local/`).
- Docker: `docker pull` and testcontainers run freely on this host (docker 29.1.3). The `postgis/postgis:16-3.4` image is already pulled locally.
- Commit after each task group with imperative messages (e.g. `Add Go module and repo hygiene`, `Add baseline migrations`, ...). Keep the `Co-Authored-By: Claude Haiku 5.5 <noreply@anthropic.com>` trailer you used in the spec commits. Never commit `deploy/.local/`.

Research: if testcontainers-go's current API differs from what you remember, investigate in a subagent and apply the summary; do not paste long docs into this session.

Done when: every validation check V0–V9 in the spec passes when YOU run it — quote each command and its output in your report. V0 preflight comes first; if gcc or docker is missing, stop and report instead of forcing.

Out of scope: anything not in the phase spec (no P2+ work, no auth/users/seed logic/endpoints, no compose or README edits, no PORTS.md edits beyond the GF-15 MinIO text fix, no pushing, no merge to main).

Report: files per task group, every command + output for V0–V9, final `git log --oneline` for the branch, and `git status` (must be clean).