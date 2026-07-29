#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMPOSE_FILE="$SCRIPT_DIR/docker-compose.yml"
ENV_FILE="$SCRIPT_DIR/.env"
DOCKER_BIN="${DOCKER_BIN:-docker}"
CHECK_ONLY=false

usage() {
  cat <<'EOF'
Usage: ./deploy/install.sh [--env-file PATH] [--check-only]

Options:
  --env-file PATH  Use a Compose environment file other than deploy/.env.
  --check-only     Validate the deployment configuration without starting containers.
  -h, --help       Show this help message.
EOF
}

log() {
  printf '[TokenHub] %s\n' "$*"
}

error() {
  printf '[TokenHub] ERROR: %s\n' "$*" >&2
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --env-file)
      if [ "$#" -lt 2 ]; then
        error "--env-file requires a path"
        usage >&2
        exit 2
      fi
      ENV_FILE="$2"
      shift 2
      ;;
    --check-only)
      CHECK_ONLY=true
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      error "unknown option: $1"
      usage >&2
      exit 2
      ;;
  esac
done

if ! command -v "$DOCKER_BIN" >/dev/null 2>&1; then
  error "Docker is not installed or is not available on PATH"
  exit 1
fi

if [ ! -f "$ENV_FILE" ]; then
  error "environment file not found: $ENV_FILE"
  error "create it with: cp deploy/.env.example deploy/.env"
  exit 1
fi

compose=("$DOCKER_BIN" compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE")

if ! "${compose[@]}" version >/dev/null; then
  error "Docker Compose is not available"
  exit 1
fi

if ! "${compose[@]}" config --quiet; then
  error "Docker Compose could not parse $ENV_FILE"
  exit 1
fi

compose_environment="$("${compose[@]}" config --environment)" || {
  error "Docker Compose could not resolve deployment environment variables"
  exit 1
}

tokenhub_environment=""
admin_token=""
integration_token=""
bootstrap_admin_password=""
secret_key=""

while IFS= read -r line; do
  case "$line" in
    TOKENHUB_ENV=*) tokenhub_environment="${line#*=}" ;;
    TOKENHUB_ADMIN_TOKEN=*) admin_token="${line#*=}" ;;
    TOKENHUB_INTEGRATION_TOKEN=*) integration_token="${line#*=}" ;;
    TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD=*) bootstrap_admin_password="${line#*=}" ;;
    TOKENHUB_SECRET_KEY=*) secret_key="${line#*=}" ;;
  esac
done <<<"$compose_environment"
unset compose_environment

# These defaults mirror the ${VAR:-default} expressions in docker-compose.yml.
tokenhub_environment="${tokenhub_environment:-prod}"
admin_token="${admin_token:-change-me-tokenhub-admin-token}"
integration_token="${integration_token:-change-me-tokenhub-integration-token}"
bootstrap_admin_password="${bootstrap_admin_password:-change-me-tokenhub-admin-password}"
secret_key="${secret_key:-change-me-tokenhub-secret-key}"

trim_whitespace() {
  # Keep this list aligned with Go's strings.TrimSpace (Unicode White_Space).
  # LC_ALL=C makes every pattern operate on the explicit UTF-8 byte sequences.
  local LC_ALL=C
  local value="$1"
  local whitespace
  local matched
  local whitespace_characters=(
    ' '
    $'\t'
    $'\n'
    $'\v'
    $'\f'
    $'\r'
    $'\302\205'
    $'\302\240'
    $'\341\232\200'
    $'\342\200\200'
    $'\342\200\201'
    $'\342\200\202'
    $'\342\200\203'
    $'\342\200\204'
    $'\342\200\205'
    $'\342\200\206'
    $'\342\200\207'
    $'\342\200\210'
    $'\342\200\211'
    $'\342\200\212'
    $'\342\200\250'
    $'\342\200\251'
    $'\342\200\257'
    $'\342\201\237'
    $'\343\200\200'
  )

  while [ -n "$value" ]; do
    matched=false
    for whitespace in "${whitespace_characters[@]}"; do
      case "$value" in
        "$whitespace"*)
          value="${value#"$whitespace"}"
          matched=true
          break
          ;;
      esac
    done
    if [ "$matched" = false ]; then
      break
    fi
  done

  while [ -n "$value" ]; do
    matched=false
    for whitespace in "${whitespace_characters[@]}"; do
      case "$value" in
        *"$whitespace")
          value="${value%"$whitespace"}"
          matched=true
          break
          ;;
      esac
    done
    if [ "$matched" = false ]; then
      break
    fi
  done

  printf '%s' "$value"
}

byte_length() {
  local LC_ALL=C
  local value="$1"
  printf '%d' "${#value}"
}

validation_errors=()
environment="$(trim_whitespace "$tokenhub_environment")"
environment="$(printf '%s' "$environment" | tr '[:upper:]' '[:lower:]')"

if [ -z "$environment" ]; then
  validation_errors+=("TOKENHUB_ENV must not be empty")
elif [[ "$environment" != "dev" && "$environment" != "development" && "$environment" != "local" && "$environment" != "test" ]]; then
  validate_secret() {
    local name="$1"
    local value="$2"
    local minimum_length="$3"
    shift 3
    value="$(trim_whitespace "$value")"

    local blocked
    for blocked in "$@"; do
      if [ "$value" = "$blocked" ]; then
        validation_errors+=("$name must not use a default placeholder value")
        return
      fi
    done

    if [ "$(byte_length "$value")" -lt "$minimum_length" ]; then
      validation_errors+=("$name must be at least $minimum_length bytes after trimming whitespace")
      return
    fi
  }

  validate_secret "TOKENHUB_ADMIN_TOKEN" "$admin_token" 32 \
    "dev_admin_token" "change-me-tokenhub-admin-token"
  validate_secret "TOKENHUB_INTEGRATION_TOKEN" "$integration_token" 32 \
    "dev_integration_token" "change-me-tokenhub-integration-token"
  validate_secret "TOKENHUB_SECRET_KEY" "$secret_key" 32 \
    "dev_tokenhub_secret_key" "change-me-tokenhub-secret-key"
  validate_secret "TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD" "$bootstrap_admin_password" 12 \
    "admin123456" "change-me-tokenhub-admin-password"
fi

unset admin_token integration_token bootstrap_admin_password secret_key

if [ "${#validation_errors[@]}" -gt 0 ]; then
  error "deployment configuration is unsafe for $environment:"
  for validation_error in "${validation_errors[@]}"; do
    printf '  - %s\n' "$validation_error" >&2
  done
  error "update $ENV_FILE and run this command again"
  exit 1
fi

log "deployment configuration is valid for $environment"

if [ "$CHECK_ONLY" = true ]; then
  exit 0
fi

log "building and starting TokenHub"
compose_started_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
backend_container_id_before="$("${compose[@]}" ps -a -q tokenhub-backend 2>/dev/null || true)"
backend_started_at_before=""
if [ -n "$backend_container_id_before" ]; then
  backend_started_at_before="$("$DOCKER_BIN" inspect --format '{{.State.StartedAt}}' "$backend_container_id_before" 2>/dev/null || true)"
fi

if "${compose[@]}" up -d --build; then
  log "TokenHub started successfully"
  "${compose[@]}" ps
else
  status=$?
  error "Docker Compose failed to start TokenHub (exit status $status)"

  backend_container_id_after="$("${compose[@]}" ps -a -q tokenhub-backend 2>/dev/null || true)"
  backend_state=""
  backend_health=""
  backend_started_at_after=""
  if [ -n "$backend_container_id_after" ]; then
    backend_inspect="$("$DOCKER_BIN" inspect --format '{{.State.Status}}|{{if .State.Health}}{{.State.Health.Status}}{{end}}|{{.State.StartedAt}}' "$backend_container_id_after" 2>/dev/null || true)"
    IFS='|' read -r backend_state backend_health backend_started_at_after <<<"$backend_inspect"
    unset backend_inspect
  fi

  backend_changed=false
  if [ -n "$backend_container_id_after" ]; then
    if [ "$backend_container_id_after" != "$backend_container_id_before" ]; then
      backend_changed=true
    elif [ -n "$backend_started_at_before" ] &&
      [ -n "$backend_started_at_after" ] &&
      [ "$backend_started_at_after" != "$backend_started_at_before" ]; then
      backend_changed=true
    fi
  fi

  backend_failed=false
  case "$backend_state" in
    exited|restarting|dead) backend_failed=true ;;
  esac
  if [ "$backend_health" = "unhealthy" ]; then
    backend_failed=true
  fi

  if [ "$backend_changed" = true ] && [ "$backend_failed" = true ]; then
    error "tokenhub-backend logs from this startup attempt:"
    backend_logs_since="${backend_started_at_after:-$compose_started_at}"
    "${compose[@]}" logs --no-color --tail=100 --since "$backend_logs_since" tokenhub-backend >&2 || \
      error "unable to read tokenhub-backend logs"
  else
    error "tokenhub-backend did not both change and enter a failed state during this startup attempt; its logs were not included"
  fi
  exit "$status"
fi
