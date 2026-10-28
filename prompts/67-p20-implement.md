# P20 implement — build the feature from its spec (pointer)

Goal: implement roadmap phase P20 (ETA slip & OSRM) of this repo on branch `phase-20-eta-slip-osrm`, driven entirely by the spec.

The complete instruction is the union of: `specs/constitution/constitution.md` (binding rules, R-1…R-9) and `specs/features/20-eta-slip-osrm.md` (the approved feature spec — Scope, Plan groups G0–G7 in order, Requirements incl. decisions GF-303…GF-318, Validation checks a–l).

Rules of engagement:
- Work task-group by task-group in the spec's build order; one commit per task group (house style: `Add <what> (Gn)`).
- Each group's tests are part of the group; run the group's tests before committing it.
- Done when every validation check (a)–(l) in the spec passes, run by you; record the results in the spec's "Validation results and deviations" section (append, do not rewrite existing sections) and record any deviation like the P18/P19 specs' Deviations lists. If any check cannot run on this host, record NOT RUN with the reason.
- Known flake: `internal/hub` — an isolated re-run (`go test ./internal/hub -race -count=1`) before failing anything.
- The real `prepare.sh` run against a PBF is operator-manual (D-10): record it NOT RUN with the reason; `sh -n` and the grep/mld checks are the runnable part.
- `make e2e` must NOT be run (stack.sh does `docker compose down -v`; record the reason under (d)).
- Out of scope: next phases (P21+ content — reports, dashboards, /metrics export, k6), README.md edits, merge to main, push, compose changes, `e2e/` changes, `apps/web/**` and `apps/driver-pwa/**` changes, `internal/jobs/**` and `internal/geofence/**` changes, `db/migrations/` additions, `internal/simulator/**` changes.
- Report: files per task group, commands + outputs for the validation checks, git log.

Out of scope for this run: writing outside the repo except the spec's validation-results append; any git operation beyond commits on this branch.