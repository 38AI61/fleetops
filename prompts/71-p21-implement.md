# P21 implement — build the feature from its spec (pointer)

Goal: implement roadmap phase P21 (Daily reports & CSV) of this repo on branch `phase-21-daily-reports-csv`, driven entirely by the spec.

The complete instruction is the union of: `specs/constitution/constitution.md` (binding rules, R-1…R-9) and `specs/features/21-daily-reports-csv.md` (the approved feature spec — Scope, Plan groups G0–G6 in order, Requirements incl. decisions GF-319…GF-328, Validation checks a–k).

Rules of engagement:
- Work task-group by task-group in the spec's build order; one commit per task group (house style: `Add <what> (Gn)`).
- Each group's tests are part of the group; run the group's tests before committing it.
- Done when every validation check (a)–(k) in the spec passes, run by you; record the results in the spec's "Validation results and deviations" section (append, do not rewrite existing sections) and record any deviation like the P18/P19/P20 specs' Deviations lists. If any check cannot run on this host, record NOT RUN with the reason.
- Known flake: `internal/hub` — an isolated re-run (`go test ./internal/hub -race -count=1`) before failing anything.
- `make e2e` must NOT be run (stack.sh does `docker compose down -v`; record the reason under (d)).
- The spec's "Decision for the user to confirm" (GF-324 dispatcher reading) is resolved by default for this run: implement the fleet-wide dispatcher read exactly as GF-324 states it; do not change README or the role matrix.
- Web work (G4) uses only existing dependencies; run `npm --prefix apps/web run typecheck`, `npm --prefix apps/web test`, `npm --prefix apps/web run build` as part of the group and the final validation (a).
- CLAUDE.md (G5): the file is 197 lines today; the new block pushes it over the < 200 cap, so trim existing content with no loss of facts as P20 did (commit `23a651d` precedent) — do not delete facts to make room silently; the trim is part of G5.
- Out of scope: next phases (P22+ content — dashboards, `/metrics` export, OTel/Grafana, k6, demo seed), README.md edits, merge to main, push, compose changes, `e2e/` changes, `apps/driver-pwa/**` changes, `internal/jobs/**` / `internal/eta/**` / `internal/alerts/**` / `internal/geofence/**` changes, `internal/simulator/**` changes, any schema beyond `db/migrations/012`.
- Report: files per task group, commands + outputs for the validation checks, git log.

Out of scope for this run: writing outside the repo except the spec's validation-results append; any git operation beyond commits on this branch.