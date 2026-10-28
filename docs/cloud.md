# Cloud profile runbook (P24)

The cloud profile runs the API, ingest, ws-hub, NATS and MinIO in one container image on Render, reads and writes Neon Postgres, and serves the two React apps from Cloudflare Pages. Local compose is unchanged and stays the default for development.

## Architecture

One image, `ghcr.io/38ai61/fleetops:latest` (and `sha-<commit>`), built from `deploy/cloud/Dockerfile.render`. Its entrypoint is `fleetops-cloud` (GF-348 to GF-350).

```
Render container (one instance, port PORT=8000 from Render)
  fleetops-cloud (supervisor, PID 1)
    front proxy   0.0.0.0:$PORT
                    /stream          -> ws-hub 127.0.0.1:10011
                    /metrics*        -> 404 (loopback only, GF-329)
                    everything else  -> api   127.0.0.1:10010  (includes /healthz)
    nats-server   127.0.0.1:4222   in-process, JetStream, store /data/nats
    minio         127.0.0.1:9000   child, store /data/minio, console 9001
    fleetops-api      child, 127.0.0.1:10010
    fleetops-ingest   child (GPS_RAW drain, geofences, alerts, ETA, rollup)
    fleetops-ws-hub   child, 127.0.0.1:10011
```

Children restart with a 1 s to 30 s backoff. NATS and the proxy exit the container on failure. Logs go to stdout and stderr only, prefixed per child.

Fanout inside the container is `FANOUT_MODE=nats` (GF-347). Redis is not used, so `REDIS_URL` must not be set on Render.

Pages serves `apps/web` and `apps/driver-pwa`. Pages Functions proxy the API paths to the Render origin, so REST calls and cookies stay on the Pages origin (GF-352). The WebSocket goes through the Pages `/stream` function.

## Environment

Names only. Values never go in git, docs, or logs.

| Name | Consumer | Notes |
|---|---|---|
| `DATABASE_URL` | api, ingest, seed, `make migrate` | Neon URI. Secret. |
| `JWT_SECRET` | api, ingest, seed | At least 32 bytes. Secret. |
| `DEVICE_KEY_SALT` | api, ingest, seed, simulator (through device keys) | At least 32 bytes. Secret. Must match the salt used by the seed that wrote `devices.json`; a mismatch makes ingest return 401 (GF-50). |
| `MINIO_KEY`, `MINIO_SECRET` | supervisor (MinIO root user and password), api (proof presign) | `MINIO_KEY` at least 3 bytes, `MINIO_SECRET` at least 8 bytes. Secret. |
| `FANOUT_MODE` | supervisor, ws-hub, ingest, api | Image sets `nats`. `redis` is the default outside the image. |
| `REDIS_URL` | api, ingest, ws-hub in `redis` mode | Required only when `FANOUT_MODE=redis`. Unset on Render. |
| `PORT` | front proxy | Set by Render. Default 8000 in the image. |
| `WS_ORIGIN_PATTERNS` | ws-hub | Optional, comma-separated `coder/websocket` patterns. Must include the Pages host, for example `fleetops.pages.dev`. Unset uses the defaults in `internal/hub/hub.go`. |
| `OSRM_URL` | ingest (ETA) | Image default is `https://router.project-osrm.org`. Public demo router, D-10 `eta_slip` degradation. |
| `SEED_PASSWORD` | `make cloud-seed` (`--password-from-env`) | Operator workstation only. Never set on Render. Secret. |
| `SMOKE_BASE`, `SMOKE_ADMIN_EMAIL`, `SMOKE_PASSWORD` | `make cloud-smoke` | Operator workstation only. `SMOKE_PASSWORD` is secret. |
| `UPSTREAM_ORIGIN` | Pages Functions (runtime env) | Render origin, `https://<render-host>`, no trailing slash. |
| `VITE_WS_BASE` | web build (optional) | `wss://<render-host>`. Fallback if the Pages WebSocket upgrade check fails after deploy (GF-352). |
| `RENDER_DEPLOY_HOOK_URL` | GitHub Actions `deploy` job | GitHub secret. The `deploy` job skips with a notice when it is unset. |
| `GHCR` login | GitHub Actions `image` job | `GITHUB_TOKEN`, no stored secret. |

## Deploy steps

**Neon.** A fresh project: `DATABASE_URL=… make migrate` (applies every migration in `db/migrations`). Then check `select version||'|'||dirty from schema_migrations` returns `12|f`. The existing version-12 project runs no migrate. P24 adds no migrations. Never run `make migrate` with `down` against Neon.

**Operator seed.** From a workstation with `DATABASE_URL` and `DEVICE_KEY_SALT` (the same salt the container will use):

```
SEED_ARGS="--admin-email <admin-email>" SEED_PASSWORD=… make cloud-seed
```

This writes `deploy/.local/devices.json` and `deploy/.local/demo-users.json` on the workstation (0600, gitignored). Nothing is uploaded. A rerun on a seeded database exits 0 with "already seeded; no changes" and reads no password.

**Render.** Create an image service from `ghcr.io/38ai61/fleetops:latest`. Set instances to 1. Health check path `/healthz`. Set `DATABASE_URL`, `JWT_SECRET`, `DEVICE_KEY_SALT`, `MINIO_KEY`, `MINIO_SECRET`, `WS_ORIGIN_PATTERNS`, and `OSRM_URL` if not the default. Do not set `REDIS_URL`. Copy the deploy hook URL into the GitHub secret `RENDER_DEPLOY_HOOK_URL`. The GHCR package must be public for Render to pull it without credentials; set that once after the first push (manual).

**Pages.** Two projects. Root directory `apps/web` and `apps/driver-pwa`. Build command `npm --prefix apps/<app> ci --include=dev && npm --prefix apps/<app> run build`. Output `dist`. Runtime env `UPSTREAM_ORIGIN`. Optional build env `VITE_WS_BASE`.

**CI.** On push to `main`, after `gates` and `e2e` pass, the `image` job builds and pushes `ghcr.io/38ai61/fleetops:sha-<commit>` and `:latest`. The `deploy` job then POSTs the Render deploy hook. PRs run `gates` and `e2e` only.

## Post-deploy checklist

1. `GET https://<render-host>/healthz` returns 200 `{"status":"ok"}` (cold starts can take about a minute).
2. Log in through the Pages URL. The map loads.
3. The refresh cookie survives a page reload on the Pages origin.
4. The WebSocket through Pages `/stream?fleet=<id>` connects and receives `position.batch` frames while the simulator runs. If the upgrade fails, set `VITE_WS_BASE` for the web build and redeploy Pages.
5. Proof photo URLs are not browser-reachable from Render (GF-350). Treat photo viewing as a known limit, not a deploy failure.

To run the smoke harness against a container you started locally:

```
docker build -f deploy/cloud/Dockerfile.render -t fleetops:cloud-smoke .
docker run -d --name fleetops-cloud-smoke --env-file <env-file> -e PORT=8000 -p 127.0.0.1:10019:8000 fleetops:cloud-smoke
set -a; . <env-file>; set +a
SMOKE_BASE=http://127.0.0.1:10019 SMOKE_ADMIN_EMAIL=<admin-email> SMOKE_PASSWORD="$SEED_PASSWORD" make cloud-smoke
```

`make cloud-smoke` checks `/healthz`, login, `/map/vehicles`, runs the simulator for 90 s against the container's ingest, and counts `position.batch` frames on WS `/stream`. It prints one line ending `result=PASS` or `result=FAIL`, and exits 0 only on PASS. It never prints a token or password.

## Cold start

Render free instances spin down when idle, and the first request can take about a minute. Neon autosuspends too, so the first query after idle can be slow. `/healthz` is liveness only (no database or NATS probe, GF-353), so Render does not restart the instance during a Neon cold start.

## Limits

- About 50 vehicles on the demo fleet. The single instance is the supported shape.
- No 2,000 msg/s burst on the cloud profile. Burst tests stay local (README §14, P23 load tests).
- `/data` is ephemeral on Render free. A restart or redeploy drops the JetStream buffer, MinIO data (proof photos), and presence state. Positions are durable in Neon.
- Proof photos are stored in MinIO inside the container. Their presigned URLs point at the container's loopback MinIO and are not reachable from a browser (GF-350).
- Routing uses the public OSRM demo. ETA is best effort; `eta_slip` degrades when the demo router is slow or rate-limited (D-10).

## Rollback

- Render: redeploy the previous image tag, `ghcr.io/38ai61/fleetops:sha-<commit>`, in the service settings.
- Pages: roll back to the previous deployment in the Pages project.
- Database: none. P24 adds no migrations, so no schema rollback is needed. Do not run `migrate down` against Neon.
