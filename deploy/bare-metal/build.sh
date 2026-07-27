#!/usr/bin/env bash
# Build a transferable TokenHub release payload for bare-metal (systemd) deployment.
#
# The payload contains everything install.sh needs on the target host: the backend
# binary, the Next.js standalone bundle, both catalogs, unit files, configuration
# examples and the installer itself. The target host needs a Node 22 runtime but
# neither a Go toolchain nor a repository checkout.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
# shellcheck source=standalone-bundle.sh
. "$SCRIPT_DIR/standalone-bundle.sh"
BACKEND_DIR="$REPO_ROOT/backend"
FRONTEND_DIR="$REPO_ROOT/frontend"
OUTPUT_DIR="${OUTPUT_DIR:-$REPO_ROOT/dist}"
SKIP_TARBALL=false

log() {
  printf '[build] %s\n' "$*"
}

error() {
  printf '[build] ERROR: %s\n' "$*" >&2
}

usage() {
  cat <<'EOF'
Usage: ./deploy/bare-metal/build.sh [--output DIR] [--version VERSION] [--skip-tarball]

Options:
  --output DIR       Directory to write the release into (default: <repo>/dist).
  --version VERSION  Override the release version (default: frontend package.json
                     version plus the short git commit).
  --skip-tarball     Leave the release directory in place without creating a
                     .tar.gz archive.
  -h, --help         Show this help message.
EOF
}

VERSION_OVERRIDE=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --output)
      [ "$#" -ge 2 ] || { error "--output requires a path"; exit 2; }
      OUTPUT_DIR="$2"
      shift 2
      ;;
    --version)
      [ "$#" -ge 2 ] || { error "--version requires a value"; exit 2; }
      VERSION_OVERRIDE="$2"
      shift 2
      ;;
    --skip-tarball)
      SKIP_TARBALL=true
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      error "Unknown argument: $1"
      usage >&2
      exit 2
      ;;
  esac
done

require_command() {
  command -v "$1" >/dev/null 2>&1 || { error "Required command not found: $1"; exit 1; }
}

# Compares dotted versions without relying on sort -V, which is absent on some
# minimal images. Returns 0 when $1 >= $2.
version_at_least() {
  local have="$1" want="$2" i have_part want_part
  local -a have_parts want_parts
  IFS='.' read -r -a have_parts <<<"$have"
  IFS='.' read -r -a want_parts <<<"$want"
  for i in 0 1 2; do
    have_part="${have_parts[$i]:-0}"
    want_part="${want_parts[$i]:-0}"
    have_part="${have_part%%[!0-9]*}"
    want_part="${want_part%%[!0-9]*}"
    have_part="${have_part:-0}"
    want_part="${want_part:-0}"
    if [ "$have_part" -gt "$want_part" ]; then
      return 0
    fi
    if [ "$have_part" -lt "$want_part" ]; then
      return 1
    fi
  done
  return 0
}

# The release must stay readable by the two service users on the target host; a
# restrictive build-host umask would otherwise produce a root-only payload.
umask 022

log "Checking build prerequisites"
require_command go
require_command node
require_command npm
require_command tar
# The installer and the runtime scripts rely on GNU behaviour that BSD/macOS
# userland does not provide. Fail here rather than midway through an install.
require_command sha256sum
require_command install
for gnu_check in "find . -maxdepth 0 -printf %p" "mktemp -u --suffix=.probe tokenhub.XXXXXX"; do
  if ! $gnu_check >/dev/null 2>&1; then
    error "This build host lacks GNU coreutils/findutils behaviour required by the deployment scripts (failed: $gnu_check)"
    error "Build on Linux, or install GNU userland."
    exit 1
  fi
done

GO_VERSION="$(go env GOVERSION 2>/dev/null || true)"
GO_VERSION="${GO_VERSION#go}"
GO_REQUIRED="$(awk '/^go [0-9]/ { print $2; exit }' "$BACKEND_DIR/go.mod")"
if [ -n "$GO_VERSION" ] && ! version_at_least "$GO_VERSION" "$GO_REQUIRED"; then
  # Go 1.21+ downloads and switches to the toolchain go.mod asks for, so an older
  # local go is only fatal when that mechanism is disabled.
  if [ "$(go env GOTOOLCHAIN 2>/dev/null || echo auto)" = "local" ]; then
    error "backend/go.mod needs Go $GO_REQUIRED, this host has $GO_VERSION, and GOTOOLCHAIN=local disables the automatic switch"
    exit 1
  fi
  log "Local Go is $GO_VERSION; go will fetch the toolchain required by go.mod ($GO_REQUIRED)"
fi

NODE_VERSION="$(node --version)"
NODE_VERSION="${NODE_VERSION#v}"
if ! version_at_least "$NODE_VERSION" "22.0.0"; then
  error "Node 22 or newer is required (found $NODE_VERSION)"
  exit 1
fi

# The SQLite driver (mattn/go-sqlite3) is imported unconditionally, so a C
# toolchain is required even when the deployment targets PostgreSQL: a
# CGO_ENABLED=0 binary compiles but fails at runtime as soon as SQLite is used.
CC_BIN="${CC:-}"
if [ -z "$CC_BIN" ]; then
  for candidate in cc gcc clang; do
    if command -v "$candidate" >/dev/null 2>&1; then
      CC_BIN="$candidate"
      break
    fi
  done
fi
if [ -z "$CC_BIN" ]; then
  error "A C compiler is required to build the SQLite driver (install gcc or clang)"
  exit 1
fi

if [ -n "$VERSION_OVERRIDE" ]; then
  VERSION="$VERSION_OVERRIDE"
else
  PKG_VERSION="$(node -p "require('$FRONTEND_DIR/package.json').version")"
  GIT_SHORT="$(git -C "$REPO_ROOT" rev-parse --short HEAD 2>/dev/null || echo unknown)"
  VERSION="${PKG_VERSION}+${GIT_SHORT}"
fi

GIT_COMMIT="$(git -C "$REPO_ROOT" rev-parse HEAD 2>/dev/null || echo unknown)"
TARGET_OS="$(go env GOOS)"
TARGET_ARCH="$(go env GOARCH)"
RELEASE_NAME="tokenhub-${VERSION}-${TARGET_OS}-${TARGET_ARCH}"
RELEASE_DIR="$OUTPUT_DIR/$RELEASE_NAME"

if [ "$TARGET_OS" != "linux" ]; then
  log "WARNING: building for $TARGET_OS; the systemd installer supports Linux only"
fi

log "Building release $RELEASE_NAME"
rm -rf "$RELEASE_DIR"
mkdir -p "$RELEASE_DIR/bin" "$RELEASE_DIR/catalog" "$RELEASE_DIR/systemd" "$RELEASE_DIR/scripts"

log "Building backend binary (CGO_ENABLED=1, CC=$CC_BIN)"
(
  cd "$BACKEND_DIR"
  CGO_ENABLED=1 CC="$CC_BIN" go build -trimpath -o "$RELEASE_DIR/bin/tokenhub" ./cmd/tokenhub
)

log "Building frontend (npm ci && npm run build)"
(
  cd "$FRONTEND_DIR"
  npm ci
  rm -rf .next
  npm run build
)

log "Assembling frontend bundle"
if ! assemble_standalone_bundle "$FRONTEND_DIR" "$RELEASE_DIR/web"; then
  error "Could not assemble the console bundle"
  exit 1
fi

log "Copying catalogs, units, scripts and configuration examples"
cp "$REPO_ROOT/data/model-catalog.yaml" "$RELEASE_DIR/catalog/model-catalog.yaml"
cp "$REPO_ROOT/data/provider-catalog.json" "$RELEASE_DIR/catalog/provider-catalog.json"
cp "$SCRIPT_DIR"/systemd/*.service "$SCRIPT_DIR"/systemd/*.timer "$RELEASE_DIR/systemd/"
cp "$SCRIPT_DIR/backend.env.example" "$SCRIPT_DIR/frontend.env.example" "$RELEASE_DIR/"
cp "$SCRIPT_DIR/install.sh" "$SCRIPT_DIR/uninstall.sh" "$RELEASE_DIR/"
cp "$SCRIPT_DIR/backup-sqlite.mjs" "$SCRIPT_DIR/parse-env-file.mjs" "$RELEASE_DIR/scripts/"
chmod +x "$RELEASE_DIR/install.sh" "$RELEASE_DIR/uninstall.sh"

LIBC_INFO="unknown"
if command -v ldd >/dev/null 2>&1; then
  LIBC_INFO="$(ldd --version 2>&1 | head -n 1 || true)"
fi

cat >"$RELEASE_DIR/VERSION" <<EOF
version=$VERSION
commit=$GIT_COMMIT
os=$TARGET_OS
arch=$TARGET_ARCH
go=$GO_VERSION
node=$NODE_VERSION
libc=$LIBC_INFO
EOF

# The backend links libsqlite3 through cgo, so the binary is tied to this
# architecture and libc. Record it: the operator has to match the target host.
log "Recording checksums"
(
  cd "$RELEASE_DIR"
  find . -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 sha256sum >SHA256SUMS
)

if [ "$SKIP_TARBALL" = false ]; then
  log "Creating archive"
  (
    cd "$OUTPUT_DIR"
    tar -czf "${RELEASE_NAME}.tar.gz" "$RELEASE_NAME"
  )
fi

log "Release ready: $RELEASE_DIR"
if [ "$SKIP_TARBALL" = false ]; then
  log "Archive:       $OUTPUT_DIR/${RELEASE_NAME}.tar.gz"
fi
cat <<EOF

Next steps on the target host (Linux with systemd, Node >= 22 installed):

  sudo ./install.sh --generate-secrets

Then review /etc/tokenhub/backend.env and start the services as instructed.
EOF
