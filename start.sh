#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BACKEND_DIR="$ROOT_DIR/backend"
FRONTEND_DIR="$ROOT_DIR/frontend"

# Backend variables with a plain dev default, in NAME=default form. This list is the
# single source for both the defaults applied below and the environment handed to the
# backend process, so a new variable is added in one place.
BACKEND_VAR_DEFAULTS=(
  "TOKENHUB_ENV=dev"
  "TOKENHUB_HTTP_ADDR=:8080"
  "TOKENHUB_PUBLIC_BASE_URL=http://localhost:8080"
  "TOKENHUB_RELEASE_REPOSITORY=astaxie/TokenHub"
  "TOKENHUB_TRUSTED_PROXY_CIDRS="
  "TOKENHUB_PROVIDER_UPSTREAM_ALLOWED_CIDRS="
  "TOKENHUB_CORS_ALLOWED_ORIGINS=http://localhost:3000"
  "TOKENHUB_ADMIN_TOKEN=dev_admin_token"
  "TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD=admin123456"
  "TOKENHUB_SECRET_KEY=dev_tokenhub_secret_key"
  "TOKENHUB_UPSTREAM_NON_STREAM_TIMEOUT_SECONDS=120"
  "TOKENHUB_UPSTREAM_STREAM_IDLE_TIMEOUT_SECONDS=300"
  "TOKENHUB_IN_FLIGHT_LEASE_TTL_SECONDS=300"
  "TOKENHUB_CLUSTER_LOCK_TTL_SECONDS=180"
  "TOKENHUB_GRACEFUL_SHUTDOWN_SECONDS=150"
  "TOKENHUB_CACHE_AFFINITY_ENABLED=false"
  "TOKENHUB_CACHE_AFFINITY_MODELS="
  "TOKENHUB_CACHE_AFFINITY_ALLOW_USER_SCOPE=false"
  "TOKENHUB_IMAGE_STORAGE_DIR=$ROOT_DIR/backend/data/images"
  "TOKENHUB_IMAGE_WORKER_CONCURRENCY=2"
  "TOKENHUB_IMAGE_QUEUE_CAPACITY=64"
  "TOKENHUB_IMAGE_JOB_TIMEOUT_SECONDS=300"
  "TOKENHUB_IMAGE_CAPABILITY_RETRY_SECONDS=86400"
)

# Equivalent to NAME="${NAME:-default}": a value already in the shell wins, and an
# empty one falls back to the default just as the parameter expansion did.
for var_entry in "${BACKEND_VAR_DEFAULTS[@]}"; do
  var_name="${var_entry%%=*}"
  if [ -z "${!var_name:-}" ]; then
    printf -v "$var_name" '%s' "${var_entry#*=}"
  fi
done

# The variables below stay out of BACKEND_VAR_DEFAULTS because neither is a plain
# default. TOKENHUB_DATABASE_URL must keep its empty value rather than gain one: if it
# is not explicitly set in the shell, backend's godotenv loads it from backend/.env, so
# PostgreSQL config there can take effect, and passing an empty value would override it.
# If neither exists, backend falls back to its own default (SQLite).
TOKENHUB_DATABASE_URL="${TOKENHUB_DATABASE_URL:-}"
# TOKENHUB_API_BASE_URL is derived: it falls back through NEXT_PUBLIC_API_BASE_URL to
# the public base URL resolved above, so it has to be assigned after the loop.
TOKENHUB_API_BASE_URL="${TOKENHUB_API_BASE_URL:-${NEXT_PUBLIC_API_BASE_URL:-$TOKENHUB_PUBLIC_BASE_URL}}"
FRONTEND_PORT="${FRONTEND_PORT:-3000}"
FRONTEND_HOST="${FRONTEND_HOST:-0.0.0.0}"
BACKEND_BIN="$ROOT_DIR/.tmp/tokenhub-backend"
NEXT_BIN="$FRONTEND_DIR/node_modules/.bin/next"

BACKEND_PID=""
FRONTEND_PID=""

log() {
  printf '[TokenHub] %s\n' "$*"
}

find_go() {
  if [ -n "${GO_BIN:-}" ]; then
    printf '%s\n' "$GO_BIN"
    return
  fi

  if command -v go >/dev/null 2>&1; then
    command -v go
    return
  fi

  if [ -x "/tmp/tokenhub-go126/go/bin/go" ]; then
    printf '%s\n' "/tmp/tokenhub-go126/go/bin/go"
    return
  fi

  log "Go not found. Please install Go, or specify via GO_BIN=/path/to/go."
  exit 1
}

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    log "Command not found: $1"
    exit 1
  fi
}

kill_tree() {
  local pid="$1"
  local children child

  children="$(pgrep -P "$pid" 2>/dev/null || true)"
  for child in $children; do
    kill_tree "$child"
  done

  if kill -0 "$pid" >/dev/null 2>&1; then
    kill -TERM "$pid" >/dev/null 2>&1 || true
  fi
}

cleanup() {
  local code=$?

  trap - INT TERM EXIT

  if [ -n "$FRONTEND_PID" ] && kill -0 "$FRONTEND_PID" >/dev/null 2>&1; then
    log "Stopping frontend service PID=$FRONTEND_PID"
    kill_tree "$FRONTEND_PID"
  fi

  if [ -n "$BACKEND_PID" ] && kill -0 "$BACKEND_PID" >/dev/null 2>&1; then
    log "Stopping backend service PID=$BACKEND_PID"
    kill_tree "$BACKEND_PID"
  fi

  wait "$FRONTEND_PID" >/dev/null 2>&1 || true
  wait "$BACKEND_PID" >/dev/null 2>&1 || true

  exit "$code"
}

trap cleanup INT TERM EXIT

GO_CMD="$(find_go)"
require_command npm

if [ ! -d "$FRONTEND_DIR/node_modules" ]; then
  log "Frontend dependencies not found, running npm install"
  (cd "$FRONTEND_DIR" && npm install)
fi

mkdir -p "$(dirname "$BACKEND_BIN")"

log "Building backend binary"
(cd "$BACKEND_DIR" && "$GO_CMD" build -o "$BACKEND_BIN" ./cmd/tokenhub)

log "Starting backend: $TOKENHUB_HTTP_ADDR"
(
  cd "$BACKEND_DIR"
  # Only pass TOKENHUB_DATABASE_URL if non-empty (explicitly set in shell),
  # otherwise let backend godotenv read from backend/.env to avoid overriding .env config.
  backend_env=()
  for var_entry in "${BACKEND_VAR_DEFAULTS[@]}"; do
    var_name="${var_entry%%=*}"
    backend_env+=("$var_name=${!var_name}")
  done
  if [ -n "$TOKENHUB_DATABASE_URL" ]; then
    backend_env+=(TOKENHUB_DATABASE_URL="$TOKENHUB_DATABASE_URL")
  fi
  exec env "${backend_env[@]}" "$BACKEND_BIN"
) &
BACKEND_PID=$!

log "Starting frontend: http://localhost:$FRONTEND_PORT"
(
  cd "$FRONTEND_DIR"
  exec env \
    TOKENHUB_API_BASE_URL="$TOKENHUB_API_BASE_URL" \
    "$NEXT_BIN" dev --hostname "$FRONTEND_HOST" --port "$FRONTEND_PORT"
) &
FRONTEND_PID=$!

cat <<EOF

TokenHub started:
  Backend API:  $TOKENHUB_PUBLIC_BASE_URL
  Frontend:     http://localhost:$FRONTEND_PORT
  Release repo: $TOKENHUB_RELEASE_REPOSITORY
  Admin Token:  $TOKENHUB_ADMIN_TOKEN

Press Ctrl-C to stop both services.
EOF

while true; do
  if ! kill -0 "$BACKEND_PID" >/dev/null 2>&1; then
    log "Backend service exited"
    wait "$BACKEND_PID" || exit $?
    exit 0
  fi

  if ! kill -0 "$FRONTEND_PID" >/dev/null 2>&1; then
    log "Frontend service exited"
    wait "$FRONTEND_PID" || exit $?
    exit 0
  fi

  sleep 1
done
