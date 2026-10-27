#!/usr/bin/env bash
# Happy-path stack lifecycle (P16, GF-252..GF-254, GF-257).
# Usage: stack.sh up | down. Run from anywhere; paths resolve from this file.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
LOCAL="$ROOT/deploy/.local"
ENV_FILE="$LOCAL/e2e.env"
E2E_DIR="$LOCAL/e2e"
BIN_DIR="$E2E_DIR/bin"
LOG_DIR="$E2E_DIR/logs"
PID_FILE="$E2E_DIR/pids"
COMPOSE=(docker compose -f "$ROOT/deploy/docker-compose.yml" -f "$ROOT/e2e/compose.override.yml")
GO_BIN="${GO:-go}"
APP_PORTS=(10010 10011 10012 10013)

port_open() {
  (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null
}

wait_port() {
  local port=$1 deadline=$((SECONDS + 60))
  until port_open "$port"; do
    if [ "$SECONDS" -ge "$deadline" ]; then
      echo "stack: port $port did not answer within 60s (see $LOG_DIR)" >&2
      return 1
    fi
    sleep 1
  done
  echo "stack: port $port answering"
}

# Signals each recorded process group, then waits and escalates to SIGKILL.
# Process groups matter: npm runs vite as a child, so the npm PID alone is not enough.
stop_services() {
  [ -f "$PID_FILE" ] || return 0
  local pid alive
  while read -r pid; do
    [ -n "$pid" ] || continue
    kill -TERM -- "-$pid" 2>/dev/null || kill -TERM "$pid" 2>/dev/null || true
  done < "$PID_FILE"
  for _ in $(seq 1 20); do
    alive=0
    while read -r pid; do
      [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null && alive=1
    done < "$PID_FILE"
    [ "$alive" = 0 ] && break
    sleep 0.5
  done
  while read -r pid; do
    [ -n "$pid" ] || continue
    kill -KILL -- "-$pid" 2>/dev/null || kill -KILL "$pid" 2>/dev/null || true
  done < "$PID_FILE"
  rm -f "$PID_FILE"
}

# Starts a command in its own session and records the PID (the process-group leader).
# The PID is left in LAST_PID for the caller.
start_bg() {
  local name=$1
  shift
  setsid "$@" >"$LOG_DIR/$name.log" 2>&1 </dev/null &
  LAST_PID=$!
  echo "$LAST_PID" >> "$PID_FILE"
}

write_env() {
  local pg_pw="${POSTGRES_PASSWORD:-fleetops-dev}"
  local minio_key="${MINIO_KEY:-minioadmin}"
  local minio_secret="${MINIO_SECRET:-minioadmin}"
  local jwt_secret salt admin_pw
  jwt_secret="$(openssl rand -hex 32)"
  salt="$(openssl rand -hex 32)"
  admin_pw="$(openssl rand -hex 16)"
  umask 077
  cat > "$ENV_FILE.tmp" <<EOF
JWT_SECRET=$jwt_secret
DEVICE_KEY_SALT=$salt
E2E_ADMIN_EMAIL=e2e-admin@fleetops.test
E2E_ADMIN_PASSWORD=$admin_pw
DATABASE_URL=postgres://fleetops:$pg_pw@127.0.0.1:10002/fleetops?sslmode=disable
NATS_URL=nats://127.0.0.1:10004
REDIS_URL=redis://127.0.0.1:10003
MINIO_ENDPOINT=127.0.0.1:10006
MINIO_KEY=$minio_key
MINIO_SECRET=$minio_secret
EOF
  mv "$ENV_FILE.tmp" "$ENV_FILE"
}

down() {
  stop_services
  "${COMPOSE[@]}" down >/dev/null 2>&1 || true
  local busy=0 p
  for p in "${APP_PORTS[@]}"; do
    if port_open "$p"; then
      echo "stack: port $p is still listening after down" >&2
      busy=1
    fi
  done
  [ "$busy" = 0 ] && echo "stack: down; ports ${APP_PORTS[*]} free"
  return "$busy"
}

on_error() {
  trap - ERR
  echo "stack: up failed; tearing down" >&2
  down || true
  exit 1
}

up() {
  trap 'on_error' ERR
  mkdir -p "$BIN_DIR" "$LOG_DIR"
  stop_services

  write_env
  set -a
  # shellcheck source=/dev/null
  . "$ENV_FILE"
  set +a

  # Compose reads the same variables, so the database password and MinIO keys match the env file.
  export POSTGRES_PASSWORD="${POSTGRES_PASSWORD:-fleetops-dev}"
  export MINIO_KEY MINIO_SECRET

  "${COMPOSE[@]}" down -v
  "${COMPOSE[@]}" up -d --wait postgres redis nats minio

  for p in "${APP_PORTS[@]}"; do
    if port_open "$p"; then
      echo "stack: port $p is already in use; stop the owner and retry" >&2
      exit 1
    fi
  done

  (cd "$ROOT" && make migrate)

  printf '%s\n' "$E2E_ADMIN_PASSWORD" | (cd "$ROOT" && SEED_ARGS="--admin-email $E2E_ADMIN_EMAIL" make seed)

  (cd "$ROOT" && "$GO_BIN" build -o "$BIN_DIR/api" ./services/api)
  (cd "$ROOT" && "$GO_BIN" build -o "$BIN_DIR/ws-hub" ./services/ws-hub)
  (cd "$ROOT" && "$GO_BIN" build -o "$BIN_DIR/ingest" ./services/ingest)

  wait_port 10006
  start_bg api "$BIN_DIR/api" -addr 127.0.0.1:10010
  start_bg ws-hub "$BIN_DIR/ws-hub" -addr 127.0.0.1:10011
  start_bg ingest "$BIN_DIR/ingest"
  local ingest_pid=$LAST_PID
  start_bg web-dev npm --prefix "$ROOT/apps/web" run dev
  start_bg driver-pwa-dev npm --prefix "$ROOT/apps/driver-pwa" run dev

  # Ingest has no listener: it is ready when it is still alive after 5 s.
  sleep 5
  if ! kill -0 "$ingest_pid" 2>/dev/null; then
    echo "stack: ingest exited; see $LOG_DIR/ingest.log" >&2
    exit 1
  fi
  echo "stack: ingest alive"

  for p in "${APP_PORTS[@]}"; do
    wait_port "$p"
  done
  echo "stack: up; ports ${APP_PORTS[*]} answering"
}

case "${1:-}" in
  up) up ;;
  down) down ;;
  *)
    echo "usage: stack.sh up|down" >&2
    exit 2
    ;;
esac
