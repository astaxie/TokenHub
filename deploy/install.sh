#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMPOSE_FILE="$SCRIPT_DIR/docker-compose.yml"
MODEL_CATALOG_COMPOSE_FILE="$SCRIPT_DIR/docker-compose.model-catalog.yml"
ENV_FILE="$SCRIPT_DIR/.env"
DOCKER_BIN="${DOCKER_BIN:-docker}"
CHECK_ONLY=false
BUILD_LOCAL=false
MODEL_CATALOG_PATH=""

usage() {
  cat <<'EOF'
Usage: ./deploy/install.sh [--env-file PATH] [--check-only] [--build] [--model-catalog PATH]

Options:
  --env-file PATH      Use a Compose environment file other than deploy/.env.
  --check-only         Validate the deployment configuration without starting containers.
  --build              Build images from the local checkout instead of pulling published images.
  --model-catalog PATH Mount a custom model catalog instead of using the image's catalog.
  -h, --help           Show this help message.
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
    --build)
      BUILD_LOCAL=true
      shift
      ;;
    --model-catalog)
      if [ "$#" -lt 2 ]; then
        error "--model-catalog requires a path"
        usage >&2
        exit 2
      fi
      MODEL_CATALOG_PATH="$2"
      shift 2
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

if "$DOCKER_BIN" compose version >/dev/null 2>&1; then
  compose_command=("$DOCKER_BIN" compose)
elif command -v docker-compose >/dev/null 2>&1 &&
  docker-compose version >/dev/null 2>&1; then
  compose_command=(docker-compose)
else
  error "Docker Compose is not installed or is not available on PATH"
  error "install the Docker Compose plugin (preferred) or the legacy docker-compose command"
  exit 1
fi

compose=("${compose_command[@]}" --env-file "$ENV_FILE" -f "$COMPOSE_FILE")
if [ -n "$MODEL_CATALOG_PATH" ]; then
  if [ ! -f "$MODEL_CATALOG_PATH" ]; then
    error "model catalog file not found: $MODEL_CATALOG_PATH"
    exit 1
  fi
  MODEL_CATALOG_PATH="$(cd "$(dirname "$MODEL_CATALOG_PATH")" && pwd)/$(basename "$MODEL_CATALOG_PATH")"
  export TOKENHUB_MODEL_CATALOG_PATH="$MODEL_CATALOG_PATH"
  compose+=(-f "$MODEL_CATALOG_COMPOSE_FILE")
fi

if ! "${compose[@]}" version >/dev/null; then
  error "Docker Compose is not available"
  exit 1
fi

if ! "${compose[@]}" config --quiet; then
  error "Docker Compose could not parse $ENV_FILE"
  exit 1
fi

if compose_environment="$("${compose[@]}" config --environment 2>/dev/null)"; then
  :
elif command -v python3 >/dev/null 2>&1; then
  compose_environment="$("${compose[@]}" config --format json | python3 -c '
import json
import sys

config = json.load(sys.stdin)
backend = config.get("services", {}).get("tokenhub-backend", {})
environment = backend.get("environment", {})
image = backend.get("image", "")
image_tag = image.rsplit(":", 1)[1] if ":" in image else ""

for name, default in (
    ("TOKENHUB_ENV", "prod"),
    ("TOKENHUB_ADMIN_TOKEN", "change-me-tokenhub-admin-token"),
    ("TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD", "change-me-tokenhub-admin-password"),
    ("TOKENHUB_DATABASE_URL", "sqlite:///app/data/tokenhub.db"),
    ("TOKENHUB_SECRET_KEY", "change-me-tokenhub-secret-key"),
):
    value = environment.get(name, default)
    print(f"{name}={default if value is None else value}")
print(f"TOKENHUB_IMAGE_TAG={image_tag or '\''latest'\''}")
')" || {
    error "Docker Compose could not resolve deployment environment variables"
    exit 1
  }
else
  error "Docker Compose cannot expose its resolved interpolation environment"
  error "upgrade Docker Compose or install python3 to validate the deployment configuration"
  exit 1
fi

tokenhub_environment=""
admin_token=""
bootstrap_admin_password=""
database_url=""
secret_key=""
image_tag=""

while IFS= read -r line; do
  case "$line" in
    TOKENHUB_ENV=*) tokenhub_environment="${line#*=}" ;;
    TOKENHUB_ADMIN_TOKEN=*) admin_token="${line#*=}" ;;
    TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD=*) bootstrap_admin_password="${line#*=}" ;;
    TOKENHUB_DATABASE_URL=*) database_url="${line#*=}" ;;
    TOKENHUB_SECRET_KEY=*) secret_key="${line#*=}" ;;
    TOKENHUB_IMAGE_TAG=*) image_tag="${line#*=}" ;;
  esac
done <<<"$compose_environment"
unset compose_environment

# These defaults mirror the ${VAR:-default} expressions in docker-compose.yml.
tokenhub_environment="${tokenhub_environment:-prod}"
admin_token="${admin_token:-change-me-tokenhub-admin-token}"
bootstrap_admin_password="${bootstrap_admin_password:-change-me-tokenhub-admin-password}"
database_url="${database_url:-sqlite:///app/data/tokenhub.db}"
secret_key="${secret_key:-change-me-tokenhub-secret-key}"
image_tag="${image_tag:-latest}"

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

sqlite_database_file_path() {
  local database_url
  local lower_database_url
  local database_path
  database_url="$(trim_whitespace "$1")"
  lower_database_url="$(printf '%s' "$database_url" | tr '[:upper:]' '[:lower:]')"

  case "$lower_database_url" in
    *mode=memory*|:memory:)
      return 1
      ;;
    sqlite://*)
      database_path="${database_url#sqlite://}"
      ;;
    sqlite:*)
      database_path="${database_url#sqlite:}"
      ;;
    file:*)
      database_path="${database_url#file:}"
      while [[ "$database_path" == //* ]]; do
        database_path="${database_path#/}"
      done
      ;;
    *://*)
      return 1
      ;;
    *)
      database_path="$database_url"
      ;;
  esac

  database_path="${database_path%%\?*}"
  if [ -z "$database_path" ] || [ "$database_path" = ":memory:" ]; then
    return 1
  fi
  printf '%s' "$database_path"
}

sqlite_secret_key_file_is_safe() {
  local key_path="$1"
  local permissions
  local key

  if permissions="$(stat -c '%a' -- "$key_path" 2>/dev/null)"; then
    :
  elif permissions="$(stat -f '%Lp' "$key_path" 2>/dev/null)"; then
    :
  else
    return 1
  fi
  case "$permissions" in
    *[!0-7]*)
      return 1
      ;;
    *00)
      ;;
    *)
      return 1
      ;;
  esac

  if [ ! -r "$key_path" ]; then
    return 1
  fi
  if ! key="$(<"$key_path")"; then
    return 1
  fi
  key="$(trim_whitespace "$key")"
  case "$key" in
    dev_tokenhub_secret_key|change-me-tokenhub-secret-key)
      return 1
      ;;
  esac
  [ "$(byte_length "$key")" -ge 32 ]
}

# A placeholder root key is safe only when the backend can create a sidecar for
# a new database or reuse the sidecar already paired with an existing database.
sqlite_root_key_can_be_unset() {
  local database_path
  local relative_path
  local volume_names
  local volume_mountpoint
  local backend_id
  local database_file
  local key_file

  if ! database_path="$(sqlite_database_file_path "$1")"; then
    return 1
  fi
  case "$database_path" in
    /app/data/*)
      relative_path="${database_path#/app/data/}"
      ;;
    *)
      return 1
      ;;
  esac
  case "$relative_path" in
    ""|..|../*|*/../*|*/..)
      return 1
      ;;
  esac

  if ! volume_names="$("$DOCKER_BIN" volume ls --quiet --filter 'name=^tokenhub-data$' 2>/dev/null)"; then
    return 1
  fi
  if [ -z "$volume_names" ]; then
    return 0
  fi
  if [ "$volume_names" != "tokenhub-data" ]; then
    return 1
  fi

  if volume_mountpoint="$("$DOCKER_BIN" volume inspect --format '{{.Mountpoint}}' tokenhub-data 2>/dev/null)" &&
    [ -d "$volume_mountpoint" ]; then
    database_file="$volume_mountpoint/$relative_path"
    key_file="$database_file.secret-key"
    if [ -e "$key_file" ] || [ -L "$key_file" ]; then
      if sqlite_secret_key_file_is_safe "$key_file"; then
        return 0
      fi
    else
      if [ -L "$database_file" ]; then
        return 1
      fi
      if [ ! -e "$database_file" ] || [ ! -s "$database_file" ]; then
        return 0
      fi
    fi
  fi

  if backend_id="$("${compose[@]}" ps -a -q tokenhub-backend 2>/dev/null)" &&
    [ -n "$backend_id" ]; then
    if "$DOCKER_BIN" exec "$backend_id" node -e '
const fs = require("node:fs");
const databasePath = process.argv[1];
const keyPath = databasePath + ".secret-key";
let keyInfo;
try {
  keyInfo = fs.statSync(keyPath);
} catch (error) {
  if (error.code !== "ENOENT") process.exit(1);
}
if (keyInfo) {
  if ((keyInfo.mode & 0o77) !== 0) process.exit(1);
  let key;
  try {
    key = fs.readFileSync(keyPath, "utf8").replace(/^\p{White_Space}+|\p{White_Space}+$/gu, "");
  } catch {
    process.exit(1);
  }
  const blocked = new Set(["dev_tokenhub_secret_key", "change-me-tokenhub-secret-key"]);
  process.exit(Buffer.byteLength(key) >= 32 && !blocked.has(key) ? 0 : 1);
}
let databaseInfo;
try {
  databaseInfo = fs.lstatSync(databasePath);
} catch (error) {
  if (error.code !== "ENOENT") process.exit(1);
}
process.exit(!databaseInfo || (!databaseInfo.isSymbolicLink() && databaseInfo.size === 0) ? 0 : 1);
' "$database_path" >/dev/null 2>&1; then
      return 0
    fi
  fi
  return 1
}

validation_errors=()
environment="$(trim_whitespace "$tokenhub_environment")"
environment="$(printf '%s' "$environment" | tr '[:upper:]' '[:lower:]')"

if [ -z "$environment" ]; then
  validation_errors+=("TOKENHUB_ENV must not be empty")
elif [[ "$environment" != "dev" && "$environment" != "development" && "$environment" != "local" && "$environment" != "test" ]]; then
  root_key_can_be_unset=false
  if sqlite_root_key_can_be_unset "$database_url"; then
    root_key_can_be_unset=true
  fi

  validate_secret() {
    local name="$1"
    local value="$2"
    local minimum_length="$3"
    local allow_unset="$4"
    shift 4
    value="$(trim_whitespace "$value")"

    local blocked
    for blocked in "$@"; do
      if [ "$value" = "$blocked" ]; then
        if [ "$allow_unset" = true ]; then
          return 0
        fi
        validation_errors+=("$name must not use a default placeholder value")
        return
      fi
    done

    if [ "$allow_unset" = true ] && [ -z "$value" ]; then
      return 0
    fi

    if [ "$(byte_length "$value")" -lt "$minimum_length" ]; then
      validation_errors+=("$name must be at least $minimum_length bytes after trimming whitespace")
      return
    fi
  }

  validate_secret "TOKENHUB_ADMIN_TOKEN" "$admin_token" 32 true \
    "dev_admin_token" "change-me-tokenhub-admin-token"
  validate_secret "TOKENHUB_SECRET_KEY" "$secret_key" 32 "$root_key_can_be_unset" \
    "dev_tokenhub_secret_key" "change-me-tokenhub-secret-key"
  validate_secret "TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD" "$bootstrap_admin_password" 12 true \
    "admin123456" "change-me-tokenhub-admin-password"
fi

unset admin_token bootstrap_admin_password database_url root_key_can_be_unset secret_key

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

if [ "$BUILD_LOCAL" = true ]; then
  log "building TokenHub images from the local checkout"
  if "${compose[@]}" build; then
    :
  else
    status=$?
    error "Docker Compose failed to build TokenHub images (exit status $status)"
    exit "$status"
  fi
else
  log "pulling published TokenHub images"
  if "${compose[@]}" pull; then
    :
  else
    pull_status=$?
    error "Docker Compose failed to pull published TokenHub images (exit status $pull_status)"
    if [ "$image_tag" != "latest" ]; then
      error "TOKENHUB_IMAGE_TAG=$image_tag was explicitly selected; refusing to replace it with a local source build"
      error "check the tag and registry access, or use --build explicitly"
      exit "$pull_status"
    fi
    log "published images are unavailable; falling back to a local source build"
    if "${compose[@]}" build; then
      :
    else
      status=$?
      error "Docker Compose also failed to build TokenHub images locally (exit status $status)"
      exit "$status"
    fi
  fi
fi

log "starting TokenHub"
compose_started_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
backend_container_id_before="$("${compose[@]}" ps -a -q tokenhub-backend 2>/dev/null || true)"
backend_started_at_before=""
if [ -n "$backend_container_id_before" ]; then
  backend_started_at_before="$("$DOCKER_BIN" inspect --format '{{.State.StartedAt}}' "$backend_container_id_before" 2>/dev/null || true)"
fi

if "${compose[@]}" up -d --remove-orphans --no-build --pull never --wait --wait-timeout 180; then
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
  if [ -n "$backend_health" ] && [ "$backend_health" != "healthy" ]; then
    backend_failed=true
  fi

  if [ "$backend_changed" = true ] && [ "$backend_failed" = true ]; then
    error "tokenhub-backend logs from this startup attempt:"
    backend_logs_since="${backend_started_at_after:-$compose_started_at}"
    "${compose[@]}" logs --no-color --tail=100 --since "$backend_logs_since" tokenhub-backend >&2 || \
      error "unable to read tokenhub-backend logs"
  else
    error "tokenhub-backend did not both change and fail readiness during this startup attempt; its logs were not included"
  fi
  exit "$status"
fi
