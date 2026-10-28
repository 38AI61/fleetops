# Phase P24 — Cloud deployment profile (README §15) — CHILD BRIEF

## Mission
Implement the cloud deployment profile for FleetOps so the merged single-container
build deploys to Render (free), with Neon as Postgres+PostGIS and Cloudflare Pages
serving the React UIs. README §15 is the spec; the backlog row "Cloud deployment
profile (README §15, D-41)" is released by the user's deploy order today.

## Repo & standards
- Repo: /root/fullstack-portfolio/01-fleetops (work here; clean tree discipline;
  feature branch `phase-24-cloud-profile` built on main; the standard SDD loop:
  plan prompt → implement prompt → persist prompt, fresh claude -p session per
  step, pointer prompts in prompts/, ledger STATE.md phase row P24).
- Consent-free rule (binding): NEVER run anything that can trigger a permission
  card. Single-purpose terminal calls, no `set -e` bundles, no curl|sh, no writes
  to system paths, no interactive prompts. Tests: `make fmt-check lint test`
  must pass before merge.
- Never commit secrets. Env NAMES only in docs (CLAUDE.md D-40 rule).
- Go 1.27.3 at /root/.local/go/bin/go; docker available.

## Verified facts (from my own reads — trust these)
- D-41 seam: `internal/fanout` has `Fanout` interface (PublishPositions,
  PublishPresence), `RedisFanout`, `Nop`. The hub (internal/hub) subscribes
  Redis PSUBSCRIBE `positions.batch.*`, `presence.changed.*` and NATS core
  `jobs.events.*`, `alerts.*`. Ingest publishes via `Publisher.Publish`
  (GF-103) after commit.
- MinIO is boot-required by the API (services/api/main.go ~line 63, GF-201):
  requires MINIO_ENDPOINT/KEY/SECRET, ensures `proofs` bucket, refuses boot
  otherwise. The cloud container MUST run MinIO (embedded) — §15's table
  omits it, but the boot requirement wins; note this in the phase spec.
- Ports (PORTS.md): api 10010, ws-hub 10011, ingest metrics 10008 (all
  127.0.0.1-bindable). Render exposes ONE port → the container needs a
  single-port front (reverse proxy inside the container, e.g. a tiny Go
  proxy or echo middleware forwarding /stream WS to ws-hub) — decide in plan.
- Neon project `frosty-hall-37378198` is LIVE and already migrated (version
  12, 22 tables, PostGIS 3.3.10). Do NOT run migrations against it again in
  tests; the connection string lives at
  /root/.local/tmp/deploy-probes/neon-uri.txt (use it ONLY for the local
  cloud-shape smoke run; never print it, never commit it).
- GitHub: origin remote exists (38AI61/fleetops, token in env
  GITHUB_API_KEY — use it only in git push, never echo). After the phase
  merges to main, `git push origin main` (identity already configured).
- OSRM cloud: https://router.project-osrm.org (D-10: ETA rules degrade,
  accepted). OSRM_URL env for the cloud profile.

## Deliverables
1. `specs/features/24-cloud-profile.md` phase spec (scope fence, binding
   decisions carried verbatim, validation).
2. Merged container: Dockerfile (e.g. deploy/cloud/Dockerfile.render) running
   embedded single-node NATS + embedded MinIO + api + ingest + ws-hub under a
   supervisor; single exposed port. Config via env only.
3. In-process fanout for single-node mode (env-gated, e.g. FANOUT_MODE):
   implement behind the D-41 seam so ingest→hub happens in-process without
   Redis; keep RedisFanout default for local compose. The hub+ingest wiring
   must work in one container.
4. CI: .github/workflows/ci.yml — fmt+lint+test on PR/main; docker build +
   push to GHCR (public image ghcr.io/38AI61/fleetops) on main; then trigger
   Render deploy hook via secret RENDER_DEPLOY_HOOK_URL (workflow reads the
   secret; the URL itself is set by the orchestrator later).
5. Cloud seeding: non-interactive path to seed the demo fleet (admin email +
   password via env/args, GF-340 rerun-safe unchanged) — extend
   tools/seeddemo or make target without breaking the interactive one.
6. Frontend deploy artifacts: Vite production builds for apps/web and
   apps/driver-pwa with configurable API base; a Cloudflare Pages Functions
   proxy (functions/[[path]].js or per-route) forwarding REST /api/* and WS
   /stream to the Render URL via env, so the browser only ever talks to the
   Pages origin (no CORS changes needed) — decide exact shape in plan.
7. docs/cloud.md runbook: architecture of the merged container, env var
   table (names + which service consumes), deploy steps, cold-start note,
   limits vs local (50 vehicles target, no 2k burst), rollback.
8. CLAUDE.md: add cloud-profile run lines + new env names (FANOUT_MODE etc.),
   keep under 200 lines (D-40).
9. Tests the plan defines (in-process fanout property + integration; container
   smoke). Local cloud-shape smoke BEFORE merge: run the merged container on
   this host with DATABASE_URL=Neon URI + embedded NATS/MinIO + FANOUT_MODE
   in-process; verify /healthz (add one if missing), login, /map/vehicles,
   simulator (tools/simulator) streaming to the container's ingest endpoint
   and positions appearing via the WS /stream. Then `make migrate` MUST NOT
   run again against Neon (idempotent; skip).
10. Merge to main (one --no-ff merge), then `git push origin main`.

## Budget & reporting
- Cap each claude -p step: --max-budget-usd 2.5 (plan/implement/persist).
- Log spend per step to deploy/.local/p24-<step>.json (existing pattern).
- Final summary: what shipped, test results, smoke evidence, commit hashes,
  total spend.