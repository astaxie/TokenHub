#!/usr/bin/env sh
set -eu

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
PROJECT_NAME="tokenhub-e2e-$$"
COMPOSE_FILE="$SCRIPT_DIR/docker-compose.e2e.yml"

cleanup() {
  docker compose -p "$PROJECT_NAME" -f "$COMPOSE_FILE" down --volumes --remove-orphans >/dev/null 2>&1 || true
}

trap cleanup EXIT INT TERM

docker compose -p "$PROJECT_NAME" -f "$COMPOSE_FILE" up \
  --abort-on-container-exit \
  --exit-code-from tokenhub-e2e
