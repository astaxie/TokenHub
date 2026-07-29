#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTALL_SCRIPT="$SCRIPT_DIR/install.sh"
TEST_DIR="$(mktemp -d)"
FAKE_DOCKER="$TEST_DIR/docker"
ENV_FILE="$TEST_DIR/deploy.env"
CALL_LOG="$TEST_DIR/calls.log"

cleanup() {
  rm -rf "$TEST_DIR"
}
trap cleanup EXIT

printf 'TOKENHUB_ENV=prod\n' >"$ENV_FILE"

cat >"$FAKE_DOCKER" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

printf '%s\n' "$*" >>"$FAKE_CALL_LOG"
up_has_run=false
if grep -q 'up -d --build' "$FAKE_CALL_LOG"; then
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
  *" ps -a -q tokenhub-backend")
    if [ "$up_has_run" = true ]; then
      printf '%s\n' "${FAKE_BACKEND_ID_AFTER:-}"
    else
      printf '%s\n' "${FAKE_BACKEND_ID_BEFORE:-}"
    fi
    ;;
  *" up -d --build")
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
TOKENHUB_INTEGRATION_TOKEN=iiiiiiiiiiiiiiiiiiiiiiiiiiiiiiii
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
assert_not_contains "$(<"$CALL_LOG")" "up -d --build"

unicode_whitespace=$'\302\205\302\240\341\232\200\342\200\200\342\200\201\342\200\202\342\200\203\342\200\204\342\200\205\342\200\206\342\200\207\342\200\210\342\200\211\342\200\212\342\200\250\342\200\251\342\200\257\342\201\237\343\200\200'
unicode_password="${unicode_whitespace}aaaaaaaaaaa${unicode_whitespace}"
unicode_whitespace_environment="$(printf 'TOKENHUB_ENV=prod\nTOKENHUB_ADMIN_TOKEN=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\nTOKENHUB_INTEGRATION_TOKEN=iiiiiiiiiiiiiiiiiiiiiiiiiiiiiiii\nTOKENHUB_SECRET_KEY=ssssssssssssssssssssssssssssssss\nTOKENHUB_BOOTSTRAP_ADMIN_PASSWORD=%s\n' "$unicode_password")"

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
assert_not_contains "$(<"$CALL_LOG")" "up -d --build"

strong_environment=$(cat <<'EOF'
TOKENHUB_ENV=prod
TOKENHUB_ADMIN_TOKEN=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
TOKENHUB_INTEGRATION_TOKEN=iiiiiiiiiiiiiiiiiiiiiiiiiiiiiiii
TOKENHUB_SECRET_KEY=ssssssssssssssssssssssssssssssss
TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD=strong-admin-password
EOF
)

: >"$CALL_LOG"
FAKE_COMPOSE_ENVIRONMENT="$strong_environment"
output="$(run_install --check-only 2>&1)"
assert_contains "$output" "deployment configuration is valid for prod"
assert_not_contains "$(<"$CALL_LOG")" "up -d --build"

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
assert_contains "$(<"$CALL_LOG")" "up -d --build"
assert_contains "$(<"$CALL_LOG")" "ps"

development_environment=$(cat <<'EOF'
TOKENHUB_ENV=dev
TOKENHUB_ADMIN_TOKEN=dev_admin_token
TOKENHUB_INTEGRATION_TOKEN=dev_integration_token
TOKENHUB_SECRET_KEY=dev_tokenhub_secret_key
TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD=admin123456
EOF
)

: >"$CALL_LOG"
FAKE_COMPOSE_ENVIRONMENT="$development_environment"
output="$(run_install --check-only 2>&1)"
assert_contains "$output" "deployment configuration is valid for dev"

printf 'deploy/install.sh tests passed\n'
