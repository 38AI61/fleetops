# P18 implement — build the feature from its spec (pointer)

Goal: implement roadmap phase P18 (Geofence evaluator) of this repo on branch `phase-18-geofence-evaluator`, driven entirely by the spec.

The complete instruction is the union of: `specs/constitution/constitution.md` (binding rules, R-1…R-9) and `specs/features/18-geofence-evaluator.md` (the approved feature spec — Scope, Plan groups G0–G5 in order, Requirements incl. decisions GF-278…GF-289, Validation checks a–j).

Rules of engagement:
- Work task-group by task-group in the spec's build order; one commit per task group (house style: `Add <what> (Gn)`).
- Each group's tests are part of the group; run the group's tests before committing it.
- Done when every validation check (a)–(j) in the spec passes, run by you; record the results in the spec's "Validation results and deviations" section (append, do not rewrite existing sections) and record any deviation like the P17 spec's Deviations list. Check (k)-style manual browser checks do not exist in this phase; if any check cannot run on this host, record NOT RUN with the reason.
- Known flake: `internal/hub` — an isolated re-run (`go test ./internal/hub -race -count=1`) before failing anything.
- Out of scope: next phases (P19+ content — alert rules, ack, alert fanout, Notifier), README.md edits, merge to main, push, compose/PORTS changes, `e2e/` and `apps/` changes.
- Report: files per task group, commands + outputs for the validation checks, git log.

Out of scope for this run: writing outside the repo except the spec's validation-results append; any git operation beyond commits on this branch.