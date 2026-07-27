#!/usr/bin/env bash
# Install or upgrade TokenHub on a Linux host with systemd, without Docker.
#
# Run from inside a release directory produced by deploy/bare-metal/build.sh:
#   sudo ./install.sh --generate-secrets
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PAYLOAD_DIR="$SCRIPT_DIR"

PREFIX_DIR=/opt/tokenhub
CONFIG_DIR=/etc/tokenhub
STATE_DIR=/var/lib/tokenhub
UNIT_DIR=/etc/systemd/system
BACKEND_USER=tokenhub
FRONTEND_USER=tokenhub-web
BACKEND_UNIT=tokenhub-backend.service
FRONTEND_UNIT=tokenhub-frontend.service
# Keep in sync with TimeoutStopSec in tokenhub-backend.service.
BACKEND_STOP_TIMEOUT=180
KEEP_RELEASES=2
READY_TIMEOUT=90

GENERATE_SECRETS=false
START_MODE=auto
SKIP_BACKUP=false
INSTALL_TIMERS=false

log() { printf '[tokenhub] %s\n' "$*"; }
warn() { printf '[tokenhub] WARNING: %s\n' "$*" >&2; }
error() { printf '[tokenhub] ERROR: %s\n' "$*" >&2; }

usage() {
  cat <<'EOF'
Usage: sudo ./install.sh [options]

Options:
  --payload DIR       Release directory to install from (default: this directory).
  --generate-secrets  Replace placeholder secrets in a freshly created
                      /etc/tokenhub/backend.env with generated values.
  --install-timers    Also install the daily SQLite backup timer.
  --no-start          Install and upgrade but leave the services stopped.
  --start             Start even if this looks like a configuration-only run.
  --skip-backup       Skip the pre-upgrade database snapshot (not recommended:
                      startup runs AutoMigrate, which a symlink rollback cannot undo).
  -h, --help          Show this help message.
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --payload)
      [ "$#" -ge 2 ] || { error "--payload requires a path"; exit 2; }
      PAYLOAD_DIR="$(cd "$2" && pwd)"
      shift 2
      ;;
    --generate-secrets) GENERATE_SECRETS=true; shift ;;
    --install-timers) INSTALL_TIMERS=true; shift ;;
    --no-start) START_MODE=never; shift ;;
    --start) START_MODE=always; shift ;;
    --skip-backup) SKIP_BACKUP=true; shift ;;
    -h|--help) usage; exit 0 ;;
    *) error "Unknown argument: $1"; usage >&2; exit 2 ;;
  esac
done

# --- preflight ---------------------------------------------------------------

[ "$(id -u)" -eq 0 ] || { error "This installer must run as root"; exit 1; }
[ -d /run/systemd/system ] || { error "systemd is not the init system on this host"; exit 1; }
command -v systemctl >/dev/null 2>&1 || { error "systemctl not found"; exit 1; }

# This installer and the runtime scripts use GNU-specific behaviour that BSD
# userland does not provide. Every mainstream Linux distribution satisfies this;
# check anyway so a mismatch fails here instead of halfway through an upgrade.
gnu_probe_dir="$(mktemp -d)"
gnu_userland=true
: >"$gnu_probe_dir/a" || gnu_userland=false
: >"$gnu_probe_dir/b" || gnu_userland=false
chmod --reference="$gnu_probe_dir/a" "$gnu_probe_dir/b" >/dev/null 2>&1 || gnu_userland=false
chown --reference="$gnu_probe_dir/a" "$gnu_probe_dir/b" >/dev/null 2>&1 || gnu_userland=false
mktemp -u --tmpdir="$gnu_probe_dir" --suffix=.probe "x.XXXXXX" >/dev/null 2>&1 || gnu_userland=false
find "$gnu_probe_dir" -maxdepth 0 -printf '%p\n' >/dev/null 2>&1 || gnu_userland=false
mkdir -p "$gnu_probe_dir/c" "$gnu_probe_dir/d" >/dev/null 2>&1 || gnu_userland=false
mv -T "$gnu_probe_dir/c" "$gnu_probe_dir/d" >/dev/null 2>&1 || gnu_userland=false
command -v sha256sum >/dev/null 2>&1 || gnu_userland=false
rm -rf "$gnu_probe_dir"
if [ "$gnu_userland" = false ]; then
  error "This host lacks the GNU coreutils/findutils behaviour the installer requires"
  error "(chmod/chown --reference, mktemp --tmpdir/--suffix, find -printf, mv -T, sha256sum)."
  exit 1
fi

for required in VERSION SHA256SUMS bin/tokenhub web/server.js catalog/model-catalog.yaml \
  backend.env.example frontend.env.example scripts/parse-env-file.mjs \
  systemd/"$BACKEND_UNIT" systemd/"$FRONTEND_UNIT"; do
  [ -e "$PAYLOAD_DIR/$required" ] || { error "Payload is incomplete: missing $required"; exit 1; }
done

# Configuration is read through systemd's own EnvironmentFile grammar rather than
# an approximation of it, so the installer validates what the service receives.
ENV_PARSER="$PAYLOAD_DIR/scripts/parse-env-file.mjs"

RELEASE_VERSION="$(sed -n 's/^version=//p' "$PAYLOAD_DIR/VERSION" | head -n 1)"
[ -n "$RELEASE_VERSION" ] || { error "Could not read version= from $PAYLOAD_DIR/VERSION"; exit 1; }

PAYLOAD_ARCH="$(sed -n 's/^arch=//p' "$PAYLOAD_DIR/VERSION" | head -n 1)"
HOST_ARCH="$(uname -m)"
case "$HOST_ARCH" in
  x86_64) HOST_ARCH=amd64 ;;
  aarch64) HOST_ARCH=arm64 ;;
esac
if [ -n "$PAYLOAD_ARCH" ] && [ "$PAYLOAD_ARCH" != "$HOST_ARCH" ]; then
  error "Release was built for $PAYLOAD_ARCH but this host is $HOST_ARCH"
  exit 1
fi

# The console runs on Node. ProtectHome=true in the unit files means an
# interpreter under a user's home directory (nvm, asdf) is unreachable at runtime.
NODE_BIN="$(command -v node || true)"
[ -n "$NODE_BIN" ] || { error "Node.js is required on this host but was not found"; exit 1; }
NODE_BIN="$(readlink -f "$NODE_BIN")"
case "$NODE_BIN" in
  /home/*|/root/*)
    error "node resolves to $NODE_BIN, which the hardened units cannot read (ProtectHome=true)."
    error "Install Node system-wide, for example under /usr/local."
    exit 1
    ;;
esac
NODE_MAJOR="$("$NODE_BIN" -p 'process.versions.node.split(".")[0]')"
if [ "$NODE_MAJOR" -lt 22 ]; then
  error "Node 22 or newer is required (found $("$NODE_BIN" --version))"
  exit 1
fi

log "Installing TokenHub $RELEASE_VERSION from $PAYLOAD_DIR"

# --- helpers -----------------------------------------------------------------

# Confirms the file parses under systemd's own EnvironmentFile grammar. Anything
# the parser rejects would also be rejected or misread by systemd itself.
validate_env_file_syntax() {
  local file="$1"
  [ -f "$file" ] || return 0
  if ! "$NODE_BIN" "$ENV_PARSER" "$file" >/dev/null; then
    error "$file cannot be parsed as a systemd EnvironmentFile."
    return 1
  fi
  return 0
}

# Reads one value using the same grammar systemd applies — quoting, C-style
# escapes and backslash continuations included — so the installer validates
# exactly what the service will receive. Sourcing the file is never an option:
# its syntax is not shell syntax, so sourcing would execute its contents.
env_get() {
  local file="$1" key="$2"
  [ -f "$file" ] || return 0
  "$NODE_BIN" "$ENV_PARSER" "$file" "$key" 2>/dev/null || true
}

env_set() {
  local file="$1" key="$2" value="$3" tmp
  if grep -qE "^[[:space:]]*${key}=" "$file"; then
    # The temporary file is created next to the target, never in /tmp: it holds
    # secrets, and a same-directory rename is atomic.
    tmp="$(mktemp "$file.XXXXXX")"
    chmod 0600 "$tmp"
    # The match must accept the same leading whitespace grep did, otherwise an
    # indented assignment is reported as replaced but left untouched.
    KEY="$key" VALUE="$value" awk '
      {
        stripped = $0
        sub(/^[[:space:]]+/, "", stripped)
        if (index(stripped, ENVIRON["KEY"] "=") == 1) {
          print ENVIRON["KEY"] "=" ENVIRON["VALUE"]
          next
        }
        print
      }
    ' "$file" >"$tmp"
    chmod --reference="$file" "$tmp"
    chown --reference="$file" "$tmp"
    mv -f "$tmp" "$file"
  else
    printf '%s=%s\n' "$key" "$value" >>"$file"
  fi
}

ensure_account() {
  local user="$1" home="$2"
  if ! getent group "$user" >/dev/null 2>&1; then
    # useradd does not portably create a matching group, so make it explicitly.
    groupadd --system "$user"
  fi
  if ! getent passwd "$user" >/dev/null 2>&1; then
    local shell=""
    for candidate in /usr/sbin/nologin /sbin/nologin /usr/bin/nologin /bin/false; do
      if [ -x "$candidate" ]; then
        shell="$candidate"
        break
      fi
    done
    [ -n "$shell" ] || { error "No nologin shell found on this host"; exit 1; }
    useradd --system --gid "$user" --home-dir "$home" --no-create-home --shell "$shell" "$user"
    log "Created system user $user"
  fi
}

unit_is_active() {
  systemctl is-active --quiet "$1" 2>/dev/null
}

http_ok() {
  "$NODE_BIN" -e "
    const url = process.argv[1];
    fetch(url, { redirect: 'manual' })
      .then(r => process.exit(r.status < 500 ? 0 : 1))
      .catch(() => process.exit(1));
  " "$1" >/dev/null 2>&1
}

wait_for_http() {
  local url="$1" label="$2" deadline=$((SECONDS + READY_TIMEOUT))
  while [ "$SECONDS" -lt "$deadline" ]; do
    if http_ok "$url"; then
      log "$label is responding at $url"
      return 0
    fi
    sleep 2
  done
  return 1
}

report_failure() {
  local unit="$1"
  error "$unit did not become ready; recent status and logs follow:"
  systemctl status --no-pager --lines=0 "$unit" >&2 || true
  journalctl -u "$unit" --no-pager --lines=40 >&2 || true
}

# --- accounts and directories ------------------------------------------------

ensure_account "$BACKEND_USER" "$STATE_DIR"
ensure_account "$FRONTEND_USER" /nonexistent

install -d -m 0755 -o root -g root "$PREFIX_DIR" "$PREFIX_DIR/releases"
install -d -m 0755 -o root -g root "$CONFIG_DIR"
# Matches StateDirectory/StateDirectoryMode in the backend unit; created here too
# so a pre-upgrade snapshot can be written before the service has ever run.
install -d -m 0750 -o "$BACKEND_USER" -g "$BACKEND_USER" "$STATE_DIR"

# --- configuration -----------------------------------------------------------

FIRST_CONFIG=false
install -m 0640 -o root -g "$BACKEND_USER" "$PAYLOAD_DIR/backend.env.example" "$CONFIG_DIR/backend.env.example"
install -m 0640 -o root -g "$FRONTEND_USER" "$PAYLOAD_DIR/frontend.env.example" "$CONFIG_DIR/frontend.env.example"

if [ ! -f "$CONFIG_DIR/backend.env" ]; then
  install -m 0640 -o root -g "$BACKEND_USER" "$PAYLOAD_DIR/backend.env.example" "$CONFIG_DIR/backend.env"
  FIRST_CONFIG=true
  log "Created $CONFIG_DIR/backend.env"
fi
if [ ! -f "$CONFIG_DIR/frontend.env" ]; then
  install -m 0640 -o root -g "$FRONTEND_USER" "$PAYLOAD_DIR/frontend.env.example" "$CONFIG_DIR/frontend.env"
  log "Created $CONFIG_DIR/frontend.env"
fi

# An existing configuration is never overwritten, so new releases can introduce
# variables it does not have. Surface that drift instead of failing silently.
report_config_drift() {
  local live="$1" example="$2" label="$3" missing
  missing="$(comm -23 \
    <(grep -oE '^[[:space:]]*#?[A-Za-z_][A-Za-z0-9_]*=' "$example" | tr -d ' #' | sed 's/=$//' | sort -u) \
    <(grep -oE '^[[:space:]]*#?[A-Za-z_][A-Za-z0-9_]*=' "$live" | tr -d ' #' | sed 's/=$//' | sort -u) || true)"
  if [ -n "$missing" ]; then
    warn "$label is missing variables present in this release:"
    printf '  %s\n' $missing >&2
    warn "Compare with $example before starting."
  fi
}
if [ "$FIRST_CONFIG" = false ]; then
  report_config_drift "$CONFIG_DIR/backend.env" "$CONFIG_DIR/backend.env.example" "$CONFIG_DIR/backend.env"
fi

# Fail loudly on syntax this installer parses differently from systemd, rather
# than validating one value while the service receives another.
validate_env_file_syntax "$CONFIG_DIR/backend.env" || exit 1
validate_env_file_syntax "$CONFIG_DIR/frontend.env" || exit 1

if [ "$GENERATE_SECRETS" = true ]; then
  command -v openssl >/dev/null 2>&1 || { error "--generate-secrets requires openssl"; exit 1; }
  generated=false
  for entry in "TOKENHUB_SECRET_KEY:32" "TOKENHUB_ADMIN_TOKEN:32" "TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD:12"; do
    key="${entry%%:*}"
    current="$(env_get "$CONFIG_DIR/backend.env" "$key")"
    case "$current" in
      change-me-*|dev_admin_token|dev_tokenhub_secret_key|admin123456|"")
        env_set "$CONFIG_DIR/backend.env" "$key" "$(openssl rand -hex 24)"
        log "Generated $key"
        generated=true
        ;;
      *) ;;
    esac
  done
  if [ "$generated" = false ]; then
    log "All secrets already set; nothing generated"
  fi
fi

# Mirror the backend's own ValidateForStartup so an invalid configuration is
# caught here rather than as a boot-time crash loop.
CONFIG_VALID=true
CONFIG_PROBLEMS=()
TOKENHUB_ENV_VALUE="$(env_get "$CONFIG_DIR/backend.env" TOKENHUB_ENV)"
case "$(printf '%s' "${TOKENHUB_ENV_VALUE:-prod}" | tr '[:upper:]' '[:lower:]')" in
  dev|development|local|test) ;;
  *)
    check_secret() {
      local key="$1" minimum="$2" value
      value="$(env_get "$CONFIG_DIR/backend.env" "$key")"
      case "$value" in
        change-me-*|dev_admin_token|dev_tokenhub_secret_key|admin123456)
          CONFIG_PROBLEMS+=("$key still holds a placeholder value")
          CONFIG_VALID=false
          return
          ;;
      esac
      if [ "${#value}" -lt "$minimum" ]; then
        CONFIG_PROBLEMS+=("$key must be at least $minimum characters")
        CONFIG_VALID=false
      fi
    }
    check_secret TOKENHUB_SECRET_KEY 32
    check_secret TOKENHUB_ADMIN_TOKEN 32
    check_secret TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD 12
    ;;
esac

GRACEFUL="$(env_get "$CONFIG_DIR/backend.env" TOKENHUB_GRACEFUL_SHUTDOWN_SECONDS)"
GRACEFUL="${GRACEFUL:-150}"
if [ "$GRACEFUL" -ge "$BACKEND_STOP_TIMEOUT" ] 2>/dev/null; then
  CONFIG_PROBLEMS+=("TOKENHUB_GRACEFUL_SHUTDOWN_SECONDS=$GRACEFUL must stay below TimeoutStopSec=$BACKEND_STOP_TIMEOUT, otherwise systemd kills the backend mid-drain")
  CONFIG_VALID=false
fi

DATABASE_URL="$(env_get "$CONFIG_DIR/backend.env" TOKENHUB_DATABASE_URL)"
# This installer manages SQLite deployments only. The backend itself also speaks
# PostgreSQL, but pointing it at one means taking over backups, preflight checks
# and the pre-upgrade snapshot yourself, so refuse rather than half-support it.
case "$DATABASE_URL" in
  postgres://*|postgresql://*|*host=*|*hostaddr=*|*dbname=*)
    CONFIG_PROBLEMS+=("TOKENHUB_DATABASE_URL points at PostgreSQL, which this installer does not manage; use SQLite or deploy PostgreSQL yourself")
    CONFIG_VALID=false
    ;;
esac
if [ -n "$(env_get "$CONFIG_DIR/backend.env" TOKENHUB_DB_HOST)" ]; then
  CONFIG_PROBLEMS+=("TOKENHUB_DB_HOST is set, which selects PostgreSQL; this installer manages SQLite deployments only")
  CONFIG_VALID=false
fi
# An unset value is not the same as the default: the installer would snapshot and
# grant one path while the backend fell back to its own CWD-relative default
# (data/tokenhub.db) inside the read-only release directory, and fail after the
# switch. Require both to be stated explicitly.
if [ -z "$DATABASE_URL" ]; then
  CONFIG_PROBLEMS+=("TOKENHUB_DATABASE_URL is not set; set it to an absolute sqlite:// path such as sqlite://$STATE_DIR/tokenhub.db")
  CONFIG_VALID=false
  SQLITE_PATH=""
else
  SQLITE_PATH="${DATABASE_URL#sqlite://}"
  case "$SQLITE_PATH" in
    /*) ;;
    *)
      CONFIG_PROBLEMS+=("TOKENHUB_DATABASE_URL must use an absolute path; a relative path resolves inside the read-only release directory")
      CONFIG_VALID=false
      ;;
  esac
fi

if [ -z "$(env_get "$CONFIG_DIR/backend.env" TOKENHUB_SQLITE_BACKUP_DIR)" ]; then
  CONFIG_PROBLEMS+=("TOKENHUB_SQLITE_BACKUP_DIR is not set; set it to an absolute path such as $STATE_DIR/backups")
  CONFIG_VALID=false
fi

# Backup directories are validated here, before anything is stopped or switched:
# discovering an unusable path only while writing the unit drop-in would abort a
# half-finished upgrade.
validate_backup_dir() {
  local label="$1" dir="$2"
  [ -n "$dir" ] || return 0
  case "$dir" in
    /*) ;;
    *)
      CONFIG_PROBLEMS+=("$label must be an absolute path")
      CONFIG_VALID=false
      return 0
      ;;
  esac
  case "$dir" in
    *\"*|*\\*)
      CONFIG_PROBLEMS+=("$label contains a quote or backslash, which cannot be written into a systemd drop-in")
      CONFIG_VALID=false
      ;;
  esac
  case "$dir" in
    /home/*|/root/*)
      CONFIG_PROBLEMS+=("$label is under a home directory, which ProtectHome=true makes unreachable for the service")
      CONFIG_VALID=false
      ;;
  esac
}

validate_backup_dir TOKENHUB_SQLITE_BACKUP_DIR "$(env_get "$CONFIG_DIR/backend.env" TOKENHUB_SQLITE_BACKUP_DIR)"

# --- upgrade helpers ---------------------------------------------------------

IS_UPGRADE=false
[ -L "$PREFIX_DIR/current" ] && IS_UPGRADE=true

# Never take a working installation down for a configuration that cannot start.
# On a first install there is nothing to protect, so the run continues and simply
# leaves the services stopped.
if [ "$CONFIG_VALID" = false ] && [ "$IS_UPGRADE" = true ]; then
  error "Refusing to upgrade: the configuration would not start, and the running version was left untouched."
  printf '  - %s\n' "${CONFIG_PROBLEMS[@]}" >&2
  error "Fix $CONFIG_DIR/backend.env and run this installer again."
  exit 1
fi

# Remembers what was running so a failed install restarts exactly that, rather
# than starting services the operator had deliberately stopped.
BACKEND_WAS_ACTIVE=false
FRONTEND_WAS_ACTIVE=false

stop_services() {
  local unit
  for unit in "$FRONTEND_UNIT" "$BACKEND_UNIT"; do
    if systemctl list-unit-files "$unit" >/dev/null 2>&1 && unit_is_active "$unit"; then
      case "$unit" in
        "$BACKEND_UNIT") BACKEND_WAS_ACTIVE=true ;;
        "$FRONTEND_UNIT") FRONTEND_WAS_ACTIVE=true ;;
      esac
      log "Stopping $unit"
      systemctl stop "$unit"
    fi
  done
}

snapshot_sqlite() {
  local db="$1" target_dir="$STATE_DIR/pre-upgrade" stamp tmp final
  [ -f "$db" ] || { log "No existing SQLite database at $db; nothing to snapshot"; return 0; }
  # A clean shutdown leaves no sidecar files. If any survive, the database was not
  # closed cleanly and copying the main file alone would produce a corrupt copy.
  local sidecar
  for sidecar in "$db-wal" "$db-shm" "$db-journal"; do
    if [ -e "$sidecar" ]; then
      error "$sidecar still exists after stopping the backend: the database was not closed cleanly."
      error "Start the service, take a backup through the admin console, then retry."
      return 1
    fi
  done

  # This function is called as `snapshot_sqlite ... || exit 1`, which disables
  # errexit for everything inside it. Every step therefore has to check itself;
  # a silently skipped copy would leave the upgrade with no way back.
  install -d -m 0750 -o "$BACKEND_USER" -g "$BACKEND_USER" "$target_dir" || {
    error "Could not create $target_dir"
    return 1
  }
  stamp="$(date -u +%Y%m%dT%H%M%SZ)"
  # mktemp allocates both names atomically, so a second run in the same second
  # cannot collide with this one.
  final="$(mktemp --tmpdir="$target_dir" --suffix=.db "tokenhub-$stamp.XXXXXX")" || {
    error "Could not allocate a snapshot file in $target_dir"
    return 1
  }
  tmp="$(mktemp --tmpdir="$target_dir" ".tokenhub-$stamp.XXXXXX")" || {
    error "Could not allocate a temporary file in $target_dir"
    rm -f "$final"
    return 1
  }

  local step_failed=false
  cp "$db" "$tmp" || step_failed=true
  if [ "$step_failed" = false ]; then
    chown "$BACKEND_USER:$BACKEND_USER" "$tmp" || step_failed=true
  fi
  if [ "$step_failed" = false ]; then
    chmod 0600 "$tmp" || step_failed=true
  fi
  if [ "$step_failed" = false ]; then
    sha256sum "$tmp" | awk '{print $1}' >"$final.sha256" || step_failed=true
  fi
  if [ "$step_failed" = false ]; then
    chown "$BACKEND_USER:$BACKEND_USER" "$final.sha256" && chmod 0640 "$final.sha256" || step_failed=true
  fi
  if [ "$step_failed" = false ]; then
    chown "$BACKEND_USER:$BACKEND_USER" "$final" && chmod 0600 "$final" || step_failed=true
  fi
  if [ "$step_failed" = false ]; then
    mv -f "$tmp" "$final" || step_failed=true
  fi

  if [ "$step_failed" = true ]; then
    error "Pre-upgrade SQLite snapshot failed while writing $final"
    rm -f "$tmp" "$final" "$final.sha256"
    return 1
  fi

  log "Pre-upgrade snapshot: $final"
  return 0
}

# --- stage the release -------------------------------------------------------

RELEASE_DIR="$PREFIX_DIR/releases/$RELEASE_VERSION"
if [ -e "$RELEASE_DIR" ]; then
  RELEASE_DIR="$PREFIX_DIR/releases/$RELEASE_VERSION-$(date -u +%Y%m%dT%H%M%SZ)"
fi

STAGE_DIR="$(mktemp -d "$PREFIX_DIR/.stage.XXXXXXXX")"
# Preserves the pending exit status: in bash, the last command of an EXIT trap
# becomes the script's exit code.
cleanup_stage() {
  local status=$?
  if [ -d "$STAGE_DIR" ]; then
    rm -rf "$STAGE_DIR"
  fi
  return "$status"
}
trap cleanup_stage EXIT

log "Staging release payload"
cp -a "$PAYLOAD_DIR/." "$STAGE_DIR/"
if command -v sha256sum >/dev/null 2>&1; then
  log "Verifying checksums"
  (cd "$STAGE_DIR" && sha256sum --quiet --check SHA256SUMS) || {
    error "Checksum verification failed; refusing to install this payload"
    exit 1
  }
else
  warn "sha256sum not found; skipping checksum verification"
fi

chown -R root:root "$STAGE_DIR"
# Do not inherit the build host's umask: a release built under umask 077 would be
# unreadable to both service users, which fails at ExecStart rather than here.
chmod -R u=rwX,go=rX "$STAGE_DIR"
mv -T "$STAGE_DIR" "$RELEASE_DIR"
trap - EXIT

# --- stop, snapshot, activate ------------------------------------------------

# Remember what `current` pointed at before the switch. Pruning must never delete
# this directory: it is the only thing a rollback can point back at.
PREVIOUS_TARGET=""
if [ -L "$PREFIX_DIR/current" ]; then
  PREVIOUS_TARGET="$(readlink -f "$PREFIX_DIR/current" 2>/dev/null || true)"
fi

# From here until the switch succeeds, a failure must not leave the host with a
# stopped service and an orphaned release directory.
RELEASE_ACTIVATED=false
abort_release() {
  local status=$? active_target=""
  if [ "$RELEASE_ACTIVATED" = false ]; then
    # A signal can land between the successful switch and RELEASE_ACTIVATED=true.
    # Deleting the directory `current` already points at would leave a dangling
    # symlink and an unstartable installation, so check the link itself.
    active_target="$(readlink -f "$PREFIX_DIR/current" 2>/dev/null || true)"
    if [ -d "$RELEASE_DIR" ] && [ "$active_target" != "$RELEASE_DIR" ]; then
      warn "Removing the staged release $RELEASE_DIR"
      rm -rf "$RELEASE_DIR"
      if [ -n "$PREVIOUS_TARGET" ] && [ -d "$PREVIOUS_TARGET" ]; then
        if [ "$BACKEND_WAS_ACTIVE" = true ] || [ "$FRONTEND_WAS_ACTIVE" = true ]; then
          warn "Restarting the previously installed version"
        fi
        [ "$BACKEND_WAS_ACTIVE" = true ] && { systemctl start "$BACKEND_UNIT" >/dev/null 2>&1 || true; }
        [ "$FRONTEND_WAS_ACTIVE" = true ] && { systemctl start "$FRONTEND_UNIT" >/dev/null 2>&1 || true; }
      fi
    fi
  fi
  return "$status"
}
trap abort_release EXIT

# Stop before switching so no running process sees a mixture of the old working
# directory and new absolute /opt/tokenhub/current paths.
stop_services

if [ "$IS_UPGRADE" = true ]; then
  if [ "$SKIP_BACKUP" = true ]; then
    warn "Skipping the pre-upgrade snapshot as requested (--skip-backup)"
  else
    snapshot_sqlite "$SQLITE_PATH" || exit 1
  fi
fi

ln -sfn "$RELEASE_DIR" "$PREFIX_DIR/.current.tmp"
mv -T "$PREFIX_DIR/.current.tmp" "$PREFIX_DIR/current"
RELEASE_ACTIVATED=true
trap - EXIT
log "Activated $RELEASE_DIR"

# Keep the previous release so a rollback only needs the symlink (plus the
# database snapshot taken above, because AutoMigrate already ran). Retention is
# driven by what was actually active, not by directory timestamps: payload mtimes
# are preserved from the build host and do not reflect installation order.
CURRENT_TARGET="$(readlink -f "$PREFIX_DIR/current")"
if [ "$KEEP_RELEASES" -gt 1 ] && [ -n "$PREVIOUS_TARGET" ] && [ "$PREVIOUS_TARGET" != "$CURRENT_TARGET" ]; then
  log "Keeping $PREVIOUS_TARGET for rollback"
fi
while IFS= read -r release; do
  [ -n "$release" ] || continue
  [ "$release" = "$CURRENT_TARGET" ] && continue
  [ "$release" = "$PREVIOUS_TARGET" ] && [ "$KEEP_RELEASES" -gt 1 ] && continue
  log "Removing old release $release"
  rm -rf "$release"
done < <(find "$PREFIX_DIR/releases" -mindepth 1 -maxdepth 1 -type d)

# --- units -------------------------------------------------------------------

install -m 0644 -o root -g root "$PREFIX_DIR/current/systemd/$BACKEND_UNIT" "$UNIT_DIR/$BACKEND_UNIT"
install -m 0644 -o root -g root "$PREFIX_DIR/current/systemd/$FRONTEND_UNIT" "$UNIT_DIR/$FRONTEND_UNIT"

write_dropin() {
  local unit="$1" name="$2" content="$3"
  install -d -m 0755 "$UNIT_DIR/$unit.d"
  printf '%s\n' "$content" >"$UNIT_DIR/$unit.d/$name"
  chmod 0644 "$UNIT_DIR/$unit.d/$name"
}

remove_dropin() {
  rm -f "$UNIT_DIR/$1.d/$2" 2>/dev/null || true
}

# Units that run Node invoke `env node`, which resolves against systemd's default
# PATH. When Node lives outside it, pin the absolute interpreter path instead.
NODE_DIR="$(dirname "$NODE_BIN")"
NODE_ON_DEFAULT_PATH=false
case ":/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:" in
  *":$NODE_DIR:"*) NODE_ON_DEFAULT_PATH=true ;;
esac

pin_node_interpreter() {
  local unit="$1" script="$2"
  if [ "$NODE_ON_DEFAULT_PATH" = true ]; then
    remove_dropin "$unit" 10-node-path.conf
    return 0
  fi
  write_dropin "$unit" 10-node-path.conf "[Service]
ExecStart=
ExecStart=$NODE_BIN $script"
}

# A backup directory outside the unit's StateDirectory is not writable under
# ProtectSystem=strict, so grant it explicitly instead of failing at runtime.
grant_backup_dir() {
  local unit="$1" dir="$2"
  case "$dir" in
    "$STATE_DIR"|"$STATE_DIR"/*)
      remove_dropin "$unit" 20-backup-path.conf
      return 0
      ;;
    /*) ;;
    *)
      warn "Backup directory \"$dir\" is not absolute; leaving the unit sandbox unchanged"
      return 0
      ;;
  esac
  install -d -m 0750 -o "$BACKEND_USER" -g "$BACKEND_USER" "$dir"
  # systemd expands % specifiers in unit values, and a path with spaces needs
  # quoting. Both are expressible; validate_backup_dir has already rejected the
  # characters that are not (quotes and backslashes).
  local escaped="${dir//%/%%}"
  write_dropin "$unit" 20-backup-path.conf "[Service]
ReadWritePaths=\"$escaped\""
  log "Granted $unit write access to $dir"
}

pin_node_interpreter "$FRONTEND_UNIT" "$PREFIX_DIR/current/web/server.js"
[ "$NODE_ON_DEFAULT_PATH" = false ] && log "Pinned Node interpreter to $NODE_BIN"

# Validated as non-empty above, so no fallback here: a fallback would grant one
# directory while the backend wrote to another.
SQLITE_BACKUP_DIR="$(env_get "$CONFIG_DIR/backend.env" TOKENHUB_SQLITE_BACKUP_DIR)"
grant_backup_dir "$BACKEND_UNIT" "$SQLITE_BACKUP_DIR"

if [ "$INSTALL_TIMERS" = true ]; then
  install -m 0644 -o root -g root "$PREFIX_DIR/current/systemd/tokenhub-backup.service" "$UNIT_DIR/"
  install -m 0644 -o root -g root "$PREFIX_DIR/current/systemd/tokenhub-backup.timer" "$UNIT_DIR/"
  pin_node_interpreter tokenhub-backup.service "$PREFIX_DIR/current/scripts/backup-sqlite.mjs"
  grant_backup_dir tokenhub-backup.service "$SQLITE_BACKUP_DIR"
  BACKUP_TIMER=tokenhub-backup.timer
fi

systemctl daemon-reload

# --- start -------------------------------------------------------------------

if [ "$CONFIG_VALID" = false ]; then
  # An upgrade may arrive on a host where the units are already enabled. Leaving
  # them enabled with an invalid configuration would crash-loop them at the next
  # boot, so disable them explicitly rather than merely not enabling them.
  # --now, so an already-running unit or an armed timer stops immediately rather
  # than continuing until the next reboot.
  systemctl disable --now "$BACKEND_UNIT" "$FRONTEND_UNIT" >/dev/null 2>&1 || true
  # The timer too: it would otherwise keep firing against an unusable service.
  systemctl disable --now tokenhub-backup.timer >/dev/null 2>&1 || true
  error "Configuration is not ready, so the services were installed but left disabled:"
  printf '  - %s\n' "${CONFIG_PROBLEMS[@]}" >&2
  cat >&2 <<EOF

Edit $CONFIG_DIR/backend.env (or re-run with --generate-secrets), then:

  sudo systemctl enable --now $BACKEND_UNIT $FRONTEND_UNIT

Leaving an invalid unit enabled would crash-loop it on the next boot, so nothing
was enabled.
EOF
  exit 1
fi

if [ "$START_MODE" = never ]; then
  log "Installed. Services were not started (--no-start)."
  log "Start them with: systemctl enable --now $BACKEND_UNIT $FRONTEND_UNIT"
  exit 0
fi

log "Enabling and starting services"
systemctl enable --now "$BACKEND_UNIT"
systemctl enable --now "$FRONTEND_UNIT"
if [ "$INSTALL_TIMERS" = true ]; then
  systemctl enable --now "$BACKUP_TIMER"
  log "Enabled $BACKUP_TIMER"
fi

HTTP_ADDR="$(env_get "$CONFIG_DIR/backend.env" TOKENHUB_HTTP_ADDR)"
BACKEND_PORT="${HTTP_ADDR##*:}"
BACKEND_PORT="${BACKEND_PORT:-8080}"
FRONTEND_PORT="$(env_get "$CONFIG_DIR/frontend.env" PORT)"
FRONTEND_PORT="${FRONTEND_PORT:-3000}"

# systemctl start returns as soon as the process is forked, which says nothing
# about the service being usable.
if ! wait_for_http "http://127.0.0.1:$BACKEND_PORT/readyz" "Backend"; then
  report_failure "$BACKEND_UNIT"
  exit 1
fi
if ! wait_for_http "http://127.0.0.1:$FRONTEND_PORT/" "Admin console"; then
  report_failure "$FRONTEND_UNIT"
  exit 1
fi

API_BASE_URL="$(env_get "$CONFIG_DIR/frontend.env" TOKENHUB_API_BASE_URL)"
cat <<EOF

TokenHub $RELEASE_VERSION is running.

  Backend API:   http://127.0.0.1:$BACKEND_PORT
  Admin console: http://127.0.0.1:$FRONTEND_PORT
  Config:        $CONFIG_DIR/backend.env, $CONFIG_DIR/frontend.env
  Data:          $STATE_DIR
  Releases:      $PREFIX_DIR/releases (current -> $(basename "$CURRENT_TARGET"))

The console calls TOKENHUB_API_BASE_URL=$API_BASE_URL from the BROWSER.
Set it to a URL your browser can reach, and list that console origin in
TOKENHUB_CORS_ALLOWED_ORIGINS, before using this from another machine.
EOF
