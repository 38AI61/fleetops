# P5 implement follow-up — task group 11 (auto-mode pointer)

Goal: implement task group 11 of `specs/features/05-batch-writer.md` (the REAL storage floor, GF-85). This is a focused follow-up: one behavior change plus its tests.

Context: read `specs/features/05-batch-writer.md` task group 11 and GF-85; also `internal/ingest/validate.go` and `internal/ingest/validate_test.go` (the existing `TestValidatePosition` table from P4, bounds in GF-55). The rule: `Validate` rejects a non-zero value whose absolute magnitude is below `1e-45` on `speed`, `heading`, or `accuracy` with a `FieldError` on that field; `0`, `1.4e-45`, and `1e-6` are accepted. Also update the `realistic` snap comment in `internal/ingest/writer_property_test.go` to point at GF-85.

Method: make the change, extend the `TestValidatePosition` table with a sub-micro case per field (rejected) and an accepted `1.4e-45` case, commit with an imperative message naming GF-85.

Host facts: Go at `/root/.local/go/bin/go`; golangci-lint at `/root/.local/bin/golangci-lint` (Makefile wires them). Run `go test ./internal/ingest -run 'TestValidatePosition' -v -count=1`, then `make fmt-check` and `make lint`. Quote the outputs in your report.

Done when: validation item 15 of the spec passes (run by you, quoted). Out of scope: anything else — no other P4/P5 file, no spec edits, no merge, no push.

Report: the diff summary, the test output, and the commit hash.