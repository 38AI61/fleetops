# P8 implement-2 — finish P8 after the GF-128/GF-134 spec amendment (auto-mode pointer)

Goal: bring the existing P8 implementation on this branch into conformance with the amended feature spec, then re-run every validation item.

Context: read `specs/features/08-map-api-simulator.md` — it is the complete instruction (task groups G0–G6, requirements GF-126…GF-151, validation items 1–14). It was amended by commit 3fabbdc: **GF-128** now pins a planar predicate (`geom::geometry && ST_MakeEnvelope(...)` plus `ST_Covers(...)` refinement) because the old geography-typed predicate was proven broken for wide and whole-world boxes (antipodal-edge error, silent misclassification); **GF-134** now requires migration `008_vehicles_latest_planar_gix` (up: `CREATE INDEX vehicles_latest_geom_planar_gix ON vehicles_latest USING GIST ((geom::geometry));` down: drop it). Also read `specs/constitution/roadmap.md` §3 P8 and `AGENTS.md`, `CLAUDE.md`. Those files are the source of truth; this prompt only points.

What already exists on this branch (do not redo; verify and adjust): map read API (`internal/mapview`, wired in `services/api`), devices file package (`internal/devicesfile`), demo seed (`internal/seed/demo.go`, `tools/seed --demo-devices`), simulator (`internal/simulator`, `tools/simulator`), e2e tests, CLAUDE.md rows, all tests green at commit 276d5dc. Validation items 1–9 and 12–14 passed in the first run; item 8 must be re-pointed at the new index name; item 10's fence and item 11 are changed by the amendment.

Work:
1. Add migration `008_vehicles_latest_planar_gix.up.sql` / `.down.sql` exactly per GF-134 (GF-5 rules: sequential after 007, no BEGIN/COMMIT, no CONCURRENTLY).
2. Change the `internal/mapview` store query to the GF-128 amended predicate (planar `&&` + `ST_Covers` refinement, same params and cap semantics).
3. Update `TestBBoxIndexScan` to assert the `vehicles_latest_geom_planar_gix` index scan, and extend the bbox fixture integration tests so a whole-world bbox (`-180,-90,180,90`) and a wide box (span > 90° longitude) return correct results (add cases; keep existing ones).
4. Follow the migration rules in CLAUDE.md for the round-trip (`make migrate MIGRATE_ARGS="down 1"` / `up` needs DATABASE_URL — in tests, `db/migrations/roundtrip_test.go` and `testinfra.NewMigratedDB` already apply all migrations, which covers 008; extend if a 008-specific assertion is cheap).
5. Commit per task group with imperative messages (one commit for the migration, one for the predicate + tests is fine). Never fold prompts/ or specs/ into implementation commits.

Host facts: Go at `/root/.local/go/bin/go`, golangci-lint at `/root/.local/bin/golangci-lint` (wired via `deploy/.local/tools.mk`), migrate CLI at `/root/go/bin/migrate`; Docker available for testcontainers (postgis/postgis:16-3.4 cached); compose NOT started.

Done when: every Validation item (1–14) in the amended feature spec passes, run by you on this host, with the command and its outcome quoted in your report. Fix failures before finishing; if an item proves wrong or ambiguous, stop it and report — do not silently deviate.

Out of scope: anything not in the amended phase spec (no P9/P10/P22 work; no README/compose/PORTS/constitution edits; no new env names; no go.mod changes; no merge, no push; never write any plaintext device key outside `deploy/.local/` test-scoped temp files).

Report: files changed per work item, every validation command + its output, final `git log --oneline` for the branch, and any deviations flagged explicitly.