# P22 implement — build the feature from its spec (pointer)

Goal: implement roadmap phase P22 (Observability) of this repo on branch `phase-22-observability`, driven entirely by the spec.

The complete instruction is the union of: `specs/constitution/` (binding rules: `mission.md`, `roadmap.md` §1 R-1…R-9, `tech-stack.md`) and `specs/features/22-observability.md` (the approved feature spec — Scope, Plan groups G0–G8 in order, Requirements incl. decisions GF-329…GF-336, Validation checks a–k).

Rules of engagement:
- Work task-group by task-group in the spec's build order; one commit per task group (house style: `Add <what> (Gn)`).
- Each group's tests are part of the group; run the group's tests before committing it.
- Toolchain on this host: Go at `/root/.local/go/bin`, golangci-lint at `/root/.local/bin`, migrate at `/root/go/bin` — prepend to PATH for make targets; no installs needed.
- Known flake: `internal/hub` — an isolated re-run (`go test ./internal/hub -race -count=1`) before failing anything.
- `make e2e` and any `docker compose up` / live Prometheus or Grafana scrape must NOT be run (driver standing rules; validation (d) records the reason; compose verification is `docker compose config -q` only).
- `apps/web` and `apps/driver-pwa` are NOT touched: record their baselines in G0 per the spec (run the npm checks only if node_modules exists; otherwise record NOT RUN with the reason).
- `go.mod`/`go.sum` changes are expected (otel sdk + otlptracehttp at v1.44.0 per GF-334/GF-16); no other dependency changes.
- The `internal/jobs/http.go` (+ its tests) span placement is the spec's fence exception (GF-335) — allowed. The `specs/constitution/mission.md` §7 SPEC-AMEND row for GF-332 (host networking) is part of this run per the spec's SPEC-AMEND repeat — append that one row, nothing else in specs/constitution/.
- Done when every validation check (a)–(k) in the spec passes, run by you; record the results in the spec's "Validation results and deviations" section (append, do not rewrite existing sections) and record any deviation like the P19/P20/P21 specs' Deviations lists. If any check cannot run on this host, record NOT RUN with the reason.
- CLAUDE.md (G7): the file is 193 lines today; if the new rows push it to ≥ 200, trim existing content with no loss of facts as P20/P21 did (`23a651d`, `4331024` precedents) — the trim is part of G7; keep `wc -l` < 200.
- Out of scope: next phases (P23+ content — k6, demo seed, demo script, 10k WS race), README.md edits, merge to main, push, any migration or schema change, any new metric beyond the README §9 set plus the GF-331 WS gauge, tracing beyond the assign flow, compose changes beyond the two service blocks, `e2e/` changes, `apps/web/**` / `apps/driver-pwa/**` changes.
- Report: files per task group, commands + outputs for the validation checks, git log.

Out of scope for this run: writing outside the repo except the spec's validation-results append; any git operation beyond commits on this branch.