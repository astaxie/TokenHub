#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTALL_SCRIPT="$SCRIPT_DIR/install.sh"
TEST_DIR="$(mktemp -d)"
FAKE_DOCKER="$TEST_DIR/docker"
ENV_FILE="$TEST_DIR/deploy.env"
MODEL_CATALOG_FILE="$TEST_DIR/model-catalog.yaml"
CALL_LOG="$TEST_DIR/calls.log"

cleanup() {
  rm -rf "$TEST_DIR"
}
trap cleanup EXIT

printf 'TOKENHUB_ENV=prod\n' >"$ENV_FILE"
printf 'models: []\n' >"$MODEL_CATALOG_FILE"

cat >"$FAKE_DOCKER" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

printf '%s\n' "$*" >>"$FAKE_CALL_LOG"
up_has_run=false
if grep -q 'up -d --remove-orphans --no-build --pull never --wait --wait-timeout 180' "$FAKE_CALL_LOG"; then
  up_has_run=true
fi

case "$*" in
  *" version")
    printf 'Docker Compose version test\n'
    ;;
  *" config --quiet")
    exit "${FAKE_CONFIG_STATUS:-0}"
    ;;
  *" config --environment")
    printf '%s\n' "$FAKE_COMPOSE_ENVIRONMENT"
    ;;
  *" pull")
    exit "${FAKE_PULL_STATUS:-0}"
    ;;
  *" build")
    exit "${FAKE_BUILD_STATUS:-0}"
    ;;
  *" ps -a -q tokenhub-backend")
    if [ "$up_has_run" = true ]; then
      printf '%s\n' "${FAKE_BACKEND_ID_AFTER:-}"
    else
      printf '%s\n' "${FAKE_BACKEND_ID_BEFORE:-}"
    fi
    ;;
  *" up -d --remove-orphans --no-build --pull never --wait --wait-timeout 180")
    printf 'Container tokenhub-backend Error\n' >&2
    exit "${FAKE_UP_STATUS:-0}"
    ;;
  "inspect --format "*)
    if [[ "$*" == *"{{.State.Status}}"* ]]; then
      printf '%s|%s|%s\n' \
        "${FAKE_BACKEND_STATE:-running}" \
        "${FAKE_BACKEND_HEALTH:-healthy}" \
        "${FAKE_BACKEND_STARTED_AT:-2026-07-22T00:00:00Z}"
    else
      printf '%s\n' "${FAKE_BACKEND_STARTED_AT:-2026-07-22T00:00:00Z}"
    fi
    ;;
  *" logs --no-color --tail=100 --since "*" tokenhub-backend")
    printf '%s\n' "${FAKE_BACKEND_LOG:-backend log unavailable}"
    ;;
  *" ps")
    printf 'tokenhub-backend running\n'
    ;;
  *)
    printf 'unexpected fake Docker invocation: %s\n' "$*" >&2
    exit 99
    ;;
esac
EOF
chmod +x "$FAKE_DOCKER"

assert_contains() {
  local haystack="$1"
  local needle="$2"
  if [[ "$haystack" != *"$needle"* ]]; then
    printf 'expected output to contain %q, got:\n%s\n' "$needle" "$haystack" >&2
    exit 1
  fi
}

assert_not_contains() {
  local haystack="$1"
  local needle="$2"
  if [[ "$haystack" == *"$needle"* ]]; then
    printf 'expected output not to contain %q, got:\n%s\n' "$needle" "$haystack" >&2
    exit 1
  fi
}

run_install() {
  DOCKER_BIN="$FAKE_DOCKER" \
    FAKE_CALL_LOG="$CALL_LOG" \
    FAKE_COMPOSE_ENVIRONMENT="$FAKE_COMPOSE_ENVIRONMENT" \
    FAKE_PULL_STATUS="${FAKE_PULL_STATUS:-0}" \
    FAKE_BUILD_STATUS="${FAKE_BUILD_STATUS:-0}" \
    FAKE_UP_STATUS="${FAKE_UP_STATUS:-0}" \
    FAKE_BACKEND_LOG="${FAKE_BACKEND_LOG:-}" \
    FAKE_BACKEND_ID_BEFORE="${FAKE_BACKEND_ID_BEFORE:-}" \
    FAKE_BACKEND_ID_AFTER="${FAKE_BACKEND_ID_AFTER:-}" \
    FAKE_BACKEND_STATE="${FAKE_BACKEND_STATE:-running}" \
    FAKE_BACKEND_HEALTH="${FAKE_BACKEND_HEALTH:-healthy}" \
    FAKE_BACKEND_STARTED_AT="${FAKE_BACKEND_STARTED_AT:-2026-07-22T00:00:00Z}" \
    "$INSTALL_SCRIPT" --env-file "$ENV_FILE" "$@"
}

weak_password_environment=$(cat <<'EOF'
TOKENHUB_ENV=prod
TOKENHUB_ADMIN_TOKEN=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
TOKENHUB_SECRET_KEY=ssssssssssssssssssssssssssssssss
TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD=short
EOF
)

: >"$CALL_LOG"
FAKE_COMPOSE_ENVIRONMENT="$weak_password_environment"
set +e
output="$(run_install --check-only 2>&1)"
status=$?
set -e
if [ "$status" -ne 1 ]; then
  printf 'expected weak configuration to exit 1, got %d\n' "$status" >&2
  exit 1
fi
assert_contains "$output" "TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD must be at least 12 bytes"
assert_not_contains "$output" "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
assert_not_contains "$output" "ssssssssssssssssssssssssssssssss"
assert_not_contains "$output" "short"
assert_not_contains "$(<"$CALL_LOG")" " pull"
assert_not_contains "$(<"$CALL_LOG")" " build"
assert_not_contains "$(<"$CALL_LOG")" "up -d --remove-orphans --no-build --pull never --wait --wait-timeout 180"

unicode_whitespace=$'\302\205\302\240\341\232\200\342\200\200\342\200\201\342\200\202\342\200\203\342\200\204\342\200\205\342\200\206\342\200\207\342\200\210\342\200\211\342\200\212\342\200\250\342\200\251\342\200\257\342\201\237\343\200\200'
unicode_password="${unicode_whitespace}aaaaaaaaaaa${unicode_whitespace}"
unicode_whitespace_environment="$(printf 'TOKENHUB_ENV=prod\nTOKENHUB_ADMIN_TOKEN=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\nTOKENHUB_SECRET_KEY=ssssssssssssssssssssssssssssssss\nTOKENHUB_BOOTSTRAP_ADMIN_PASSWORD=%s\n' "$unicode_password")"

: >"$CALL_LOG"
FAKE_COMPOSE_ENVIRONMENT="$unicode_whitespace_environment"
set +e
output="$(LC_ALL=C run_install --check-only 2>&1)"
status=$?
set -e
if [ "$status" -ne 1 ]; then
  printf 'expected Unicode-padded weak password to exit 1, got %d\n' "$status" >&2
  exit 1
fi
assert_contains "$output" "TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD must be at least 12 bytes"
assert_not_contains "$output" "$unicode_password"
assert_not_contains "$(<"$CALL_LOG")" " pull"
assert_not_contains "$(<"$CALL_LOG")" " build"
assert_not_contains "$(<"$CALL_LOG")" "up -d --remove-orphans --no-build --pull never --wait --wait-timeout 180"

strong_environment=$(cat <<'EOF'
TOKENHUB_ENV=prod
TOKENHUB_ADMIN_TOKEN=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
TOKENHUB_SECRET_KEY=ssssssssssssssssssssssssssssssss
TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD=strong-admin-password
EOF
)

: >"$CALL_LOG"
FAKE_COMPOSE_ENVIRONMENT="$strong_environment"
output="$(run_install --check-only 2>&1)"
assert_contains "$output" "deployment configuration is valid for prod"
assert_not_contains "$(<"$CALL_LOG")" " pull"
assert_not_contains "$(<"$CALL_LOG")" " build"
assert_not_contains "$(<"$CALL_LOG")" "up -d --remove-orphans --no-build --pull never --wait --wait-timeout 180"

: >"$CALL_LOG"
FAKE_COMPOSE_ENVIRONMENT="$strong_environment"
output="$(run_install --check-only --model-catalog "$MODEL_CATALOG_FILE" 2>&1)"
assert_contains "$output" "deployment configuration is valid for prod"
assert_contains "$(<"$CALL_LOG")" "docker-compose.model-catalog.yml"

: >"$CALL_LOG"
FAKE_COMPOSE_ENVIRONMENT="$strong_environment"
set +e
output="$(run_install --check-only --model-catalog "$TEST_DIR/missing-catalog.yaml" 2>&1)"
status=$?
set -e
if [ "$status" -ne 1 ]; then
  printf 'expected missing model catalog to exit 1, got %d\n' "$status" >&2
  exit 1
fi
assert_contains "$output" "model catalog file not found"

: >"$CALL_LOG"
FAKE_COMPOSE_ENVIRONMENT="$strong_environment"
FAKE_PULL_STATUS=23
output="$(run_install 2>&1)"
assert_contains "$output" "failed to pull published TokenHub images"
assert_contains "$output" "falling back to a local source build"
assert_contains "$(<"$CALL_LOG")" " build"
assert_contains "$(<"$CALL_LOG")" "up -d --remove-orphans --no-build --pull never --wait --wait-timeout 180"
FAKE_PULL_STATUS=0

: >"$CALL_LOG"
fixed_tag_environment="${strong_environment}"$'\nTOKENHUB_IMAGE_TAG=1.2.3'
FAKE_COMPOSE_ENVIRONMENT="$fixed_tag_environment"
FAKE_PULL_STATUS=23
set +e
output="$(run_install 2>&1)"
status=$?
set -e
if [ "$status" -ne 23 ]; then
  printf 'expected fixed-tag pull failure to exit 23, got %d\n' "$status" >&2
  exit 1
fi
assert_contains "$output" "TOKENHUB_IMAGE_TAG=1.2.3 was explicitly selected"
assert_contains "$output" "refusing to replace it with a local source build"
assert_not_contains "$(<"$CALL_LOG")" " build"
assert_not_contains "$(<"$CALL_LOG")" "up -d --remove-orphans --no-build --pull never --wait --wait-timeout 180"
FAKE_PULL_STATUS=0

: >"$CALL_LOG"
FAKE_COMPOSE_ENVIRONMENT="$strong_environment"
FAKE_BUILD_STATUS=24
set +e
output="$(run_install --build 2>&1)"
status=$?
set -e
if [ "$status" -ne 24 ]; then
  printf 'expected build failure to exit 24, got %d\n' "$status" >&2
  exit 1
fi
assert_contains "$output" "failed to build TokenHub images"
assert_not_contains "$(<"$CALL_LOG")" " pull"
assert_not_contains "$(<"$CALL_LOG")" "up -d --remove-orphans --no-build --pull never --wait --wait-timeout 180"
FAKE_BUILD_STATUS=0

: >"$CALL_LOG"
FAKE_COMPOSE_ENVIRONMENT="$strong_environment"
FAKE_PULL_STATUS=23
FAKE_BUILD_STATUS=25
set +e
output="$(run_install 2>&1)"
status=$?
set -e
if [ "$status" -ne 25 ]; then
  printf 'expected pull fallback build failure to exit 25, got %d\n' "$status" >&2
  exit 1
fi
assert_contains "$output" "failed to pull published TokenHub images"
assert_contains "$output" "also failed to build TokenHub images locally"
assert_not_contains "$(<"$CALL_LOG")" "up -d --remove-orphans --no-build --pull never --wait --wait-timeout 180"
FAKE_PULL_STATUS=0
FAKE_BUILD_STATUS=0

: >"$CALL_LOG"
FAKE_COMPOSE_ENVIRONMENT="$strong_environment"
FAKE_UP_STATUS=17
FAKE_BACKEND_LOG="unsafe prod configuration: TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD must be at least 12 bytes"
FAKE_BACKEND_ID_BEFORE=""
FAKE_BACKEND_ID_AFTER="backend-failed"
FAKE_BACKEND_STATE="restarting"
FAKE_BACKEND_HEALTH=""
FAKE_BACKEND_STARTED_AT="2026-07-22T00:00:01Z"
set +e
output="$(run_install 2>&1)"
status=$?
set -e
if [ "$status" -ne 17 ]; then
  printf 'expected Compose failure status 17, got %d\n' "$status" >&2
  exit 1
fi
assert_contains "$output" "tokenhub-backend logs from this startup attempt"
assert_contains "$output" "$FAKE_BACKEND_LOG"
assert_contains "$(<"$CALL_LOG")" "logs --no-color --tail=100 --since"
assert_contains "$(<"$CALL_LOG")" "tokenhub-backend"

: >"$CALL_LOG"
FAKE_COMPOSE_ENVIRONMENT="$strong_environment"
FAKE_UP_STATUS=124
FAKE_BACKEND_LOG="database connection is still unavailable"
FAKE_BACKEND_ID_BEFORE=""
FAKE_BACKEND_ID_AFTER="backend-starting"
FAKE_BACKEND_STATE="running"
FAKE_BACKEND_HEALTH="starting"
FAKE_BACKEND_STARTED_AT="2026-07-22T00:00:01Z"
set +e
output="$(run_install 2>&1)"
status=$?
set -e
if [ "$status" -ne 124 ]; then
  printf 'expected readiness timeout status 124, got %d\n' "$status" >&2
  exit 1
fi
assert_contains "$output" "tokenhub-backend logs from this startup attempt"
assert_contains "$output" "$FAKE_BACKEND_LOG"
assert_contains "$(<"$CALL_LOG")" "logs --no-color --tail=100 --since"

: >"$CALL_LOG"
FAKE_COMPOSE_ENVIRONMENT="$strong_environment"
FAKE_UP_STATUS=18
FAKE_BACKEND_LOG="old healthy backend request log"
FAKE_BACKEND_ID_BEFORE="backend-existing"
FAKE_BACKEND_ID_AFTER="backend-existing"
FAKE_BACKEND_STATE="running"
FAKE_BACKEND_HEALTH="healthy"
FAKE_BACKEND_STARTED_AT="2026-07-21T00:00:00Z"
set +e
output="$(run_install 2>&1)"
status=$?
set -e
if [ "$status" -ne 18 ]; then
  printf 'expected frontend failure status 18, got %d\n' "$status" >&2
  exit 1
fi
assert_contains "$output" "its logs were not included"
assert_not_contains "$output" "$FAKE_BACKEND_LOG"
assert_not_contains "$(<"$CALL_LOG")" "logs --no-color --tail=100 --since"

: >"$CALL_LOG"
FAKE_COMPOSE_ENVIRONMENT="$strong_environment"
FAKE_UP_STATUS=0
FAKE_BACKEND_ID_BEFORE="backend-existing"
FAKE_BACKEND_ID_AFTER="backend-existing"
FAKE_BACKEND_STATE="running"
FAKE_BACKEND_HEALTH="healthy"
FAKE_BACKEND_STARTED_AT="2026-07-21T00:00:00Z"
output="$(run_install 2>&1)"
assert_contains "$output" "TokenHub started successfully"
assert_contains "$(<"$CALL_LOG")" " pull"
assert_not_contains "$(<"$CALL_LOG")" " build"
assert_contains "$(<"$CALL_LOG")" "up -d --remove-orphans --no-build --pull never --wait --wait-timeout 180"
assert_contains "$(<"$CALL_LOG")" "ps"

: >"$CALL_LOG"
FAKE_COMPOSE_ENVIRONMENT="$strong_environment"
output="$(run_install --build 2>&1)"
assert_contains "$output" "building TokenHub images from the local checkout"
assert_not_contains "$(<"$CALL_LOG")" " pull"
assert_contains "$(<"$CALL_LOG")" " build"
assert_contains "$(<"$CALL_LOG")" "up -d --remove-orphans --no-build --pull never --wait --wait-timeout 180"

development_environment=$(cat <<'EOF'
TOKENHUB_ENV=dev
TOKENHUB_ADMIN_TOKEN=dev_admin_token
TOKENHUB_SECRET_KEY=dev_tokenhub_secret_key
TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD=admin123456
EOF
)

: >"$CALL_LOG"
FAKE_COMPOSE_ENVIRONMENT="$development_environment"
output="$(run_install --check-only 2>&1)"
assert_contains "$output" "deployment configuration is valid for dev"

printf 'deploy/install.sh tests passed\n'
