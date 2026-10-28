# Host port allocation

Every host port used by FleetOps lives in the range **10000–10019**. Do not bind any other host port in this project. Container-internal ports are unaffected.

Infrastructure services bind to `127.0.0.1` only.

## Active (Phase 0 — `deploy/docker-compose.yml`)

| Service | Container port | Host port |
|---|---|---|
| postgres | 5432 | 10002 |
| redis | 6379 | 10003 |
| nats | 4222 | 10004 |
| osrm | 5000 | 10005 |

## Reserved (future)

| Service | Container port | Host port | Phase / runtime |
|---|---|---|---|
| minio | 9000 / 9001 | 10006 / 10007 | Phase 12, compose |
| api | 8080 | 10010 | local `go run` |
| ws-hub | 8090 | 10011 | local `go run` |
| web dispatcher | 5173 | 10012 | local Vite dev |
| driver-pwa | 5174 | 10013 | local Vite dev |
| ingest metrics | — (`-metrics-addr`) | 10008 | local `go run`, `127.0.0.1` only |

## Observability (Phase 22 — `deploy/docker-compose.yml`, host network)

| Service | Container port | Host port | Runtime |
|---|---|---|---|
| prometheus | host network (`--web.listen-address=127.0.0.1:10014`) | 10014 | compose, `127.0.0.1` |
| grafana | host network (`GF_SERVER_HTTP_PORT=10015`) | 10015 | compose, `127.0.0.1` |

Unassigned in range: 10000, 10001, 10009, 10016–10019.
