# CLAUDE.md

Mechanical reference for this repo. Product scope and design live in specs/ (not yet present; created in the constitution step).

**specs/ are the source of truth — read specs/ before non-trivial work (not yet present; created in the constitution step).**

## Host ports

Full allocation and reserved ports: `deploy/PORTS.md`. All host ports are in 10000–10019.

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

## Local infrastructure

Compose file: `deploy/docker-compose.yml`

```
docker compose -f deploy/docker-compose.yml up -d
docker compose -f deploy/docker-compose.yml down
```

## Environment variables

Names from README §12. Values come from the environment or `deploy/.env` (gitignored). Never commit values.

- `DATABASE_URL`
- `NATS_URL`
- `REDIS_URL`
- `OSRM_URL`
- `MINIO_ENDPOINT`
- `MINIO_KEY`
- `MINIO_SECRET`
- `JWT_SECRET`
- `DEVICE_KEY_SALT`
- `POSTGRES_PASSWORD` (read by the postgres service in compose; local-dev default applies if unset)
