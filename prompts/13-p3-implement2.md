# P3 implement, part 2 — finish task groups 7–15 (auto-mode pointer)

Goal: finish implementing roadmap phase P3 exactly per the committed feature spec. A previous session completed task groups 1–6 (migrations 005–006; config `DEVICE_KEY_SALT` + rapid pin; `internal/devicekey`; license normalize/hash; registry stores; pairing service) and committed them. Your job: groups 7–15 only.

Context: read `specs/features/03-registry-pairing.md` — it is the complete instruction. Groups 7–15 are: 7 pairing-invariant property test (`pgregory.net/rapid`; note `internal/testinfra/database.go` already has an uncommitted helper refactor in the worktree from the previous session — review it, keep or improve it, do not redo groups 1–6); 8 registry HTTP handlers; 9 `users.driver_id` linkage; 10 apierr codes; 11 role-matrix extension; 12 fleet-scoping extension; 13 audit tests; 14 API wiring + CLAUDE.md; 15 validation run. Also read `specs/constitution/roadmap.md` §3 P3, `AGENTS.md`, `CLAUDE.md`.

Git: the worktree has intentional uncommitted state from the killed session (`internal/testinfra/database.go` modified, `internal/registry/pairing_property_test.go` new, untracked). Make it yours first: review, finish, and commit group 7 as one commit. Then commit each later task group with an imperative message. Branch is `phase-3-registry-pairing`; do not merge or push.

Research: if the `pgregory.net/rapid` API is unclear, use a subagent to investigate and report a summary only.

Host facts: Go at `/root/.local/go/bin/go`, golangci-lint at `/root/.local/bin/golangci-lint` (wired via `deploy/.local/tools.mk`); Docker available for testcontainers; `make test` runs `-race`.

Done when: every Validation item (1–14) in the feature spec passes, run by you on this host, command + outcome quoted in your report. Fix failures before finishing; if a spec item proves wrong, stop that item and report it — do not silently deviate.

Out of scope: groups 1–6 redo; anything not in the phase spec (no ingest endpoint, no device-key HTTP auth, no `last_seen_at` updates, no NATS, no positions, no map, no WS, no jobs, no geofences, no alerts; no compose/README/PORTS.md edits, no MinIO, no Redis, no merge, no push). The group 14 CLAUDE.md update is in scope (mechanical, < 200 lines).

Report: files per task group, every validation command + output, final `git log --oneline` for the branch, deviations flagged.