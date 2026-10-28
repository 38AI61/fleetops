# P23 implement — Load, tuning & demo

The binding constitution and the feature spec are the complete instruction. Read them first and follow exactly:

- `specs/constitution/roadmap.md` §1 (R-1…R-9), §3 **P23**, §5 (the P23 gate rows)
- `specs/constitution/tech-stack.md` §3, §4, §5, §11 (**D-22**, **D-37**)
- `specs/constitution/mission.md` §7 and the demo bullet (§51)
- `AGENTS.md` and `CLAUDE.md` (current rules and limits; CLAUDE.md stays under 200 lines)
- `specs/features/23-load-tuning-demo.md` — the feature spec: Scope, Plan (task groups G0–G6 in order), Requirements (D-22/D-37 verbatim + GF-337…GF-346 with rationales), Validation (checks (a)–(j)), Testing depth, SPEC-AMEND repeat, Out of scope.

Execution rules:

1. Work on the current branch `phase-23-load-tuning-demo` only. Do not merge, do not push, do not touch `main`.
2. Implement the task groups in the spec's Plan order (G0 preflight → G1 seed-demo + demo drivers → G2 10k hub race test → G3 GF-192 close (publish-side mapping) → G4 k6 scripts + Makefile entry + static validation → G6 tuning record + `docs/demo.md` + CLAUDE.md rows + full validation). G5 is a recording note only (GF-192 closes in G3).
3. Commit per task group with a `+ <verb>`-style conventional message naming the group (match the P22 commit style: `Add …`, `Fix …`), scope fenced exactly by the spec's Validation check (b). The G6 tuning record and validation results are appended to the spec file itself and committed.
4. Every test named in the spec's Plan groups must exist and pass. Use the exact test names the spec pins.
5. G2: run `TestScaleConnections` at 10,000 with `-race` via `make hub-scale` (add the target). If the race detector goroutine ceiling aborts the process, follow the pre-declared fallback in the spec (record the abort, binary-search the largest passing N under `-race` in steps of 1000, run 10k without `-race` for full-count evidence) and record it as a deviation — do not silently drop the `-race` requirement.
6. G4: the five k6 scripts are deliverables only — k6 is NOT installed on this host and must NOT be executed. Validate statically via the Go tests the spec pins (`TestK6ScriptsHaveThresholds`, `TestK6ScriptsBalanced`, `TestK6ScriptsParseWithNode`; skip the node case with a logged reason if node is absent).
7. Validation: run every check (a)–(j) from the spec's Validation section yourself. Append a `## Validation results and deviations` section at the end of `specs/features/23-load-tuning-demo.md` with per-check results, command outputs, and any deviations (the P22 spec's results section is the format precedent — read its tail). Records must be honest: NOT RUN with standing reasons where a check cannot run on this host.
8. CLAUDE.md: mechanical rows only (replace the `make seed-demo` stub line, add the load/demo block); file must stay under 200 lines (193 today). Trim in the P20/P21 style if needed.
9. No changes outside the spec's scope fence. No README edits. No migrations. No `.github/`, `apps/**`, `e2e/**`, `deploy/docker-compose.yml`, `internal/config/**`, `internal/auth/**` changes. No new env vars. No SPEC-AMEND rows (the plan resolved: none owed).
10. Done when every validation check passes or is recorded NOT RUN with its standing reason, the full `make test` suite is green (known `internal/hub` flake policy: isolated re-run before failing anything), and the tree is clean.

Out of scope: next phases (none — P23 is the last roadmap phase), README, merge, push, k6 execution, cloud profile, frontend changes.

Report (final message): files per task group, the commands you ran with their outcomes (including the 10k race result or fallback record), the CLAUDE.md line count, the git log of the branch, and any deviations.