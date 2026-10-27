# Feature 12 — Proof of delivery (P12)

Status: approved plan, persisted 2026-10-27. Branch `phase-12-proof-of-delivery`.

Source plan: `/root/.claude/plans/p12-plan-sequential-blum.md` (approved, self-gate passed with GF-195 and GF-198 accepted). Binding constitution: `specs/constitution/roadmap.md` §1 (R-1…R-9, esp. R-6), §3 P12, `specs/constitution/tech-stack.md` §2, §4, §5, §7.1, §9, §10, §11 (D-07, D-25, D-23, D-24, D-37), `specs/constitution/mission.md` §7 (D-07 timing row, D-25 EXIF backlog row), `README.md` §2.1.5, §6, §7, §8, `specs/features/10-job-core.md`, `specs/features/11-driver-jobs.md`, `AGENTS.md`, `CLAUDE.md`.

This spec is written before any P12 code (R-1, SDD). Implementation starts at Plan task group G0.

## Scope

Roadmap P12 (proof of delivery), server side plus compose.

- MinIO compose service: container 9000 (S3) / 9001 (console) → host 10006 / 10007, bound to `127.0.0.1`, volume `minio_data`. Compose is rendered-checked only; it is never started by tests or validation.
- `proofs` bucket created idempotently at API boot. Boot refuses to start when the bucket cannot be ensured.
- `POST /driver/jobs/:id/deliver`: multipart with `photo` (JPEG/PNG/WebP, ≤ 5 MiB) plus `lat` / `lng`. Driver-only, own jobs only, valid only from `picked_up`.
- Object name `proofs/<fleet_id>/<job_id>/<uuid>.<ext>` (D-25), stored verbatim in `jobs.proof_photo`. The same UPDATE records `delivery_geom` and `delivered_at`.
- Every job object carries `proof_photo_url`, a presigned GET valid 15 min, signed on read.
- Role-matrix row for the new route (R-6).
- No migration. Migration 009 already has `jobs.proof_photo TEXT`, `jobs.delivered_at TIMESTAMPTZ`, `jobs.delivery_geom geography(Point,4326)`. Verified against `db/migrations/009_jobs_core.up.sql` lines 12–18.

## Plan

**G0 — Preflight.**
- `make fmt-check lint test` green on the phase branch before any change.
- `ls db/migrations` ends at `009_jobs_core`. Columns from 009 present (verified during planning).
- GF-193 is the highest existing decision id (verified: `specs/features/11-driver-jobs.md`).
- Verify `github.com/testcontainers/testcontainers-go/modules/minio` exists at v0.44.0 (`go list -m -versions`). If it does not, fall back to `testcontainers.GenericContainer` with the MinIO image, and record the fallback in the G1 commit message. Not a scope change.
- Verify the minio-go/v7 presign path is offline when a region is set (`Region: "us-east-1"`). Confirm from the pinned module source.
- Tests: none new. Pass condition: baseline green, no migration needed.

**G1 — `internal/proofs` store.** (new `internal/proofs/store.go`, `store_test.go`; new `internal/proofs/prooftest/minio.go`)
- `FromEnv(lookup func(string) string) (*Store, error)` reads `MINIO_ENDPOINT` (`host:port`), `MINIO_KEY`, `MINIO_SECRET`. Any empty value is an error that names the variable, never the value. Plain HTTP (no TLS in MVP). `internal/config` is not touched (GF-201).
- `Store` carries `PresignTTL time.Duration`, default 15 min when zero (GF-202).
- `EnsureBucket(ctx) error`: `BucketExists`, then `MakeBucket` in `us-east-1` if missing. Idempotent.
- `Put(ctx, fleetID, jobID int64, r io.Reader, size int64, ext string) (string, error)`: builds `proofs/<fleetID>/<jobID>/<uuid>.<ext>` with `uuid.NewString()` (google/uuid v1.6.0, already pinned), uploads to bucket `proofs`, returns the object name.
- `PresignGet(ctx, key string) (string, error)`: presigned GET, expiry `PresignTTL`.
- `prooftest`: exported test helper. A shared MinIO container per test binary (sync.Once), plus `TerminateMinIO()` in `TestMain`, mirroring `internal/testinfra/nats.go`. Holds the pinned image constant (GF-203).
- Tests: `TestProofsBucketIdempotent` (EnsureBucket twice; second is a no-op; an object put after the first call is listable and headable), `TestProofsPutPresignFetch` (Put builds the exact D-25 key; PresignGet GET returns 200 with the exact bytes; GET on a missing key fails), `TestProofsPresignExpiry` (PresignTTL 2 s; GET 200; poll until 403 with a 10 s deadline).

**G2 — Compose minio service.** (`deploy/docker-compose.yml`)
- Service `minio`: image `minio/minio:<same tag as prooftest constant>`, command `server /data --console-address ":9001"`, ports `127.0.0.1:10006:9000` and `127.0.0.1:10007:9001`, env `MINIO_ROOT_USER=${MINIO_KEY:-minioadmin}`, `MINIO_ROOT_PASSWORD=${MINIO_SECRET:-minioadmin}`, volume `minio_data:/data`, declared under top-level `volumes:` (GF-205).
- Header TODO line 6: `minio (… phase 3)` → `phase 12`. This is the stale-comment fix; it rides with the compose change.
- Tests: none at runtime. Pass condition: `docker compose -f deploy/docker-compose.yml config -q` exits 0. Nothing is started.

**G3 — Store: gate and deliver.** (`internal/jobs/driver.go`)
- `DriverDeliverGate(ctx, fleetID, jobID, driverID int64) (State, error)`: read-only. `SELECT state FROM jobs WHERE id=$1 AND fleet_id=$2 AND driver_id=$3`. No row → `ErrNotFound`. State `picked_up` or `delivered` → returned. Any other state → `ErrConflict`. (GF-197)
- `DriverDeliver(ctx, actor, fleetID, jobID, driverID int64, photoKey string, lat, lng float64) (Change, Outcome, error)`: `begin(ctx, actor)`, `selectJob(lock=true)`, ownership guard (GF-187) → `ErrNotFound`. State `delivered` → `Replayed`, no event, first key unchanged (D-24). State not `picked_up` → `ErrConflict`. Otherwise one UPDATE: `state='delivered', delivered_at=now(), proof_photo=$key, delivery_geom=ST_SetSRID(ST_MakePoint($lng,$lat),4326)::geography` (GF-194). Commit → `Applied`.
- The D-24 replay block is repeated, not refactored. `Transition`, `Assign`, `DriverTransition`, `begin`, `selectJob`, `appliedChange`, `mapWriteErr` stay untouched (GF-204).
- Tests: `TestDriverStoreDeliver` (`internal/jobs`, PostGIS testcontainers). Happy path writes state, delivery_geom, delivered_at, proof_photo in one tx and yields one `picked_up→delivered` event. Replay → `Replayed`, no event, first key unchanged. Wrong state → `ErrConflict`. Wrong driver and cross-fleet → `ErrNotFound`. Gate cases: `picked_up` → picked_up, `delivered` → delivered, `en_route` → `ErrConflict`, wrong driver → `ErrNotFound`. Event count per job matches.

**G4 — HTTP deliver route.** (new `internal/jobs/deliver.go`; `internal/jobs/http.go` gets the driver-group route; `services/api/server.go`; `services/api/main.go`)
- `jobs.ProofStore` interface: `Put(...)` and `PresignGet(...)` with the `proofs.Store` signatures. `Handler` gains a `Proofs ProofStore` field.
- Handler order for `POST /driver/jobs/:id/deliver`: role group (driver only; 403 before body read) → `idParam` (malformed → 404) → `Proofs == nil` → 503 `proofs_unavailable` → resolve driver (unlinked → 404) → `DriverDeliverGate` (404 / 409, all before upload and before body parse) → multipart parse (`photo`, `lat`, `lng`; missing → 400 `validation_failed`) → lat ∈ [-90, 90], lng ∈ [-180, 180], finite (NaN/Inf → 400) → read part with `io.LimitReader(max+1)`; more than 5 MiB → 413 `payload_too_large` → sniff MIME → ext → if gate state is `picked_up`, `Put`; if `delivered` (replay), skip upload → `Store.DriverDeliver` → `Applied` → `publishChange` after commit (GF-191); `Replayed` → no publish → 200 with the job object, `proof_photo_url` presigned (GF-196).
- Route-level `middleware.BodyLimit("8M")` on the deliver route only (GF-199).
- Global `BodyLimit` in `services/api/server.go` (`maxBodyBytes = "64K"`) gets a Skipper matching `POST` and path `^/driver/jobs/[0-9]+/deliver$`. Reason: it is registered with `e.Use`, so it runs for matched routes too. Without the Skipper, a per-route 8M limit does not lift the 64K cap. Every other route keeps the 64K global limit.
- `Deps` gains `Proofs *proofs.Store`. `NewServer` assigns the `jobs.ProofStore` field only when `d.Proofs != nil` (typed-nil trap, GF-201).
- `services/api/main.go` boot, after the existing NATS/EnsureStream steps: `proofs.FromEnv(os.Getenv)` → `EnsureBucket(ctx)` → `log.Fatalf` on either error (D-07 refuse-on-startup) → pass into `Deps`.
- Tests: `TestDriverDeliver` (services/api harness with MinIO; see Testing depth). `TestDriverDeliverValidation`. `TestDriverDeliverIsolation`. The harness hands the test a MinIO-backed store from `prooftest`.

**G5 — Presigned URLs on job responses.** (`internal/jobs/http.go`, new view helper in `internal/jobs/deliver.go`)
- A `jobView` wrapper = the GF-173 job object + `proof_photo_url`. Non-null iff `proof_photo` is non-null, signed on read (GF-196). `Store` results and the `Job` struct are unchanged, so store tests are not touched.
- Applied to every job-emitting response: dispatcher `POST /jobs`, `GET /jobs`, assign, cancel; driver `GET /driver/jobs/today`; every driver transition response; deliver.
- Tests: `TestJobProofURLs` (services/api, MinIO harness). A delivered job's `proof_photo_url` GET returns 200 with the exact bytes. Jobs without proof emit `proof_photo_url: null` on dispatcher list and driver today, and on create, assign and cancel. Existing P10/P11 response tests stay green.

**G6 — Role-matrix row (R-6).** (`services/api/rolematrix_test.go`)
- One row: `POST /driver/jobs/:id/deliver` → `[403,403,403,2xx,401]`. Fixture assigns the job to the matrix driver and advances it to `picked_up` (GF-206). The driver case sends a small valid JPEG part with `lat`/`lng`.
- Tests: `TestRoleMatrix` with the new row.

**G7 — CLAUDE.md rows (mechanical only).** (`CLAUDE.md`, currently 120 lines; must stay under 200)
- Deliver route and its multipart contract (fields, types, 5 MiB, sniffed MIME).
- `MINIO_ENDPOINT`, `MINIO_KEY`, `MINIO_SECRET` required at API boot; the `proofs` bucket is ensured at boot. The local API `go run` line gains these three variables.
- Presigned-URL rule: 15 min, signed on read, never stored.
- Per-route 8M body limit on deliver, global 64K for everything else.
- No new env var names beyond the three already listed in the Environment section, no new make targets, no new `FLEETOPS_*` tunable.
- Tests: none. Pass condition: `wc -l CLAUDE.md` < 200.

**G8 — Full validation run.** See Validation.

## Requirements

### Binding (carried from the constitution and earlier specs)

- **D-07** (tech-stack §11; mission §7): MinIO added to compose in Phase 12 (not Phase 0), ports 10006 / 10007, bound to 127.0.0.1; bucket created idempotently at API boot; cloud photo storage deferred.
- **D-25** (tech-stack §11): delivery proof: `deliver` requires multipart photo (JPEG/PNG/WebP, ≤ 5 MB) plus lat/lng; object key `proofs/<fleet_id>/<job_id>/<uuid>.<ext>`; EXIF is not stripped in MVP (accepted risk, listed in backlog).
- **D-23** (tech-stack §11): the job state machine. P12 uses the `picked_up→delivered` edge only.
- **D-24** (tech-stack §11): driver transitions are idempotent; a replay returns the current job with no event and no publish.
- **D-37** (tech-stack §11): testcontainers-go MinIO module in integration tests; no mocks of infra; `-race` on the packages under test.
- **R-6** (roadmap §1): any phase that adds an endpoint extends the §8 role-matrix test.
- **Mission §7** D-07 row: timing deviation, MinIO in P12 not Phase 0, recorded. The D-25 EXIF row: accepted risk, backlog.
- **Reused decisions** (cite, do not restate): GF-170 (job column shapes incl. `proof_photo`, `delivery_geom`), GF-171 (trigger semantics; non-assignment columns may change alongside a state change), GF-173 (job object shape), GF-178 (error mapping), GF-179 (publish), GF-180, GF-184 (driver identity), GF-187 (ownership guard), GF-188 (driver endpoint contracts), GF-190 (store shape; repeat the D-24 block), GF-191 (driver publish, `driver_id` non-null) — all from `specs/features/10-job-core.md` and `specs/features/11-driver-jobs.md`.

### New decisions (GF-194 onward)

- **GF-194 — Deliver writes four columns in one UPDATE.** The deliver UPDATE sets `state='delivered'`, `delivered_at=now()`, `proof_photo=<key>`, `delivery_geom=ST_SetSRID(ST_MakePoint(lng,lat),4326)::geography`, all in one statement inside one tx. *Why:* trigger `jobs_transition_guard` (009 lines 76–118) only checks assignment and state, and writes an event only on state or assignment change, so non-assignment columns ride along (GF-171). README §2.1.5 says coordinates are captured at delivery. P10 left the writer to P12. `delivered_at` uses the DB `now()`, the same clock the trigger's transition runs on.
- **GF-195 — Object naming.** Bucket `proofs` (D-07). Object name = the full D-25 string `proofs/<fleet_id>/<job_id>/<uuid>.<ext>`, stored verbatim in `jobs.proof_photo`. *Why:* D-25 is literal and the stored string is the object name, so no translation layer can drift. The bucket name repeats as an object-name prefix (`proofs/proofs/…` inside the bucket). Accepted as cosmetic.
- **GF-196 — `proof_photo_url` on every job object.** New GF-173 field: `proof_photo_url` (string or null). Non-null iff `proof_photo` is non-null. Signed on read, 15 min, never stored. Emitted on every job-emitting endpoint (G5 list). `delivery_geom` is not emitted (no consumer until P13+; GF-173 precedent of not emitting unwired columns). When `Proofs` is nil the field is null. *Why:* one rule keeps every consumer correct; signing on read keeps URLs fresh and avoids a stored secret.
- **GF-197 — Pre-upload gate.** `DriverDeliverGate` (read-only) runs before any body parse and before any upload. It returns 404 (wrong driver, cross-fleet, malformed id, unlinked) or 409 (state not `picked_up`/`delivered`) with nothing stored. *Why:* the roadmap requires 404 and 409 with no object stored, and the in-tx check alone would upload first. The gate is a pre-check; the in-tx `DriverDeliver` re-validates under lock.
- **GF-198 — Upload before tx; concurrent loser orphan.** The upload happens in the handler before `DriverDeliver`. If two drivers race, the loser's tx sees `delivered` and replays with the winner's key. The loser's object is an orphan. No cleanup in MVP (accepted). A loser that lands on a non-`picked_up` state returns 409 and also leaves an orphan.
- **GF-199 — Size and MIME enforcement.** Two layers. (a) Route-level `middleware.BodyLimit("8M")` on the deliver route, plus a global Skipper for that path only; oversized total request → 413 from echo. (b) Handler (authoritative): part size ≤ `5 * 1024 * 1024` = **5,242,880 bytes**; part read with `io.LimitReader(max+1)`, so more than the limit → 413 `payload_too_large`. MIME is sniffed by `http.DetectContentType` on the first 512 bytes; the declared Content-Type is ignored. `image/jpeg` → `jpg`, `image/png` → `png`, `image/webp` → `webp`. Anything else → 400 `validation_failed`. *Why:* the declared type is client-controlled; sniffing is the trust boundary. 413 is a deliberate extension of the GF-178 vocabulary, matching echo's BodyLimit status. The 503 `proofs_unavailable` is a defensive extension, unreachable in production.
- **GF-200 — Deliver contract.** Multipart fields: `photo` (file), `lat` and `lng` (text, parsed with `strconv.ParseFloat`, NaN and Inf rejected). lat ∈ [-90, 90], lng ∈ [-180, 180] (GF-173 point ranges). Success → 200 with the job object (GF-196). Applied → one `job.updated` after commit with `driver_id` non-null (GF-191). Replay → 200 with no event, no publish, no upload. Check order: role → id → proofs nil → gate → body validation → upload → tx. Body validation runs on replay too: a replay must still carry a valid photo, which is then not stored. *Why:* keeps the replay contract uniform and avoids a second code path.
- **GF-201 — Proofs package shape and wiring.** New package `internal/proofs` owns MinIO config: `FromEnv` reads `MINIO_*` directly, so `internal/config` is untouched. `jobs.ProofStore` is a consumer-side interface. `Deps.Proofs` is a concrete `*proofs.Store`. `NewServer` assigns the interface only when non-nil. *Why:* avoids the typed-nil interface trap, and keeps `jobs` free of a `proofs` import. Plain HTTP, `Region: "us-east-1"`.
- **GF-202 — Short-TTL test hook.** `Store.PresignTTL` (field, default 15 min). `TestProofsPresignExpiry` sets 2 s. No env var, no `FLEETOPS_*` tunable (D-22 list untouched). README fixes 15 min. *Why:* the field is the one hook the store needs; an env var would leak a test knob into production config.
- **GF-203 — Testcontainer helper location.** `internal/proofs/prooftest` (not `internal/proofs` itself). Shared MinIO container per test binary, `TerminateMinIO()` in `TestMain`, mirroring `internal/testinfra/nats.go`. The image is a pinned constant shared with compose. *Why:* keeps testcontainers out of the production API binary; `internal/proofs/prooftest` stays inside the `internal/proofs/` scope fence. GF-8's `internal/testinfra` stays for PostGIS/NATS/Redis.
- **GF-204 — Store shape.** New methods `DriverDeliverGate` and `DriverDeliver` only, in `internal/jobs/driver.go`. `Transition`, `Assign`, `DriverTransition` and helpers are reused or untouched. The D-24 block is repeated (GF-190 precedent). *Why:* the P10/P11 path stays green without edits.
- **GF-205 — Compose service.** Ports, binding, volume as in Scope. Credentials default to `minioadmin` when `MINIO_KEY`/`MINIO_SECRET` are unset, matching the postgres local-dev default. Compose is never started here. The stale "phase 3" comment is corrected. *Why:* compose renders without a `deploy/.env` present.
- **GF-206 — Role-matrix row fixture.** One row per R-6 (G6). The driver case sends a valid multipart body; the 403 cases send no body (role check runs first). *Why:* exercises the real 2xx path, not a shortcut.
- **GF-207 — CLAUDE.md.** Mechanical rows only (G7). No new env var names beyond the three already listed, no new make targets, no migrations. File stays under 200 lines. *Why:* CLAUDE.md is the mechanical reference; decisions live in this spec.

## Validation

Runnable on this host. Pass conditions are explicit. Compose is never started.

1. `make fmt-check` → exit 0.
2. `make lint` → exit 0.
3. `make test` → exit 0 (`go test ./... -race -count=1`; PostGIS, NATS and MinIO testcontainers run; Docker available).
4. `go test ./internal/proofs/... -race -count=1 -run 'TestProofs' -v` → all of `TestProofsBucketIdempotent`, `TestProofsPutPresignFetch`, `TestProofsPresignExpiry` report PASS.
5. `go test ./internal/jobs/... -race -count=1 -run 'TestDriverStoreDeliver' -v` → PASS.
6. `go test ./services/api/... -race -count=1 -run 'TestDriverDeliver|TestJobProofURLs|TestRoleMatrix' -v` → PASS, with `TestDriverDeliver`, `TestDriverDeliverValidation`, `TestDriverDeliverIsolation`, `TestJobProofURLs` each reported PASS by name.
7. `docker compose -f deploy/docker-compose.yml config -q` → exit 0. Renders config only; nothing is started.
8. Scope fence: `git diff --name-only main...HEAD` touches only `internal/jobs/`, `internal/proofs/`, `services/api/`, `deploy/docker-compose.yml`, `CLAUDE.md`, `specs/features/12-proof-of-delivery.md`, `prompts/`, `go.mod`, `go.sum`. Must NOT touch `README.md`, `deploy/PORTS.md`, `specs/constitution/`, `db/`, `internal/hub/`, `internal/config/`, `internal/testinfra/`, `apps/`. Empty result for the forbidden set.
9. `ls db/migrations | sed 's/\..*//' | sort -u | tail -1` → `009_jobs_core`.
10. `wc -l CLAUDE.md` → under 200.
11. `git diff main...HEAD` contains no credentials beyond the documented `minioadmin` local defaults in compose and the test container.

## Testing depth

| Layer | Tool | What is proven | Tests |
|---|---|---|---|
| Integration: MinIO (`internal/proofs`) | testcontainers MinIO via `prooftest` | Bucket idempotent; upload; exact D-25 key; presigned fetch 200 with exact bytes; missing key fails; presigned URL works then expires (403) | `TestProofsBucketIdempotent`, `TestProofsPutPresignFetch`, `TestProofsPresignExpiry` |
| Integration: PostGIS (`internal/jobs`) | testcontainers PostGIS (testinfra) | Happy path in one tx: state, delivery_geom, delivered_at, proof_photo, one event; replay; wrong state; ownership; cross-fleet; gate | `TestDriverStoreDeliver` |
| Services API harness (MinIO + PostGIS + NATS) | `services/api` harness | Multipart deliver 200; object fetched via presigned URL; key recorded; one `picked_up→delivered` event; one NATS `job.updated` with `driver_id`; replay: 200, no new event, no second upload, same key | `TestDriverDeliver` |
| Services API harness: rejections | same | Oversize (5 MiB + 1 byte) → 413, nothing stored; wrong MIME → 400; missing photo/lat/lng → 400; lat 91 / lng 181 → 400; `en_route` → 409, nothing stored; malformed id → 404; non-driver → 403 before body read | `TestDriverDeliverValidation` |
| Services API harness: isolation | same | Driver A on driver B's job → 404, nothing stored, event count unchanged; cross-fleet → 404 | `TestDriverDeliverIsolation` |
| Services API harness: proof URLs | same | Delivered job's `proof_photo_url` GET → 200; `null` on jobs without proof on every job-emitting endpoint | `TestJobProofURLs` |
| Role matrix (R-6) | integration table test | Deliver row `[403,403,403,2xx,401]` | `TestRoleMatrix` (new row) |
| Race | `go test -race` | All packages under test | `make test` |

Migration round trip stays green unchanged (`db/roundtrip_test.go` untouched; no new migration).

## SPEC-AMEND repeat

P12 adds **no new SPEC-AMEND row**. Two existing facts are repeated here, not new:

- **D-07 timing deviation**: MinIO is added in P12, not Phase 0. Recorded in `specs/constitution/mission.md` §7 (D-07 row). Unchanged.
- **D-25 EXIF stripping**: deferred. Photos are stored as received. This is an accepted risk, kept in the backlog (`specs/constitution/roadmap.md` §4 table, "EXIF stripping on delivery photos"). P12 does not change it.

## Out of scope

- Driver PWA and offline queue (P14, P15).
- Playwright (P16).
- Hub changes, including the GF-192 identity binding (P13, P23). No `internal/hub/` changes.
- `db/` migrations.
- `internal/config` and `internal/testinfra` changes.
- `deploy/PORTS.md` edits (minio row already reserved, Phase 12).
- EXIF stripping (D-25 accepted risk).
- `README.md`, constitution, and any compose behavior beyond the minio service.
- Starting compose. Merge to `main`. Push.
- Any P13+ content.
