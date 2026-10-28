# P19 implement — build the feature from its spec (pointer)

Goal: implement roadmap phase P19 (Alerts core) of this repo on branch `phase-19-alerts-core`, driven entirely by the spec.

The complete instruction is the union of: `specs/constitution/constitution.md` (binding rules, R-1…R-9) and `specs/features/19-alerts-core.md` (the approved feature spec — Scope, Plan groups G0–G9 in order, Requirements incl. decisions GF-290…GF-302, Validation checks a–k).

Rules of engagement:
- Work task-group by task-group in the spec's build order; one commit per task group (house style: `Add <what> (Gn)`).
- Each group's tests are part of the group; run the group's tests before committing it.
- Done when every validation check (a)–(k) in the spec passes, run by you; record the results in the spec's "Validation results and deviations" section (append, do not rewrite existing sections) and record any deviation like the P17/P18 specs' Deviations lists. If any check cannot run on this host, record NOT RUN with the reason.
- Known flake: `internal/hub` — an isolated re-run (`go test ./internal/hub -race -count=1`) before failing anything.
- Out of scope: next phases (P20+ content — eta_slip evaluator, OSRM, reports, dashboards, /metrics export, k6), README.md edits, merge to main, push, compose/PORTS changes, `e2e/` and `apps/driver-pwa/` changes, any `internal/geofence/` change.
- Report: files per task group, commands + outputs for the validation checks, git log.

Out of scope for this run: writing outside the repo except the spec's validation-results append; any git operation beyond commits on this branch.