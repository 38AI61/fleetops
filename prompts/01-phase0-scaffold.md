# Phase 0 — scaffold (auto mode)

Goal: write the Phase 0 scaffold for this repo (01-fleetops) and commit it. Work from the repo root (your cwd).

Context: `README.md` in the repo root is the binding product spec (esp. §10 Phase 0, §11 layout, §12 local dev). `specs/` does NOT exist yet — it is created in the next SDD step. Do not create specs/.

Write exactly these files:

1. `.gitignore` — ignore: node_modules, dist/build outputs, .venv/__pycache__, `.env` and `.env.*`, agent artifacts (`.claude/`), logs. Lockfiles ARE committed: do NOT ignore package-lock.json, pnpm-lock.yaml, yarn.lock, go.sum.
2. `CLAUDE.md` — under 200 lines, mechanical facts only: the host port mapping table (below), the compose command `docker compose -f deploy/docker-compose.yml`, env var names from README §12, and the pointer: "specs/ are the source of truth — read specs/ before non-trivial work (not yet present; created in the constitution step)". Nothing about mission/scope/roadmap.
3. `AGENTS.md` — short agent-rules file (<40 lines): specs/ are the binding source of truth; never hand-edit README.md; work per SDD (constitution + per-feature specs on their own branches); no secrets in the repo.
4. `deploy/docker-compose.yml` — skeleton ONLY, exactly these 4 services: postgres (image `postgis/postgis:16`), redis (image `redis:7`), nats (image `nats:2`), osrm (image `osrm/osrm-backend`). Skeleton = service definitions, host port bindings, named volumes, healthchecks, plus a comment marking where app services (api, ingest, ws-hub, minio, web) join later. Do NOT pull images, do NOT download OSRM data, do NOT start containers, do NOT run compose up.
5. `deploy/PORTS.md` — the full host-port allocation table (below), with a "reserved (future)" section.

Binding host-port allocation (every host port lives in 10000–10019):

| Service | Container port | Host port |
|---|---|---|
| postgres | 5432 | 10002 |
| redis | 6379 | 10003 |
| nats | 4222 | 10004 |
| osrm | 5000 | 10005 |
| minio (reserved, phase 3) | 9000 / 9001 | 10006 / 10007 |
| api (reserved, local go run) | 8080 | 10010 |
| ws-hub (reserved, local go run) | 8090 | 10011 |
| web dispatcher (reserved, vite dev) | 5173 | 10012 |
| driver-pwa (reserved, vite dev) | 5174 | 10013 |

Done when:
- `docker compose -f deploy/docker-compose.yml config -q` exits 0 (you run it, quote output).
- `wc -l CLAUDE.md` proves < 200 lines.
- `git add` the new files (including `prompts/01-phase0-scaffold.md` — it is provenance for this run) and commit ONCE with the imperative message `Scaffold Phase 0 config`. `git status` clean after.

Out of scope: any app code, specs/, CI files, image pulls, container starts, compose up, README edits, anything else.
Report: files written with line counts, the compose config check output, `git log --oneline -3`.