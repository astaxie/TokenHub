#!/usr/bin/env bash
set -Eeuo pipefail

GITHUB_REPOSITORY="${TOKENHUB_RELEASE_REPOSITORY:-astaxie/TokenHub}"
INSTALL_ROOT="${TOKENHUB_INSTALL_ROOT:-/opt/tokenhub}"
CONFIG_DIR="${TOKENHUB_CONFIG_DIR:-/etc/tokenhub}"
STATE_DIR="${TOKENHUB_STATE_DIR:-/var/lib/tokenhub}"
SYSTEMD_DIR="${TOKENHUB_SYSTEMD_DIR:-/etc/systemd/system}"
SERVICE_USER="${TOKENHUB_SERVICE_USER:-tokenhub}"
SERVICE_GROUP="${TOKENHUB_SERVICE_GROUP:-}"
SERVICE_NAME="${TOKENHUB_SERVICE_NAME:-tokenhub}"
BACKEND_PORT="${TOKENHUB_BACKEND_PORT:-8080}"
FRONTEND_PORT="${TOKENHUB_FRONTEND_PORT:-3000}"
VERSION=""
COMMAND="install"
PURGE=false
TEMP_DIR=""
DOWNLOADED_ARCHIVE=""
GENERATED_ADMIN_PASSWORD=""
CREATED_SERVICE_USER=false
RESOLVED_PUBLIC_HOST=""
RESOLVED_PUBLIC_HOST_SOURCE=""
MAX_NATIVE_ARCHIVE_BYTES=$((600 * 1024 * 1024))
MAX_NATIVE_CHECKSUM_BYTES=$((1024 * 1024))
MAX_NATIVE_EXTRACTED_BYTES=$((2 * 1024 * 1024 * 1024))
MAX_NATIVE_ARCHIVE_FILE_BYTES=$((600 * 1024 * 1024))
MAX_NATIVE_ARCHIVE_ENTRIES=100000
SERVICE_READY_ATTEMPTS="${TOKENHUB_INSTALLER_READY_ATTEMPTS:-60}"
MANAGED_DIRECTORY_MARKER=".tokenhub-managed-directory"

usage() {
  cat <<'EOF'
TokenHub native installer

Usage:
  install.sh install [--version VERSION]
  install.sh upgrade [--version VERSION]
  install.sh rollback --version VERSION
  install.sh uninstall [--purge]
  install.sh status

Environment:
  TOKENHUB_RELEASE_REPOSITORY  GitHub owner/repository (default: astaxie/TokenHub)
  TOKENHUB_PUBLIC_HOST         Public hostname or IP used in generated URLs
  TOKENHUB_PUBLIC_BASE_URL     Public backend URL override
  TOKENHUB_API_BASE_URL        Browser-facing backend URL override
  TOKENHUB_CORS_ALLOWED_ORIGINS
  TOKENHUB_BACKEND_PORT        Backend port (default: 8080)
  TOKENHUB_FRONTEND_PORT       Admin console port (default: 3000)
  TOKENHUB_DATABASE_URL        Database URL written on first install (default: SQLite)
  TOKENHUB_SERVICE_USER        Linux service user (default: tokenhub)
EOF
}

fail() {
  printf 'TokenHub installer: %s\n' "$*" >&2
  exit 1
}

info() {
  printf '[TokenHub] %s\n' "$*"
}

warn() {
  printf '[TokenHub] Warning: %s\n' "$*" >&2
}

cleanup() {
  if [ -n "$TEMP_DIR" ] && [ -d "$TEMP_DIR" ]; then
    rm -rf -- "$TEMP_DIR"
  fi
}

validate_safe_path() {
  local value="$1"
  local label="$2"
  [[ "$value" == /* ]] || fail "$label must be an absolute path"
  [[ "$value" != "/" ]] || fail "$label must not be /"
  [[ "$value" =~ ^/[A-Za-z0-9._/-]+$ ]] || fail "$label contains unsupported characters"
  [[ "$value" != */ && "$value" != *"//"* ]] ||
    fail "$label must not contain a trailing or repeated /"
  [[ "${value}/" != *"/../"* && "${value}/" != *"/./"* ]] ||
    fail "$label must not contain . or .. path segments"
}

validate_managed_path() {
  local value="$1"
  local label="$2"
  local relative="${value#/}"
  validate_safe_path "$value" "$label"
  case "$value" in
    /bin|/boot|/dev|/etc|/home|/lib|/lib64|/media|/mnt|/opt|/proc|/root|/run|/sbin|/srv|/sys|/tmp|/usr|/usr/local|/var|/var/lib|/var/log|/var/tmp)
      fail "$label must be an application-specific directory"
      ;;
  esac
  [[ "$relative" == */* ]] ||
    fail "$label must be at least two path segments below /"
}

validate_managed_paths_are_distinct() {
  local first
  local second
  for first in "$INSTALL_ROOT" "$CONFIG_DIR" "$STATE_DIR"; do
    for second in "$INSTALL_ROOT" "$CONFIG_DIR" "$STATE_DIR"; do
      [ "$first" = "$second" ] && continue
      case "${first}/" in
        "${second}/"*)
          fail "managed directories must not contain one another: $first and $second"
          ;;
      esac
    done
  done
  [ "$INSTALL_ROOT" != "$CONFIG_DIR" ] &&
    [ "$INSTALL_ROOT" != "$STATE_DIR" ] &&
    [ "$CONFIG_DIR" != "$STATE_DIR" ] ||
    fail "managed directories must be distinct"
}

validate_port() {
  local value="$1"
  local label="$2"
  [[ "$value" =~ ^[0-9]+$ ]] || fail "$label must be a number"
  (( 10#$value >= 1 && 10#$value <= 65535 )) || fail "$label must be between 1 and 65535"
}

validate_single_line_value() {
  local value="$1"
  local label="$2"
  if [[ "$value" == *$'\n'* || "$value" == *$'\r'* ]]; then
    fail "$label must not contain line breaks"
  fi
}

port_is_listening() {
  local port="$1"
  local listeners
  if command -v ss >/dev/null 2>&1; then
    listeners="$(ss -H -ltn "sport = :$port" 2>/dev/null || true)"
    [ -n "$listeners" ]
    return
  fi
  if command -v lsof >/dev/null 2>&1; then
    lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1
    return
  fi
  return 1
}

validate_fresh_install_ports() {
  local backend_port=$((10#$BACKEND_PORT))
  local frontend_port=$((10#$FRONTEND_PORT))
  [ ! -f "$CONFIG_DIR/tokenhub.env" ] || return 0
  [ "$backend_port" -ne "$frontend_port" ] ||
    fail "TOKENHUB_BACKEND_PORT and TOKENHUB_FRONTEND_PORT must use different ports"
  if port_is_listening "$backend_port"; then
    fail "backend port $backend_port is already in use; set TOKENHUB_BACKEND_PORT to a free port and rerun the installer"
  fi
  if port_is_listening "$frontend_port"; then
    fail "admin console port $frontend_port is already in use; set TOKENHUB_FRONTEND_PORT to a free port and rerun the installer"
  fi
}

validate_identifiers() {
  [[ "$SERVICE_USER" =~ ^[A-Za-z_][A-Za-z0-9_-]*$ ]] ||
    fail "TOKENHUB_SERVICE_USER contains unsupported characters"
  [[ "$SERVICE_NAME" =~ ^[A-Za-z0-9][A-Za-z0-9_.@-]*$ ]] ||
    fail "TOKENHUB_SERVICE_NAME contains unsupported characters"
}

normalize_version() {
  local value="${1#v}"
  local numeric_identifier='(0|[1-9][0-9]*)'
  local prerelease_identifier='(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)'
  local semver_pattern="^${numeric_identifier}\.${numeric_identifier}\.${numeric_identifier}(-${prerelease_identifier}(\.${prerelease_identifier})*)?$"
  [[ "$value" =~ $semver_pattern ]] ||
    fail "invalid semantic version: $1"
  printf '%s\n' "$value"
}

compare_decimal_identifiers() {
  local left="$1"
  local right="$2"
  if ((${#left} < ${#right})); then
    printf '%s\n' -1
  elif ((${#left} > ${#right})); then
    printf '%s\n' 1
  elif [ "$left" = "$right" ]; then
    printf '%s\n' 0
  elif [[ "$left" < "$right" ]]; then
    printf '%s\n' -1
  else
    printf '%s\n' 1
  fi
}

compare_semantic_versions() {
  local left
  local right
  local left_core
  local right_core
  local left_prerelease=""
  local right_prerelease=""
  local comparison
  local index
  local left_identifier
  local right_identifier
  local LC_ALL=C
  local -a left_core_parts
  local -a right_core_parts
  local -a left_prerelease_parts
  local -a right_prerelease_parts

  left="$(normalize_version "$1")"
  right="$(normalize_version "$2")"
  left_core="${left%%-*}"
  right_core="${right%%-*}"
  if [[ "$left" == *-* ]]; then
    left_prerelease="${left#*-}"
  fi
  if [[ "$right" == *-* ]]; then
    right_prerelease="${right#*-}"
  fi
  IFS=. read -r -a left_core_parts <<<"$left_core"
  IFS=. read -r -a right_core_parts <<<"$right_core"
  for index in 0 1 2; do
    comparison="$(compare_decimal_identifiers "${left_core_parts[$index]}" "${right_core_parts[$index]}")"
    if [ "$comparison" -ne 0 ]; then
      printf '%s\n' "$comparison"
      return
    fi
  done

  if [ -z "$left_prerelease" ] && [ -z "$right_prerelease" ]; then
    printf '%s\n' 0
    return
  fi
  if [ -z "$left_prerelease" ]; then
    printf '%s\n' 1
    return
  fi
  if [ -z "$right_prerelease" ]; then
    printf '%s\n' -1
    return
  fi

  IFS=. read -r -a left_prerelease_parts <<<"$left_prerelease"
  IFS=. read -r -a right_prerelease_parts <<<"$right_prerelease"
  for ((index = 0; index < ${#left_prerelease_parts[@]} || index < ${#right_prerelease_parts[@]}; index++)); do
    if ((index >= ${#left_prerelease_parts[@]})); then
      printf '%s\n' -1
      return
    fi
    if ((index >= ${#right_prerelease_parts[@]})); then
      printf '%s\n' 1
      return
    fi
    left_identifier="${left_prerelease_parts[$index]}"
    right_identifier="${right_prerelease_parts[$index]}"
    if [[ "$left_identifier" =~ ^[0-9]+$ ]] && [[ "$right_identifier" =~ ^[0-9]+$ ]]; then
      comparison="$(compare_decimal_identifiers "$left_identifier" "$right_identifier")"
    elif [[ "$left_identifier" =~ ^[0-9]+$ ]]; then
      comparison=-1
    elif [[ "$right_identifier" =~ ^[0-9]+$ ]]; then
      comparison=1
    elif [ "$left_identifier" = "$right_identifier" ]; then
      comparison=0
    elif [[ "$left_identifier" < "$right_identifier" ]]; then
      comparison=-1
    else
      comparison=1
    fi
    if [ "$comparison" -ne 0 ]; then
      printf '%s\n' "$comparison"
      return
    fi
  done
  printf '%s\n' 0
}

parse_args() {
  if [ "$#" -gt 0 ] && [[ "$1" != -* ]]; then
    COMMAND="$1"
    shift
  fi
  while [ "$#" -gt 0 ]; do
    case "$1" in
      -v|--version)
        [ "$#" -ge 2 ] || fail "$1 requires a value"
        VERSION="$(normalize_version "$2")"
        shift 2
        ;;
      --purge)
        PURGE=true
        shift
        ;;
      -h|--help)
        usage
        exit 0
        ;;
      *)
        fail "unknown argument: $1"
        ;;
    esac
  done
}

require_root() {
  if [ "${EUID:-$(id -u)}" -ne 0 ] && [ "${TOKENHUB_INSTALLER_ALLOW_NON_ROOT:-}" != "1" ]; then
    fail "run this installer as root (for example, with sudo)"
  fi
}

require_platform() {
  [ "$(uname -s)" = "Linux" ] || fail "native installation supports Linux only"
  command -v systemctl >/dev/null 2>&1 || fail "systemd is required on Linux"
  command -v sha256sum >/dev/null 2>&1 || fail "sha256sum is required on Linux"
  validate_safe_path "$SYSTEMD_DIR" "TOKENHUB_SYSTEMD_DIR"
  for command in curl tar sed awk find grep install od tr wc; do
    command -v "$command" >/dev/null 2>&1 || fail "missing required command: $command"
  done
  load_existing_release_repository
  [[ "$GITHUB_REPOSITORY" =~ ^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$ ]] ||
    fail "TOKENHUB_RELEASE_REPOSITORY must use owner/repository form"
  validate_managed_path "$INSTALL_ROOT" "TOKENHUB_INSTALL_ROOT"
  validate_managed_path "$CONFIG_DIR" "TOKENHUB_CONFIG_DIR"
  validate_managed_path "$STATE_DIR" "TOKENHUB_STATE_DIR"
  validate_managed_paths_are_distinct
  validate_identifiers
  validate_port "$BACKEND_PORT" "TOKENHUB_BACKEND_PORT"
  validate_port "$FRONTEND_PORT" "TOKENHUB_FRONTEND_PORT"
  [[ "$SERVICE_READY_ATTEMPTS" =~ ^[1-9][0-9]*$ ]] ||
    fail "TOKENHUB_INSTALLER_READY_ATTEMPTS must be a positive integer"
}

platform_arch() {
  case "$(uname -m)" in
    x86_64|amd64) printf 'amd64\n' ;;
    aarch64|arm64) printf 'arm64\n' ;;
    *) fail "unsupported architecture: $(uname -m)" ;;
  esac
}

latest_version() {
  local response
  local tag
  response="$(curl -fsSL \
    -H 'Accept: application/vnd.github+json' \
    -H 'X-GitHub-Api-Version: 2022-11-28' \
    "https://api.github.com/repos/${GITHUB_REPOSITORY}/releases/latest")" ||
    fail "unable to query the latest GitHub Release"
  tag="$(printf '%s\n' "$response" | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | sed -n '1p')"
  [ -n "$tag" ] || fail "latest GitHub Release has no tag_name"
  [[ "$tag" == v* ]] || fail "latest GitHub Release tag must start with v"
  normalize_version "$tag"
}

ensure_service_user() {
  if id "$SERVICE_USER" >/dev/null 2>&1; then
    SERVICE_GROUP="${SERVICE_GROUP:-$(id -gn "$SERVICE_USER")}"
    [[ "$SERVICE_GROUP" =~ ^[A-Za-z_][A-Za-z0-9_-]*$ ]] ||
      fail "TOKENHUB_SERVICE_GROUP contains unsupported characters"
    return
  fi
  command -v useradd >/dev/null 2>&1 || fail "useradd is required to create $SERVICE_USER"
  useradd --system --user-group --home-dir "$STATE_DIR" --shell /usr/sbin/nologin "$SERVICE_USER"
  SERVICE_GROUP="${SERVICE_GROUP:-$(id -gn "$SERVICE_USER")}"
  [[ "$SERVICE_GROUP" =~ ^[A-Za-z_][A-Za-z0-9_-]*$ ]] ||
    fail "TOKENHUB_SERVICE_GROUP contains unsupported characters"
  CREATED_SERVICE_USER=true
}

record_created_service_user() {
  if [ "$CREATED_SERVICE_USER" != true ]; then
    return
  fi
  local marker="$CONFIG_DIR/.service-user-created"
  printf '%s:%s\n' "$SERVICE_USER" "$(id -u "$SERVICE_USER")" >"$marker"
  chmod 0600 "$marker"
}

random_hex() {
  local bytes="$1"
  od -An -N "$bytes" -tx1 /dev/urandom | tr -d ' \n'
}

read_config_value() {
  local env_file="$1"
  local key="$2"
  awk -v key="$key" '
    index($0, key "=") == 1 {
      value = substr($0, length(key) + 2)
      found = 1
    }
    END {
      if (found) {
        print value
      }
    }
  ' "$env_file"
}

load_existing_release_repository() {
  local env_file="$CONFIG_DIR/tokenhub.env"
  local configured_repository
  if [ -n "${TOKENHUB_RELEASE_REPOSITORY:-}" ] || [ ! -f "$env_file" ]; then
    return 0
  fi
  configured_repository="$(read_config_value "$env_file" TOKENHUB_RELEASE_REPOSITORY)"
  if [ -n "$configured_repository" ]; then
    GITHUB_REPOSITORY="$configured_repository"
  fi
}

load_pending_admin_password() {
  local env_file="$CONFIG_DIR/tokenhub.env"
  local marker="$CONFIG_DIR/.initial-admin-password-pending"
  if [ ! -f "$marker" ]; then
    return 0
  fi
  [ -f "$env_file" ] || fail "initial admin password marker exists without tokenhub.env"
  GENERATED_ADMIN_PASSWORD="$(read_config_value "$env_file" TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD)"
  [ -n "$GENERATED_ADMIN_PASSWORD" ] ||
    fail "tokenhub.env has no initial admin password for the pending installation"
}

show_initial_admin_credentials() {
  local marker="$CONFIG_DIR/.initial-admin-password-pending"
  if [ -z "$GENERATED_ADMIN_PASSWORD" ]; then
    return
  fi
  info "Initial admin username: admin"
  info "Initial admin password: $GENERATED_ADMIN_PASSWORD"
  rm -f -- "$marker"
  GENERATED_ADMIN_PASSWORD=""
}

is_ipv4_address() {
  local value="$1"
  local first
  local second
  local third
  local fourth
  local octet
  [[ "$value" =~ ^[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}$ ]] ||
    return 1
  IFS=. read -r first second third fourth <<<"$value"
  for octet in "$first" "$second" "$third" "$fourth"; do
    [ "$((10#$octet))" -le 255 ] || return 1
  done
}

is_ip_address() {
  local value="$1"
  if is_ipv4_address "$value"; then
    return 0
  fi
  [[ "$value" == *:* && "$value" =~ ^[0-9A-Fa-f:]+$ ]]
}

detect_ipinfo_public_address() {
  local response
  local host
  command -v curl >/dev/null 2>&1 || return 1
  response="$(
    curl --connect-timeout 5 --max-time 10 -fsS \
      --proto '=https' \
      'https://ipinfo.io/json' 2>/dev/null
  )" || return 1
  host="$(
    printf '%s\n' "$response" |
      sed -n 's/.*"ip"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' |
      head -n 1
  )"
  [ -n "$host" ] || return 1
  is_ip_address "$host" || return 1
  printf '%s\n' "$host"
}

resolve_public_host() {
  local addresses
  local configured_host
  local env_file="$CONFIG_DIR/tokenhub.env"
  local host
  if [ -n "$RESOLVED_PUBLIC_HOST" ]; then
    return
  fi
  if [ -f "$env_file" ]; then
    configured_host="$(read_config_value "$env_file" TOKENHUB_PUBLIC_HOST)"
    if [ -n "$configured_host" ]; then
      validate_single_line_value "$configured_host" "configured TOKENHUB_PUBLIC_HOST"
      RESOLVED_PUBLIC_HOST="$configured_host"
      RESOLVED_PUBLIC_HOST_SOURCE="config"
      return
    fi
  fi
  if [ -n "${TOKENHUB_PUBLIC_HOST:-}" ]; then
    validate_single_line_value "$TOKENHUB_PUBLIC_HOST" "TOKENHUB_PUBLIC_HOST"
    RESOLVED_PUBLIC_HOST="$TOKENHUB_PUBLIC_HOST"
    RESOLVED_PUBLIC_HOST_SOURCE="explicit"
    return
  fi
  if host="$(detect_ipinfo_public_address)"; then
    RESOLVED_PUBLIC_HOST="$host"
    RESOLVED_PUBLIC_HOST_SOURCE="ipinfo"
    return
  fi
  if command -v hostname >/dev/null 2>&1; then
    addresses="$(hostname -I 2>/dev/null || true)"
    host="$(printf '%s\n' "$addresses" | awk '{print $1}')"
    if [ -n "$host" ]; then
      RESOLVED_PUBLIC_HOST="$host"
      RESOLVED_PUBLIC_HOST_SOURCE="hostname"
      return
    fi
  fi
  RESOLVED_PUBLIC_HOST="127.0.0.1"
  RESOLVED_PUBLIC_HOST_SOURCE="loopback"
}

initial_database_url() {
  local database_url="${TOKENHUB_DATABASE_URL:-sqlite://${STATE_DIR}/tokenhub.db}"
  validate_single_line_value "$database_url" "TOKENHUB_DATABASE_URL"
  printf '%s\n' "$database_url"
}

url_host() {
  local host="${1#[}"
  host="${host%]}"
  if [[ "$host" == *:* ]]; then
    printf '[%s]\n' "$host"
    return
  fi
  printf '%s\n' "$host"
}

write_initial_config() {
  local env_file="$CONFIG_DIR/tokenhub.env"
  local config_owner="root"
  local config_mode="0640"
  local directory_mode="0750"
  if [ -f "$env_file" ]; then
    info "Keeping existing configuration at $env_file"
    if [ -n "${TOKENHUB_DATABASE_URL:-}" ] &&
      [ "$(read_config_value "$env_file" TOKENHUB_DATABASE_URL)" != "$TOKENHUB_DATABASE_URL" ]; then
      warn "Existing configuration keeps its current TOKENHUB_DATABASE_URL; edit $env_file and restart TokenHub to change databases."
    fi
    load_pending_admin_password
    return
  fi
  if [ "${EUID:-$(id -u)}" -ne 0 ]; then
    config_owner="$SERVICE_USER"
    config_mode="0600"
    directory_mode="0700"
  fi

  local public_host
  local public_base_url
  local frontend_url
  local api_base_url
  local allowed_origins
  local public_url_host
  local database_url
  local admin_token
  local secret_key
  resolve_public_host
  public_host="$RESOLVED_PUBLIC_HOST"
  if [ "$RESOLVED_PUBLIC_HOST_SOURCE" = "hostname" ] ||
    [ "$RESOLVED_PUBLIC_HOST_SOURCE" = "loopback" ]; then
    warn "Public IP lookup failed; generated URLs use $public_host. Set TOKENHUB_PUBLIC_HOST when clients connect through a different public IP or hostname."
  fi
  public_url_host="$(url_host "$public_host")"
  public_base_url="${TOKENHUB_PUBLIC_BASE_URL:-http://${public_url_host}:${BACKEND_PORT}}"
  frontend_url="http://${public_url_host}:${FRONTEND_PORT}"
  api_base_url="${TOKENHUB_API_BASE_URL:-$public_base_url}"
  allowed_origins="${TOKENHUB_CORS_ALLOWED_ORIGINS:-$frontend_url}"
  database_url="$(initial_database_url)"
  admin_token="$(random_hex 32)"
  secret_key="$(random_hex 32)"
  GENERATED_ADMIN_PASSWORD="$(random_hex 12)"

  install -d -m "$directory_mode" -o "$config_owner" -g "$SERVICE_GROUP" "$CONFIG_DIR"
  cat >"$env_file" <<EOF
TOKENHUB_ENV=prod
TOKENHUB_HTTP_ADDR=:${BACKEND_PORT}
TOKENHUB_PUBLIC_HOST=${public_host}
TOKENHUB_PUBLIC_BASE_URL=${public_base_url}
TOKENHUB_RELEASE_REPOSITORY=${GITHUB_REPOSITORY}
TOKENHUB_CORS_ALLOWED_ORIGINS=${allowed_origins}
TOKENHUB_ADMIN_TOKEN=${admin_token}
TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD=${GENERATED_ADMIN_PASSWORD}
TOKENHUB_SECRET_KEY=${secret_key}
TOKENHUB_DATABASE_URL=${database_url}
TOKENHUB_SQLITE_BACKUP_DIR=${STATE_DIR}/backups
TOKENHUB_IMAGE_STORAGE_DIR=${STATE_DIR}/images
TOKENHUB_MODEL_CATALOG_FILE=${INSTALL_ROOT}/current/catalog/model-catalog.yaml
TOKENHUB_PROVIDER_CATALOG_FILE=${INSTALL_ROOT}/current/catalog/provider-catalog.json
TOKENHUB_INSTALL_ROOT=${INSTALL_ROOT}
TOKENHUB_SEED_DEMO=false
TOKENHUB_FRONTEND_HOST=0.0.0.0
TOKENHUB_FRONTEND_PORT=${FRONTEND_PORT}
TOKENHUB_API_BASE_URL=${api_base_url}
EOF
  chown "$config_owner:$SERVICE_GROUP" "$env_file"
  chmod "$config_mode" "$env_file"
  install -m 0600 -o "$config_owner" -g "$SERVICE_GROUP" /dev/null \
    "$CONFIG_DIR/.initial-admin-password-pending"
  info "Created configuration at $env_file"
}

ensure_public_host_config() {
  local env_file="$CONFIG_DIR/tokenhub.env"
  local configured
  configured="$(read_config_value "$env_file" TOKENHUB_PUBLIC_HOST)"
  if [ -n "$configured" ]; then
    return
  fi
  resolve_public_host
  validate_single_line_value "$RESOLVED_PUBLIC_HOST" "TOKENHUB_PUBLIC_HOST"
  printf '\nTOKENHUB_PUBLIC_HOST=%s\n' "$RESOLVED_PUBLIC_HOST" >>"$env_file"
  info "Added the installer public host to $env_file"
}

ensure_persistent_image_storage_config() {
  local env_file="$CONFIG_DIR/tokenhub.env"
  local configured
  configured="$(read_config_value "$env_file" TOKENHUB_IMAGE_STORAGE_DIR)"
  if [ -n "$configured" ]; then
    return
  fi
  printf '\nTOKENHUB_IMAGE_STORAGE_DIR=%s/images\n' "$STATE_DIR" >>"$env_file"
  info "Added persistent image storage to $env_file"
}

prepare_directories() {
  local directory_mode="0750"
  local config_owner="root"
  local config_mode="0750"
  if [ "${EUID:-$(id -u)}" -ne 0 ]; then
    config_owner="$SERVICE_USER"
    config_mode="0700"
  fi
  prepare_managed_directory "$INSTALL_ROOT" "application" "$SERVICE_USER" "$directory_mode"
  prepare_managed_directory "$CONFIG_DIR" "configuration" "$config_owner" "$config_mode"
  prepare_managed_directory "$STATE_DIR" "state" "$SERVICE_USER" "$directory_mode"
  install -d -m "$directory_mode" -o "$SERVICE_USER" -g "$SERVICE_GROUP" "$INSTALL_ROOT/releases"
  install -d -m "$directory_mode" -o "$SERVICE_USER" -g "$SERVICE_GROUP" "$STATE_DIR/backups"
  install -d -m "$directory_mode" -o "$SERVICE_USER" -g "$SERVICE_GROUP" "$STATE_DIR/images"
}

managed_directory_marker_content() {
  local directory="$1"
  local kind="$2"
  printf 'tokenhub-native-managed-directory-v1\nkind=%s\npath=%s\nservice=%s\n' \
    "$kind" "$directory" "$SERVICE_NAME"
}

write_managed_directory_marker() {
  local directory="$1"
  local kind="$2"
  local marker="$directory/$MANAGED_DIRECTORY_MARKER"
  local marker_owner="root"
  if [ "${EUID:-$(id -u)}" -ne 0 ]; then
    marker_owner="$SERVICE_USER"
  fi
  install -m 0600 -o "$marker_owner" -g "$SERVICE_GROUP" /dev/null "$marker"
  managed_directory_marker_content "$directory" "$kind" >"$marker"
  chown "$marker_owner:$SERVICE_GROUP" "$marker"
  chmod 0600 "$marker"
}

require_managed_directory_marker() {
  local directory="$1"
  local kind="$2"
  local marker="$directory/$MANAGED_DIRECTORY_MARKER"
  local expected
  local actual
  [ -f "$marker" ] && [ ! -L "$marker" ] ||
    fail "refusing to remove unmarked $kind directory: $directory"
  expected="$(managed_directory_marker_content "$directory" "$kind")"
  actual="$(cat "$marker")"
  [ "$actual" = "$expected" ] ||
    fail "refusing to remove $kind directory with an invalid ownership marker: $directory"
}

legacy_managed_directory_valid() {
  local directory="$1"
  local kind="$2"
  local env_file="$CONFIG_DIR/tokenhub.env"
  case "$kind" in
    application)
      [ -f "$directory/current/VERSION" ] ||
        { [ -f "$env_file" ] &&
          [ "$(read_config_value "$env_file" TOKENHUB_INSTALL_ROOT)" = "$directory" ]; }
      ;;
    configuration)
      [ -f "$directory/tokenhub.env" ]
      ;;
    state)
      [ -f "$directory/tokenhub.db" ] ||
        { [ -f "$env_file" ] &&
          { [ "$(read_config_value "$env_file" TOKENHUB_DATABASE_URL)" = "sqlite://$directory/tokenhub.db" ] ||
            [ "$(read_config_value "$env_file" TOKENHUB_SQLITE_BACKUP_DIR)" = "$directory/backups" ]; }; }
      ;;
    *)
      return 1
      ;;
  esac
}

prepare_managed_directory() {
  local directory="$1"
  local kind="$2"
  local owner="$3"
  local mode="$4"
  local marker="$directory/$MANAGED_DIRECTORY_MARKER"
  local can_claim=false
  if [ -e "$directory" ] || [ -L "$directory" ]; then
    [ -d "$directory" ] && [ ! -L "$directory" ] ||
      fail "refusing to use a non-directory or symbolic link as $kind directory: $directory"
    if [ -e "$marker" ] || [ -L "$marker" ]; then
      require_managed_directory_marker "$directory" "$kind"
    elif [ -z "$(find "$directory" -mindepth 1 -maxdepth 1 -print -quit)" ] ||
      legacy_managed_directory_valid "$directory" "$kind"; then
      can_claim=true
    else
      fail "refusing to claim non-empty unmarked $kind directory: $directory"
    fi
  else
    can_claim=true
  fi
  install -d -m "$mode" -o "$owner" -g "$SERVICE_GROUP" "$directory"
  if [ "$can_claim" = true ]; then
    write_managed_directory_marker "$directory" "$kind"
  fi
}

sha256_file() {
  local path="$1"
  sha256sum "$path" | awk '{print $1}'
}

download_file() {
  local url="$1"
  local destination="$2"
  local max_bytes="$3"
  local label="$4"
  local file_blocks=$(((max_bytes + 1023) / 1024))
  local downloaded_bytes

  rm -f -- "$destination"
  if ! (
    ulimit -f "$file_blocks"
    curl -fL --retry 3 --connect-timeout 15 -o "$destination" "$url"
  ); then
    rm -f -- "$destination"
    fail "unable to download $label"
  fi
  downloaded_bytes="$(wc -c <"$destination" | tr -d '[:space:]')"
  if ((downloaded_bytes > max_bytes)); then
    rm -f -- "$destination"
    fail "$label exceeds the allowed download size"
  fi
}

download_release() {
  local version="$1"
  local arch
  local asset
  local base_url
  local archive
  local checksums
  local expected
  local actual
  arch="$(platform_arch)"
  asset="tokenhub_${version}_linux_${arch}.tar.gz"
  base_url="https://github.com/${GITHUB_REPOSITORY}/releases/download/v${version}"
  TEMP_DIR="$(mktemp -d)"
  archive="$TEMP_DIR/$asset"
  checksums="$TEMP_DIR/checksums.txt"

  info "Downloading TokenHub v${version} for linux/${arch}"
  download_file "$base_url/$asset" "$archive" "$MAX_NATIVE_ARCHIVE_BYTES" "$asset"
  download_file "$base_url/checksums.txt" "$checksums" "$MAX_NATIVE_CHECKSUM_BYTES" "checksums.txt"

  expected="$(awk -v file="$asset" '$2 == file || $2 == "*" file { print $1; exit }' "$checksums")"
  [[ "$expected" =~ ^[0-9a-fA-F]{64}$ ]] || fail "checksums.txt has no valid entry for $asset"
  actual="$(sha256_file "$archive" | tr '[:upper:]' '[:lower:]')"
  expected="$(printf '%s' "$expected" | tr '[:upper:]' '[:lower:]')"
  [ "$actual" = "$expected" ] || fail "SHA-256 verification failed for $asset"
  DOWNLOADED_ARCHIVE="$archive"
}

validate_release_archive() {
  local archive="$1"
  local entry
  local listing
  local entry_type
  local _entry_mode
  local listing_field_2
  local listing_field_3
  local _listing_field_4
  local listing_field_5
  local entry_size
  local _listing_remainder
  local file_size
  local entry_count=0
  local extracted_bytes=0

  tar -tzf "$archive" >/dev/null || fail "release archive is not a valid gzip-compressed tar file"
  while IFS= read -r entry; do
    entry="${entry#./}"
    [ -z "$entry" ] && continue
    [[ "$entry" != /* ]] || fail "release archive contains an absolute path"
    [[ "/$entry/" != *"/../"* ]] || fail "release archive contains path traversal"
  done < <(tar -tzf "$archive")

  while IFS= read -r listing; do
    [ -z "$listing" ] && continue
    ((entry_count += 1))
    ((entry_count <= MAX_NATIVE_ARCHIVE_ENTRIES)) ||
      fail "release archive contains too many entries"
    entry_type="${listing:0:1}"
    case "$entry_type" in
      -)
        read -r _entry_mode listing_field_2 listing_field_3 _listing_field_4 listing_field_5 _listing_remainder <<<"$listing"
        if [[ "$listing_field_2" == */* ]] && [[ "$listing_field_3" =~ ^[0-9]+$ ]]; then
          entry_size="$listing_field_3"
        elif [[ "$listing_field_2" =~ ^[0-9]+$ ]] && [[ "$listing_field_5" =~ ^[0-9]+$ ]]; then
          entry_size="$listing_field_5"
        else
          entry_size=""
        fi
        [[ "$entry_size" =~ ^[0-9]+$ ]] ||
          fail "release archive contains an unreadable file size"
        [ "$(compare_decimal_identifiers "$entry_size" "$MAX_NATIVE_ARCHIVE_FILE_BYTES")" -le 0 ] ||
          fail "release archive contains an oversized file"
        file_size=$((10#$entry_size))
        ((extracted_bytes <= MAX_NATIVE_EXTRACTED_BYTES - file_size)) ||
          fail "release archive exceeds the allowed extracted size"
        extracted_bytes=$((extracted_bytes + file_size))
        ;;
      d) ;;
      *) fail "release archive contains a link or special file" ;;
    esac
  done < <(LC_ALL=C tar -tvzf "$archive")
}

validate_staged_release() {
  local staging="$1"
  local version="$2"
  local link
  local path

  link="$(find "$staging" -type l -print -quit)"
  [ -z "$link" ] || fail "release archive contains a symbolic link"
  for path in \
    bin/tokenhub \
    bin/node \
    bin/tokenhub-run \
    frontend/server.js \
    catalog/model-catalog.yaml \
    catalog/provider-catalog.json \
    deploy/tokenhub.service \
    VERSION; do
    [ -f "$staging/$path" ] || fail "release archive is missing regular file $path"
  done
  [ "$(tr -d '[:space:]' <"$staging/VERSION")" = "$version" ] ||
    fail "release archive VERSION does not match v$version"
}

activate_release() {
  local version="$1"
  local archive="$2"
  local releases_dir="$INSTALL_ROOT/releases"
  local staging="$releases_dir/.${version}.install.$$"
  local target="$releases_dir/$version"
  local next_link="$INSTALL_ROOT/.current.$$"
  local directory_mode="0750"

  rm -rf -- "$staging"
  install -d -m "$directory_mode" -o "$SERVICE_USER" -g "$SERVICE_GROUP" "$staging"
  validate_release_archive "$archive"
  tar --no-same-owner --no-same-permissions -xzf "$archive" -C "$staging"
  validate_staged_release "$staging" "$version"

  chmod 0755 "$staging/bin/tokenhub" "$staging/bin/node" "$staging/bin/tokenhub-run"
  "$staging/bin/node" --version >/dev/null 2>&1 ||
    fail "bundled Node.js runtime cannot run on this host"
  chown -R "$SERVICE_USER:$SERVICE_GROUP" "$staging"
  if [ -e "$target" ]; then
    rm -rf -- "$target"
  fi
  mv "$staging" "$target"
  ln -s "$target" "$next_link"
  if [ -e "$INSTALL_ROOT/current" ] && [ ! -L "$INSTALL_ROOT/current" ]; then
    rm -f -- "$next_link"
    fail "native current path is not a symbolic link"
  fi
  mv -Tf "$next_link" "$INSTALL_ROOT/current"
  chown -h "$SERVICE_USER:$SERVICE_GROUP" "$INSTALL_ROOT/current"
  info "Activated TokenHub v$version"
}

install_service() {
  local template
  local unit
  template="$INSTALL_ROOT/current/deploy/tokenhub.service"
  unit="$SYSTEMD_DIR/${SERVICE_NAME}.service"
  sed \
    -e "s|@SERVICE_USER@|$SERVICE_USER|g" \
    -e "s|@SERVICE_GROUP@|$SERVICE_GROUP|g" \
    -e "s|@CONFIG_DIR@|$CONFIG_DIR|g" \
    -e "s|@INSTALL_ROOT@|$INSTALL_ROOT|g" \
    -e "s|@STATE_DIR@|$STATE_DIR|g" \
    "$template" >"$unit"
  chmod 0644 "$unit"
  systemctl daemon-reload
  systemctl enable "$SERVICE_NAME"
}

restart_service() {
  systemctl restart "$SERVICE_NAME"
  wait_for_service_ready
}

configured_backend_port() {
  local address
  local port
  address="$(read_config_value "$CONFIG_DIR/tokenhub.env" TOKENHUB_HTTP_ADDR)"
  port="${address##*:}"
  validate_port "$port" "TOKENHUB_HTTP_ADDR port"
  printf '%s\n' "$port"
}

configured_frontend_port() {
  local port
  port="$(read_config_value "$CONFIG_DIR/tokenhub.env" TOKENHUB_FRONTEND_PORT)"
  validate_port "$port" "TOKENHUB_FRONTEND_PORT"
  printf '%s\n' "$port"
}

wait_for_service_ready() {
  local backend_port
  local frontend_port
  local attempt
  backend_port="$(configured_backend_port)"
  frontend_port="$(configured_frontend_port)"
  for ((attempt = 1; attempt <= SERVICE_READY_ATTEMPTS; attempt++)); do
    if systemctl is-active --quiet "$SERVICE_NAME" &&
      curl -fsS --connect-timeout 2 --max-time 3 "http://127.0.0.1:${backend_port}/healthz" >/dev/null 2>&1 &&
      curl -fsS --connect-timeout 2 --max-time 3 "http://127.0.0.1:${frontend_port}/" >/dev/null 2>&1; then
      return
    fi
    sleep 1
  done
  systemctl status --no-pager "$SERVICE_NAME" >&2 || true
  if command -v journalctl >/dev/null 2>&1; then
    journalctl -u "$SERVICE_NAME" -n 50 --no-pager >&2 || true
  fi
  fail "TokenHub did not become ready after ${SERVICE_READY_ATTEMPTS} readiness checks"
}

validate_rollback_target() {
  local current_file="$INSTALL_ROOT/current/VERSION"
  local current_version
  local comparison
  [ -f "$current_file" ] || fail "rollback requires an installed TokenHub release"
  current_version="$(normalize_version "$(tr -d '[:space:]' <"$current_file")")"
  comparison="$(compare_semantic_versions "$VERSION" "$current_version")"
  [ "$comparison" -lt 0 ] ||
    fail "rollback target v$VERSION must be older than installed version v$current_version"
}

validate_upgrade_target() {
  local target_version="$1"
  local current_file="$INSTALL_ROOT/current/VERSION"
  local current_version
  local comparison
  [ -f "$current_file" ] || return 0
  current_version="$(normalize_version "$(tr -d '[:space:]' <"$current_file")")"
  comparison="$(compare_semantic_versions "$target_version" "$current_version")"
  [ "$comparison" -ge 0 ] ||
    fail "upgrade target v$target_version must not be older than installed version v$current_version; use rollback instead"
}

service_status() {
  systemctl status "$SERVICE_NAME"
}

remove_service() {
  systemctl disable --now "$SERVICE_NAME" 2>/dev/null || true
  rm -f -- "$SYSTEMD_DIR/${SERVICE_NAME}.service"
  systemctl daemon-reload
}

install_or_upgrade() {
  local target_version="$VERSION"
  local admin_console_port
  local archive
  validate_fresh_install_ports
  [ -n "$target_version" ] || target_version="$(latest_version)"
  if [ "$COMMAND" = "upgrade" ]; then
    validate_upgrade_target "$target_version"
  fi

  ensure_service_user
  prepare_directories
  write_initial_config
  ensure_public_host_config
  ensure_persistent_image_storage_config
  record_created_service_user
  if [ -f "$INSTALL_ROOT/current/VERSION" ] &&
    [ "$(tr -d '[:space:]' <"$INSTALL_ROOT/current/VERSION")" = "$target_version" ]; then
    info "TokenHub v$target_version is already installed"
    install_service
    restart_service
    show_initial_admin_credentials
    return
  fi
  download_release "$target_version"
  archive="$DOWNLOADED_ARCHIVE"
  activate_release "$target_version" "$archive"
  install_service
  restart_service

  admin_console_port="$(configured_frontend_port)"
  resolve_public_host
  info "TokenHub v$target_version is running"
  info "Admin console: http://$(url_host "$RESOLVED_PUBLIC_HOST"):${admin_console_port}"
  show_initial_admin_credentials
  info "Configuration: $CONFIG_DIR/tokenhub.env"
  info "Logs: journalctl -u ${SERVICE_NAME} -f"
}

uninstall_tokenhub() {
  local remove_service_user=false
  local marker="$CONFIG_DIR/.service-user-created"
  local expected_user=""
  local expected_uid=""
  if [ -f "$marker" ]; then
    IFS=: read -r expected_user expected_uid <"$marker" || true
    if [ "$expected_user" = "$SERVICE_USER" ] &&
      [ -n "$expected_uid" ] &&
      [ "$(id -u "$SERVICE_USER" 2>/dev/null || true)" = "$expected_uid" ]; then
      remove_service_user=true
    fi
  fi

  if [ -e "$INSTALL_ROOT" ]; then
    require_managed_directory_marker "$INSTALL_ROOT" "application"
  fi
  if [ "$PURGE" = true ]; then
    [ ! -e "$CONFIG_DIR" ] || require_managed_directory_marker "$CONFIG_DIR" "configuration"
    [ ! -e "$STATE_DIR" ] || require_managed_directory_marker "$STATE_DIR" "state"
  fi

  remove_service
  rm -rf -- "$INSTALL_ROOT"
  if [ "$PURGE" = true ]; then
    rm -rf -- "$CONFIG_DIR" "$STATE_DIR"
    if [ "$remove_service_user" = true ]; then
      userdel "$SERVICE_USER" 2>/dev/null || true
    fi
    info "Removed application, configuration, and data"
  else
    info "Removed application; preserved $CONFIG_DIR and $STATE_DIR"
  fi
}

main() {
  trap cleanup EXIT
  parse_args "$@"
  require_root
  validate_safe_path "$INSTALL_ROOT" "TOKENHUB_INSTALL_ROOT"
  validate_safe_path "$CONFIG_DIR" "TOKENHUB_CONFIG_DIR"
  validate_safe_path "$STATE_DIR" "TOKENHUB_STATE_DIR"
  validate_safe_path "$SYSTEMD_DIR" "TOKENHUB_SYSTEMD_DIR"
  validate_identifiers

  case "$COMMAND" in
    install|upgrade)
      require_platform
      install_or_upgrade
      ;;
    rollback)
      require_platform
      [ -n "$VERSION" ] || fail "rollback requires --version"
      validate_rollback_target
      install_or_upgrade
      ;;
    uninstall)
      require_platform
      uninstall_tokenhub
      ;;
    status)
      require_platform
      service_status
      ;;
    help)
      usage
      ;;
    *)
      fail "unknown command: $COMMAND"
      ;;
  esac
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
