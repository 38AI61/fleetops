# P19 re-plan — Alerts core (plan-mode pointer, second pass)

Goal: revise your previously written plan; do not execute it. Do not write any file except the plan itself.

Context: you (a previous plan-mode session) wrote `/root/.claude/plans/p19-plan-tranquil-avalanche.md` for roadmap phase P19 (Alerts core). Read that file first — it is the base. The driver self-gate found ONE blocking deviation from the repo's binding tooling pins; everything else in the plan is approved as-is. Revise the plan file in place (same path, full rewrite with the corrected content), changing ONLY what the fix requires.

The blocking deviation — **G5 property test tooling violates D-37**:

- `specs/constitution/tech-stack.md` §11 **D-37** pins `pgregory.net/rapid` for property tests. The repo convention (see `internal/geofence/debounce_test.go`, `internal/ingest/buffer_property_test.go`, `internal/jobs/transition_property_test.go`) is `rapid.Check` with a **fresh seed per run** (no fixed seed anywhere); a failure prints the seed for replay (`-rapid.seed=<n>`), and every failing seed is then added as a fixed regression case before the fix lands.
- The plan's G5 uses `math/rand/v2` with `const silenceSeed = 0x19` and 500 fixed sequences. That contradicts the pin and the seed policy.
- Revise G5 (`internal/alerts/silence_property_test.go`) to use `rapid.Check`: generator draws random report-gap/tick-gap sequences (same model as GF-292), assertions unchanged (at most one alert per episode key; alerted keys equal the oracle set; each alert fires at the first observing tick after the threshold; a new report re-arms). Keep the hand-written deterministic streams as separate plain tests if you want fixed coverage under `-count=1` — that matches the P18 pattern (three fixed streams alongside the property).
- Update every downstream mention of the old seed policy: the Validation (f) determinism check, the Testing depth property row, and any seed wording in GF-292/G5. Validation (f) becomes: `go test ./internal/alerts -run <PropertyTestName> -race -count=1` passes twice in a row with a fresh seed each run.
- Note in GF-292 or G5 that rapid draws a fresh seed per run and a failing seed is added as a regression case (one sentence, P18 G3 style).

Do not change anything else: keep GF-290…GF-302, the task groups, the scope fence, the role matrix, all other validation checks exactly as approved.

Produce the complete revised plan content for `specs/features/19-alerts-core.md` with sections exactly: Scope · Plan · Requirements · Validation · Testing depth · SPEC-AMEND repeat · Out of scope, and overwrite `/root/.claude/plans/p19-plan-tranquil-avalanche.md` with it.

Out of scope for this re-plan: writing any other file, code, git operations. No P20+ content.

Report: a one-paragraph summary of exactly what changed relative to the previous plan.