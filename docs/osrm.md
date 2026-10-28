# OSRM operator prep (P20, D-10, D-39)

The `osrm` compose service serves routes from an `osrm_data` volume. The volume is empty on a fresh clone. Routing data is an operator step: it is never in the repo and never built in CI (GF-247).

## 1. Get a regional extract

- Download a Geofabrik extract for your region from <https://download.geofabrik.de/> (for example `europe/germany-latest.osm.pbf`, or a smaller sub-region).
- Do not use a continent extract. The target output is **200–500 MB** on disk (roadmap §13, README §13). Pick a region that fits that range.
- Save the file into `deploy/osrm/data/`. That directory is gitignored (`deploy/osrm/.gitignore`).

## 2. Prepare the data

From the repo root, with Docker running:

```sh
sh deploy/osrm/prepare.sh <pbf-file-name>
```

`<pbf-file-name>` is the plain file name inside `deploy/osrm/data/` (no path, letters, digits, `.`, `_` and `-` only).

The script runs four steps in one-shot containers from the same image as compose:

1. Copy the PBF into `osrm_data` as `region.osrm.pbf`.
2. `osrm-extract -p /opt/car.lua` (car profile).
3. `osrm-partition`.
4. `osrm-customize`. This matches the `mld` algorithm in compose, so no `--uploaded` flag.

It then asserts that `region.osrm.mldgr`, `.cells`, `.cell_metrics` and `.partition` exist.

- **Idempotent:** if `region.osrm.mldgr` is already in the volume, the script exits 0 and does nothing.
- **Force:** `sh deploy/osrm/prepare.sh --force <pbf-file-name>` re-runs the pipeline.
- **Image:** the default is `osrm/osrm-backend`. Override it with `OSRM_IMAGE=...`.
- **Volume name:** the script uses `<project>_osrm_data`. The project defaults to `deploy`, the name of the compose directory. If you set `COMPOSE_PROJECT_NAME` for compose, set the same value for the script.

Then start the service:

```sh
docker compose -f deploy/docker-compose.yml up -d osrm
```

## 3. Record the image digest

Record the digest of the image you prepared with, so a later re-run can be matched to it:

```sh
docker image inspect --format '{{index .RepoDigests 0}}' osrm/osrm-backend
```

Put the output in your operator notes. Do not commit it to the repo.

## 4. Point the API at it

`OSRM_URL` defaults to `http://127.0.0.1:10005`, the compose port. Set it only if the server is elsewhere. It must be an `http` or `https` URL. A bad value stops the process at boot, and the error names the variable only.

## Cloud degradation

A cloud deployment can set `OSRM_URL` to the public demo server (D-10). The demo server has no availability guarantee and the repo has no fallback. When OSRM is unreachable, times out or returns no route, the ETA loop skips the job. It writes no ETA, raises no `eta_slip` alert, and increments `fleetops_eta_rule_skipped_total{reason="osrm_unavailable"}` (GF-314). Dispatchers see no ETA slip in that case, so an empty alert list does not prove that jobs are on time.
