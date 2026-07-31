#!/usr/bin/env bash
# Run TokenHub locally from a production build, without Docker, root or systemd.
#
# Everything stays inside the repository: the binary, the console bundle, the
# SQLite database, the logs and the pid files all live under .tokenhub/, which is
# gitignored. Nothing is installed system-wide, no service user is created,
# nothing survives a reboot.
#
#   ./run-local.sh              start in the foreground, Ctrl-C stops both
#   ./run-local.sh -d           start in the background and return
#   ./run-local.sh stop         stop a background instance
#   ./run-local.sh status       report what is running
#   ./run-local.sh logs -f      follow the logs
#
# This is not the deployment path. For a real installation see the native
# systemd installer in docs/deployment.md; this script exists to run the same
# artefacts a deployment would run, on your own machine, with one command.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
BACKEND_DIR="$REPO_ROOT/backend"
FRONTEND_DIR="$REPO_ROOT/frontend"
# Lays the bundle out exactly like a release, so what runs here matches what a
# deployment would run.
# shellcheck source=standalone-bundle.sh
. "$SCRIPT_DIR/standalone-bundle.sh"

RUN_DIR="${TOKENHUB_LOCAL_DIR:-$REPO_ROOT/.tokenhub}"
BACKEND_PORT="${TOKENHUB_LOCAL_BACKEND_PORT:-8080}"
CONSOLE_PORT="${TOKENHUB_LOCAL_CONSOLE_PORT:-3000}"
STATE_DIR="$RUN_DIR/run"
LOG_DIR="$RUN_DIR/logs"
WEB_DIR="$RUN_DIR/web"
BACKEND_BIN="$RUN_DIR/bin/tokenhub"
DB_PATH="$RUN_DIR/data/tokenhub.db"
READY_TIMEOUT=90

ACTION=start
DETACH=false
REBUILD=false
RESET=false
FOLLOW=false

BACKEND_PID=""
CONSOLE_PID=""

log() { printf '[tokenhub] %s\n' "$*"; }
error() { printf '[tokenhub] ERROR: %s\n' "$*" >&2; }

usage() {
  cat <<'EOF'
Usage: ./deploy/local/run-local.sh [command] [options]

Commands:
  start (default)      Build if needed, then run the backend and console.
  stop                 Stop a background instance.
  status               Report whether the local instance is running.
  logs                 Print the logs (background instances only).
  restart              stop, then start.

Options:
  -d, --detach         Start in the background and return immediately.
  -f, --follow         For "logs": follow instead of printing and exiting.
  --rebuild            Rebuild both components even if the artefacts look current.
  --reset              Delete the local database and start from an empty one.
  --backend-port N     Backend port (default 8080, or TOKENHUB_LOCAL_BACKEND_PORT).
  --console-port N     Console port (default 3000, or TOKENHUB_LOCAL_CONSOLE_PORT).
  -h, --help           Show this help message.

All runtime state lives in .tokenhub/ inside the repository; delete that
directory to reset. Building may also refresh the usual ignored frontend
artefacts (frontend/node_modules, frontend/.next).
EOF
}

case "${1:-}" in
  start|stop|status|logs|restart) ACTION="$1"; shift ;;
esac

while [ "$#" -gt 0 ]; do
  case "$1" in
    -d|--detach) DETACH=true; shift ;;
    -f|--follow) FOLLOW=true; shift ;;
    --rebuild) REBUILD=true; shift ;;
    --reset) RESET=true; shift ;;
    --backend-port)
      [ "$#" -ge 2 ] || { error "--backend-port requires a value"; exit 2; }
      BACKEND_PORT="$2"; shift 2 ;;
    --console-port)
      [ "$#" -ge 2 ] || { error "--console-port requires a value"; exit 2; }
      CONSOLE_PORT="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) error "Unknown argument: $1"; usage >&2; exit 2 ;;
  esac
done

[ "$(id -u)" -ne 0 ] || { error "Do not run this as root; it is meant to run as your own user"; exit 1; }

# setsid puts each service in its own process group, which is what makes group
# signalling and the pgid check work. macOS does not ship it, so fall back there
# to a plain background start and to stopping the process tree by walking
# children. Determined before any subcommand runs: stop and status need the same
# answer as start, or they would apply the wrong check to a running instance.
HAVE_SETSID=true
command -v setsid >/dev/null 2>&1 || HAVE_SETSID=false

# --- process bookkeeping -----------------------------------------------------

pid_file() { printf '%s/%s.pid' "$STATE_DIR" "$1"; }
log_file() { printf '%s/%s.log' "$LOG_DIR" "$1"; }

read_pid() {
  local file
  file="$(pid_file "$1")"
  [ -f "$file" ] || return 1
  local pid
  pid="$(sed -n 1p "$file" 2>/dev/null || true)"
  case "$pid" in
    ''|*[!0-9]*) return 1 ;;
  esac
  printf '%s' "$pid"
}

# Elapsed-time-normalised start time of a running process, used to tell a
# recycled pid from the process we actually started.
process_start_time() {
  ps -p "$1" -o lstart= 2>/dev/null | sed 's/^ *//; s/ *$//'
}

# The pid file records the start time on a second line, so a recycled number can
# be recognised even when everything else about the process looks plausible.
record_pid() {
  local service="$1" pid="$2" started
  started="$(process_start_time "$pid")"
  if [ -z "$started" ]; then
    # Without a start time the pid file cannot be trusted later, and a one-line
    # file would silently downgrade the safety check. Refuse to record it.
    error "Could not read the start time of $service (pid $pid); refusing to record an unverifiable pid file"
    return 1
  fi
  printf '%s\n%s\n' "$pid" "$started" >"$(pid_file "$service")"
}

# A pid file alone is not proof: pids are recycled, and by the time we stop an
# instance that number may belong to something else entirely. Three independent
# checks have to agree:
#   1. the recorded start time still matches, which a recycled pid cannot fake;
#   2. the process leads its own process group, as everything started through
#      setsid here does;
#   3. the command line is recognisable. Next rewrites its process title to
#      "next-server (vX)", so the console cannot be matched on its script path —
#      which is exactly why check 1 matters.
pid_is_ours() {
  local pid="$1" service="$2" args pgid recorded_start current_start
  kill -0 "$pid" 2>/dev/null || return 1

  # Mandatory, not best-effort: a pid file without a start time cannot be
  # distinguished from a recycled pid, so treat it as not ours.
  recorded_start="$(sed -n 2p "$(pid_file "$service")" 2>/dev/null || true)"
  [ -n "$recorded_start" ] || return 1
  current_start="$(process_start_time "$pid")"
  [ "$current_start" = "$recorded_start" ] || return 1

  # Only meaningful when the service was started through setsid; without it the
  # start-time check above carries the safety.
  if [ "${HAVE_SETSID:-true}" = true ]; then
    pgid="$(ps -p "$pid" -o pgid= 2>/dev/null | tr -d ' ')"
    [ "$pgid" = "$pid" ] || return 1
  fi

  args="$(ps -p "$pid" -o args= 2>/dev/null || true)"
  case "$service" in
    backend) [ "${args#*"$BACKEND_BIN"}" != "$args" ] ;;
    console) [ "${args#*next-server}" != "$args" ] || [ "${args#*"$WEB_DIR"}" != "$args" ] ;;
    *) return 1 ;;
  esac
}

service_pid() {
  local service="$1" pid
  pid="$(read_pid "$service")" || return 1
  pid_is_ours "$pid" "$service" || return 1
  printf '%s' "$pid"
}

running_services() {
  local service found=""
  for service in backend console; do
    if service_pid "$service" >/dev/null; then found="$found $service"; fi
  done
  printf '%s' "${found# }"
}

# Depth-first so children die before their parent can reap or respawn them.
kill_tree() {
  local pid="$1" signal="${2:-TERM}" child
  for child in $(pgrep -P "$pid" 2>/dev/null || true); do
    kill_tree "$child" "$signal"
  done
  kill -"$signal" "$pid" 2>/dev/null || true
}

stop_service() {
  local service="$1" pid deadline
  if ! pid="$(service_pid "$service")"; then
    rm -f "$(pid_file "$service")"
    return 0
  fi
  log "Stopping $service (pid $pid)"
  # Next spawns a worker, and the backend may too, so the children have to go as
  # well: by process group where setsid gave us one, otherwise by walking the tree.
  if [ "${HAVE_SETSID:-true}" = true ]; then
    kill -TERM -- "-$pid" 2>/dev/null || kill -TERM "$pid" 2>/dev/null || true
  else
    kill_tree "$pid" TERM
  fi
  deadline=$((SECONDS + 20))
  while [ "$SECONDS" -lt "$deadline" ]; do
    kill -0 "$pid" 2>/dev/null || break
    sleep 1
  done
  if kill -0 "$pid" 2>/dev/null; then
    # The pid may have been recycled while we waited, so re-verify before
    # escalating rather than SIGKILLing whatever now holds that number.
    if pid_is_ours "$pid" "$service"; then
      error "$service did not exit within 20s; sending SIGKILL"
      if [ "${HAVE_SETSID:-true}" = true ]; then
        kill -KILL -- "-$pid" 2>/dev/null || kill -KILL "$pid" 2>/dev/null || true
      else
        kill_tree "$pid" KILL
      fi
    else
      log "$service exited; pid $pid now belongs to another process and was left alone"
    fi
  fi
  rm -f "$(pid_file "$service")"
}

stop_all() {
  # Console first: it is the one users are pointed at.
  stop_service console
  stop_service backend
}

# --- commands that do not build ----------------------------------------------

case "$ACTION" in
  stop)
    if [ -z "$(running_services)" ]; then
      log "Nothing is running"
      rm -f "$(pid_file backend)" "$(pid_file console)"
      exit 0
    fi
    stop_all
    log "Stopped"
    exit 0
    ;;
  status)
    active="$(running_services)"
    if [ -z "$active" ]; then
      log "Not running"
      exit 1
    fi
    for service in backend console; do
      if pid="$(service_pid "$service")"; then
        printf '[tokenhub] %-8s running (pid %s)\n' "$service" "$pid"
      else
        printf '[tokenhub] %-8s not running\n' "$service"
      fi
    done
    printf '[tokenhub] console  http://127.0.0.1:%s\n' "$CONSOLE_PORT"
    printf '[tokenhub] backend  http://127.0.0.1:%s\n' "$BACKEND_PORT"
    printf '[tokenhub] data     %s\n' "$DB_PATH"
    printf '[tokenhub] logs     %s\n' "$LOG_DIR"
    exit 0
    ;;
  logs)
    shopt -s nullglob
    files=("$LOG_DIR"/*.log)
    shopt -u nullglob
    if [ "${#files[@]}" -eq 0 ]; then
      error "No logs in $LOG_DIR (a foreground instance writes to the terminal instead)"
      exit 1
    fi
    if [ "$FOLLOW" = true ]; then
      tail -n 50 -f "${files[@]}"
    else
      tail -n 200 "${files[@]}"
    fi
    exit 0
    ;;
  restart)
    stop_all
    ACTION=start
    ;;
esac

# --- start -------------------------------------------------------------------

# mkdir is atomic, so two launchers racing here cannot both proceed to build and
# start competing processes over the same pid files.
START_LOCK="$STATE_DIR/start.lock"
mkdir -p "$STATE_DIR"
if ! mkdir "$START_LOCK" 2>/dev/null; then
  error "Another start is already in progress ($START_LOCK exists)."
  error "If no launcher is running, remove that directory and retry."
  exit 1
fi
release_start_lock() { rmdir "$START_LOCK" 2>/dev/null || true; }
trap release_start_lock EXIT

active="$(running_services)"
if [ -n "$active" ]; then
  error "Already running ($active). Use \"$0 stop\" or \"$0 restart\"."
  exit 1
fi
# Clear pid files left behind by a crash, so they do not confuse the next status.
rm -f "$(pid_file backend)" "$(pid_file console)"

for tool in go node npm; do
  command -v "$tool" >/dev/null 2>&1 || { error "$tool is required but was not found"; exit 1; }
done
NODE_MAJOR="$(node -p 'process.versions.node.split(".")[0]')"
[ "$NODE_MAJOR" -ge 22 ] || { error "Node 22 or newer is required (found $(node --version))"; exit 1; }

mkdir -p "$RUN_DIR/bin" "$RUN_DIR/data" "$STATE_DIR" "$LOG_DIR"

# An HTTP probe cannot tell our service from anything else already answering on
# that port, so a readiness check alone would report success against a stranger.
# Claim the ports up front instead: if binding fails now, refuse to start.
port_available() {
  node -e "
    const net = require('net');
    const server = net.createServer();
    server.once('error', (e) => process.exit(e.code === 'EADDRINUSE' ? 1 : 0));
    server.once('listening', () => server.close(() => process.exit(0)));
    server.listen(Number(process.argv[1]), '127.0.0.1');
  " "$1" >/dev/null 2>&1
}

for probe in "backend:$BACKEND_PORT" "console:$CONSOLE_PORT"; do
  if ! port_available "${probe#*:}"; then
    error "Port ${probe#*:} (${probe%%:*}) is already in use."
    error "Stop whatever is listening, or pass --${probe%%:*}-port with a free port."
    exit 1
  fi
done

needs_backend_build() {
  [ "$REBUILD" = true ] && return 0
  [ -x "$BACKEND_BIN" ] || return 0
  # Dependency changes alter the binary just as much as source changes do.
  [ "$BACKEND_DIR/go.mod" -nt "$BACKEND_BIN" ] && return 0
  [ "$BACKEND_DIR/go.sum" -nt "$BACKEND_BIN" ] && return 0
  [ -n "$(find "$BACKEND_DIR" -name '*.go' -newer "$BACKEND_BIN" -print -quit 2>/dev/null)" ]
}

if needs_backend_build; then
  log "Building backend"
  # cgo is required: the SQLite driver fails at runtime without it.
  (cd "$BACKEND_DIR" && CGO_ENABLED=1 go build -o "$BACKEND_BIN" ./cmd/tokenhub)
else
  log "Backend binary is current"
fi

if [ ! -d "$FRONTEND_DIR/node_modules" ]; then
  log "Installing frontend dependencies"
  (cd "$FRONTEND_DIR" && npm install)
fi

needs_console_build() {
  [ "$REBUILD" = true ] && return 0
  local build_id="$FRONTEND_DIR/.next/BUILD_ID"
  [ -f "$build_id" ] || return 0
  local input
  for input in package.json package-lock.json next.config.ts tsconfig.json; do
    [ -e "$FRONTEND_DIR/$input" ] && [ "$FRONTEND_DIR/$input" -nt "$build_id" ] && return 0
  done
  # Every source directory, not a hardcoded three: a new top-level directory would
  # otherwise never trigger a rebuild.
  [ -n "$(find "$FRONTEND_DIR" -mindepth 1 -maxdepth 1 -type d \
    ! -name node_modules ! -name .next \
    -exec find {} -newer "$build_id" -print -quit \; 2>/dev/null)" ]
}

if needs_console_build; then
  log "Building console (production build, not a dev server)"
  (cd "$FRONTEND_DIR" && npm run build)
  rm -rf "$WEB_DIR"
else
  log "Console build is current"
fi

# Also reassemble when .next was rebuilt by something else (npm run build),
# otherwise an older bundle would be served while looking current.
if [ ! -f "$WEB_DIR/server.js" ] || [ "$FRONTEND_DIR/.next/BUILD_ID" -nt "$WEB_DIR/server.js" ]; then
  log "Assembling console bundle"
  assemble_standalone_bundle "$FRONTEND_DIR" "$WEB_DIR" || exit 1
fi

if [ "$RESET" = true ] && [ -f "$DB_PATH" ]; then
  log "Removing the existing local database"
  rm -f "$DB_PATH" "$DB_PATH-wal" "$DB_PATH-shm" "$DB_PATH-journal"
fi

# TOKENHUB_ENV=dev on purpose: it tells the backend this is not a production
# deployment, so it accepts the throwaway credentials below instead of demanding
# 32-byte secrets. Never point this script at real data.
BACKEND_ENV=(
  TOKENHUB_ENV=dev
  TOKENHUB_HTTP_ADDR="127.0.0.1:$BACKEND_PORT"
  TOKENHUB_PUBLIC_BASE_URL="http://127.0.0.1:$BACKEND_PORT"
  TOKENHUB_CORS_ALLOWED_ORIGINS="http://127.0.0.1:$CONSOLE_PORT,http://localhost:$CONSOLE_PORT"
  TOKENHUB_DATABASE_URL="sqlite://$DB_PATH"
  TOKENHUB_SQLITE_BACKUP_DIR="$RUN_DIR/data/backups"
  TOKENHUB_MODEL_CATALOG_FILE="$REPO_ROOT/data/model-catalog.yaml"
  TOKENHUB_PROVIDER_CATALOG_FILE="$REPO_ROOT/data/provider-catalog.json"
  TOKENHUB_ADMIN_TOKEN=local_dev_admin_token
  TOKENHUB_SECRET_KEY=local_dev_secret_key
  TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD=admin123456
  TOKENHUB_LOG_LEVEL="${TOKENHUB_LOG_LEVEL:-info}"
)

CONSOLE_ENV=(
  NODE_ENV=production
  TOKENHUB_API_BASE_URL="http://127.0.0.1:$BACKEND_PORT"
  HOSTNAME=127.0.0.1
  PORT="$CONSOLE_PORT"
)

# Armed before anything is launched: a signal (or an errexit) between starting the
# backend and finishing startup would otherwise leave a detached process behind
# with no pid file to find it by.
STARTUP_COMPLETE=false
abort_startup() {
  local status=$?
  trap - INT TERM EXIT
  if [ "$STARTUP_COMPLETE" = false ]; then
    error "Startup interrupted; stopping anything already started"
    stop_all
  fi
  release_start_lock
  exit "$status"
}
trap abort_startup INT TERM EXIT

# Detached services write to log files; a foreground run inherits the terminal so
# the output is visible as it happens.
log "Starting backend on 127.0.0.1:$BACKEND_PORT"
if [ "$DETACH" = true ]; then
  if [ "$HAVE_SETSID" = true ]; then
    setsid env "${BACKEND_ENV[@]}" "$BACKEND_BIN" >>"$(log_file backend)" 2>&1 </dev/null &
  else
    env "${BACKEND_ENV[@]}" "$BACKEND_BIN" >>"$(log_file backend)" 2>&1 </dev/null &
  fi
else
  if [ "$HAVE_SETSID" = true ]; then
    setsid env "${BACKEND_ENV[@]}" "$BACKEND_BIN" &
  else
    env "${BACKEND_ENV[@]}" "$BACKEND_BIN" &
  fi
fi
BACKEND_PID=$!
record_pid backend "$BACKEND_PID" || { kill "$BACKEND_PID" 2>/dev/null; exit 1; }

# Liveness is checked BEFORE the HTTP probe: if our own process is already gone,
# a successful response just means something else owns that port, and reporting
# "ready" would be a lie. The probe also demands a 2xx/3xx rather than merely
# "not a server error", so a 404 from an unrelated service does not count.
wait_for_http() {
  local url="$1" label="$2" pid="$3" deadline=$((SECONDS + READY_TIMEOUT))
  while [ "$SECONDS" -lt "$deadline" ]; do
    if ! kill -0 "$pid" 2>/dev/null; then
      error "$label exited during startup"
      return 1
    fi
    if node -e "fetch(process.argv[1],{redirect:'manual'}).then(r=>process.exit(r.status<400?0:1)).catch(()=>process.exit(1))" "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  error "$label did not become ready within ${READY_TIMEOUT}s"
  return 1
}

startup_failed() {
  error "$1"
  [ "$DETACH" = true ] && error "See $LOG_DIR for details."
  # The abort_startup trap performs the cleanup.
  exit 1
}

wait_for_http "http://127.0.0.1:$BACKEND_PORT/readyz" "Backend" "$BACKEND_PID" \
  || startup_failed "Backend did not start"

log "Starting console on 127.0.0.1:$CONSOLE_PORT"
# node server.js, not `next start`: Next refuses to serve an output: "standalone"
# build through `next start`. This is the same entrypoint the systemd unit uses.
# Always to a file, in both modes: Next's startup output is noisy enough that
# interleaving it with the backend's would make a foreground run hard to read.
if [ "$HAVE_SETSID" = true ]; then
  setsid env "${CONSOLE_ENV[@]}" node "$WEB_DIR/server.js" >>"$(log_file console)" 2>&1 </dev/null &
else
  env "${CONSOLE_ENV[@]}" node "$WEB_DIR/server.js" >>"$(log_file console)" 2>&1 </dev/null &
fi
CONSOLE_PID=$!
record_pid console "$CONSOLE_PID" || { kill "$CONSOLE_PID" 2>/dev/null; exit 1; }

wait_for_http "http://127.0.0.1:$CONSOLE_PORT/" "Console" "$CONSOLE_PID" \
  || startup_failed "Console did not start"

print_summary() {
  cat <<EOF

TokenHub is running locally.

  Console:  http://127.0.0.1:$CONSOLE_PORT
  Backend:  http://127.0.0.1:$BACKEND_PORT
  Data:     $DB_PATH

  Sign in with  admin / admin123456

Development credentials, loopback only.
EOF
}

if [ "$DETACH" = true ]; then
  print_summary
  cat <<EOF
Running in the background.

  $0 status
  $0 logs -f
  $0 stop
EOF
  # The services are meant to outlive this process, so disarm the cleanup first.
  STARTUP_COMPLETE=true
  trap - INT TERM EXIT
  release_start_lock
  exit 0
fi

print_summary
printf 'Console logs: %s\nPress Ctrl-C to stop both.\n\n' "$(log_file console)"

# Foreground only: clean up on the way out.
STARTUP_COMPLETE=true
cleanup() {
  local status=$?
  trap - INT TERM EXIT
  stop_all
  release_start_lock
  exit "$status"
}
trap cleanup INT TERM EXIT

# Exit as soon as either side does, so a crashed backend does not leave a console
# running against nothing.
while true; do
  if ! kill -0 "$BACKEND_PID" 2>/dev/null; then
    error "Backend exited"
    exit 1
  fi
  if ! kill -0 "$CONSOLE_PID" 2>/dev/null; then
    error "Console exited"
    exit 1
  fi
  sleep 1
done
