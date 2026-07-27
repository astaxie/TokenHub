#!/usr/bin/env bash
# Remove the bare-metal TokenHub installation.
#
# Configuration and data are kept by default: an uninstall is usually a step in
# migrating or reinstalling, and /var/lib/tokenhub holds the only copy of a
# SQLite deployment's database.
set -euo pipefail

PREFIX_DIR=/opt/tokenhub
CONFIG_DIR=/etc/tokenhub
STATE_DIR=/var/lib/tokenhub
UNIT_DIR=/etc/systemd/system
BACKEND_USER=tokenhub
FRONTEND_USER=tokenhub-web
UNITS=(
  tokenhub-backend.service
  tokenhub-frontend.service
  tokenhub-backup.service
  tokenhub-backup.timer
)

PURGE=false
REMOVE_USERS=false

log() { printf '[tokenhub] %s\n' "$*"; }
error() { printf '[tokenhub] ERROR: %s\n' "$*" >&2; }

usage() {
  cat <<'EOF'
Usage: sudo ./uninstall.sh [--purge] [--remove-users]

Options:
  --purge         Also delete /etc/tokenhub and /var/lib/tokenhub, including the
                  SQLite database and every local backup. Irreversible.
  --remove-users  Also delete the tokenhub and tokenhub-web system accounts.
  -h, --help      Show this help message.
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --purge) PURGE=true; shift ;;
    --remove-users) REMOVE_USERS=true; shift ;;
    -h|--help) usage; exit 0 ;;
    *) error "Unknown argument: $1"; usage >&2; exit 2 ;;
  esac
done

[ "$(id -u)" -eq 0 ] || { error "This script must run as root"; exit 1; }

for unit in "${UNITS[@]}"; do
  if systemctl list-unit-files "$unit" >/dev/null 2>&1 && [ -e "$UNIT_DIR/$unit" ]; then
    log "Stopping and disabling $unit"
    systemctl disable --now "$unit" >/dev/null 2>&1 || true
    rm -f "$UNIT_DIR/$unit"
  fi
done
rm -rf "$UNIT_DIR/tokenhub-frontend.service.d"
systemctl daemon-reload
systemctl reset-failed >/dev/null 2>&1 || true

if [ -d "$PREFIX_DIR" ]; then
  log "Removing $PREFIX_DIR"
  rm -rf "$PREFIX_DIR"
fi

if [ "$PURGE" = true ]; then
  log "Purging $CONFIG_DIR and $STATE_DIR"
  rm -rf "$CONFIG_DIR" "$STATE_DIR"
else
  log "Kept $CONFIG_DIR and $STATE_DIR (use --purge to delete them)"
  if [ -d "$STATE_DIR" ]; then
    log "The database and its backups are still in $STATE_DIR"
  fi
fi

if [ "$REMOVE_USERS" = true ]; then
  for user in "$FRONTEND_USER" "$BACKEND_USER"; do
    if getent passwd "$user" >/dev/null 2>&1; then
      log "Removing user $user"
      userdel "$user" >/dev/null 2>&1 || true
    fi
    if getent group "$user" >/dev/null 2>&1; then
      groupdel "$user" >/dev/null 2>&1 || true
    fi
  done
fi

log "Uninstall complete"
