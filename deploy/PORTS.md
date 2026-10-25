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

Unassigned in range: 10000, 10001, 10008, 10009, 10014–10019.
