#!/usr/bin/env bash
set -Eeuo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
test_root="$(mktemp -d)"
trap 'rm -rf -- "$test_root"' EXIT

# shellcheck source=install.sh
source "$script_dir/install.sh"

fail_test() {
  printf 'native installer test: %s\n' "$*" >&2
  exit 1
}

assert_fails() {
  if ("$@" >/dev/null 2>&1); then
    fail_test "expected command to fail: $*"
  fi
}

create_bundle() {
  local root="$1"
  local version="$2"
  mkdir -p "$root/bin" "$root/frontend" "$root/catalog" "$root/deploy"
  : >"$root/bin/tokenhub"
  : >"$root/bin/node"
  cp "$script_dir/tokenhub-run" "$root/bin/tokenhub-run"
  : >"$root/frontend/server.js"
  : >"$root/catalog/model-catalog.yaml"
  printf '{"providers":[]}\n' >"$root/catalog/provider-catalog.json"
  cp "$script_dir/tokenhub.service" "$root/deploy/tokenhub.service"
  printf '%s\n' "$version" >"$root/VERSION"
}

[ "$(normalize_version v0.3.3)" = "0.3.3" ] || fail_test "version normalization failed"
assert_fails normalize_version "0.3"
assert_fails normalize_version "v0.3.3-01"
[ "$(compare_semantic_versions 0.3.2 0.3.3)" = "-1" ] ||
  fail_test "semantic patch comparison failed"
[ "$(compare_semantic_versions 1.0.0-rc.10 1.0.0-rc.2)" = "1" ] ||
  fail_test "semantic numeric prerelease comparison failed"
[ "$(compare_semantic_versions 1.0.0-rc.1 1.0.0)" = "-1" ] ||
  fail_test "semantic stable/prerelease comparison failed"
[ "$(url_host "127.0.0.1")" = "127.0.0.1" ] || fail_test "IPv4 URL host normalization failed"
[ "$(url_host "2001:db8::1")" = "[2001:db8::1]" ] || fail_test "IPv6 URL host normalization failed"
[ "$(url_host "[2001:db8::1]")" = "[2001:db8::1]" ] || fail_test "bracketed IPv6 URL host normalization failed"
is_ip_address "18.138.123.53" || fail_test "IPv4 address was rejected"
is_ip_address "2606:4700:4700::1111" || fail_test "IPv6 address was rejected"
assert_fails is_ip_address "999.1.1.1"
assert_fails is_ip_address '1.2.3.4$(touch /tmp/tokenhub-test)'

public_host_config="$test_root/public-host-config"
mkdir -p "$public_host_config"
CONFIG_DIR="$public_host_config"
RESOLVED_PUBLIC_HOST=""
RESOLVED_PUBLIC_HOST_SOURCE=""
TOKENHUB_PUBLIC_HOST="tokenhub.example.com"
resolve_public_host
[ "$RESOLVED_PUBLIC_HOST" = "tokenhub.example.com" ] ||
  fail_test "explicit public host was not preserved"
[ "$RESOLVED_PUBLIC_HOST_SOURCE" = "explicit" ] ||
  fail_test "explicit public host source was not recorded"
unset TOKENHUB_PUBLIC_HOST

printf 'TOKENHUB_PUBLIC_HOST=stored.example.com\n' >"$CONFIG_DIR/tokenhub.env"
TOKENHUB_PUBLIC_HOST="ignored.example.com"
RESOLVED_PUBLIC_HOST=""
RESOLVED_PUBLIC_HOST_SOURCE=""
resolve_public_host
[ "$RESOLVED_PUBLIC_HOST" = "stored.example.com" ] ||
  fail_test "stored public host was not reused"
[ "$RESOLVED_PUBLIC_HOST_SOURCE" = "config" ] ||
  fail_test "stored public host source was not recorded"
rm -f -- "$CONFIG_DIR/tokenhub.env"
unset TOKENHUB_PUBLIC_HOST

printf 'TOKENHUB_ENV=prod\n' >"$CONFIG_DIR/tokenhub.env"
TOKENHUB_PUBLIC_HOST="migrated.example.com"
RESOLVED_PUBLIC_HOST=""
RESOLVED_PUBLIC_HOST_SOURCE=""
ensure_public_host_config
grep -Fqx "TOKENHUB_PUBLIC_HOST=migrated.example.com" "$CONFIG_DIR/tokenhub.env" ||
  fail_test "missing public host was not added to an existing configuration"
rm -f -- "$CONFIG_DIR/tokenhub.env"
unset TOKENHUB_PUBLIC_HOST

curl() {
  case "$*" in
    *ipinfo.io/json*)
      printf '{"ip":"8.8.8.8","city":"test"}\n'
      ;;
    *)
      return 1
      ;;
  esac
}
hostname() {
  printf '10.0.0.2\n'
}
RESOLVED_PUBLIC_HOST=""
RESOLVED_PUBLIC_HOST_SOURCE=""
resolve_public_host
[ "$RESOLVED_PUBLIC_HOST" = "8.8.8.8" ] ||
  fail_test "ipinfo public address was not detected"
[ "$RESOLVED_PUBLIC_HOST_SOURCE" = "ipinfo" ] ||
  fail_test "ipinfo source was not recorded"
unset -f curl hostname

curl() {
  case "$*" in
    *ipinfo.io/json*)
      printf '{"ip":"not-an-ip"}\n'
      ;;
    *)
      return 1
      ;;
  esac
}
hostname() {
  printf '10.0.0.2 1.1.1.1\n'
}
RESOLVED_PUBLIC_HOST=""
RESOLVED_PUBLIC_HOST_SOURCE=""
resolve_public_host
[ "$RESOLVED_PUBLIC_HOST" = "10.0.0.2" ] ||
  fail_test "first hostname address was not used as the fallback"
[ "$RESOLVED_PUBLIC_HOST_SOURCE" = "hostname" ] ||
  fail_test "hostname fallback source was not recorded"
unset -f curl hostname
RESOLVED_PUBLIC_HOST=""
RESOLVED_PUBLIC_HOST_SOURCE=""

TOKENHUB_DATABASE_URL=""
[ "$(initial_database_url)" = "sqlite:///var/lib/tokenhub/tokenhub.db" ] ||
  fail_test "empty database URL did not use the native SQLite default"
TOKENHUB_DATABASE_URL="postgres://tokenhub:test@db.internal:5432/tokenhub?sslmode=require"
[ "$(initial_database_url)" = "$TOKENHUB_DATABASE_URL" ] ||
  fail_test "explicit database URL was not preserved"
TOKENHUB_DATABASE_URL=$'postgres://tokenhub:test@db.internal/tokenhub\nINJECTED=value'
assert_fails initial_database_url
unset TOKENHUB_DATABASE_URL

validate_port "08" "test port"
assert_fails validate_port "0" "test port"
port_config="$test_root/port-config"
mkdir -p "$port_config"
CONFIG_DIR="$port_config"
BACKEND_PORT=18080
FRONTEND_PORT=13000
ss() {
  case "$*" in
    *":18080"*) printf 'LISTEN 0 4096 0.0.0.0:18080 0.0.0.0:*\n' ;;
  esac
}
set +e
port_output="$(validate_fresh_install_ports 2>&1)"
port_status=$?
set -e
[ "$port_status" -ne 0 ] || fail_test "fresh install accepted an occupied backend port"
[[ "$port_output" == *"backend port 18080 is already in use"* ]] ||
  fail_test "occupied backend port failed without an actionable message: $port_output"
printf 'TOKENHUB_HTTP_ADDR=:18080\n' >"$CONFIG_DIR/tokenhub.env"
validate_fresh_install_ports
rm -f -- "$CONFIG_DIR/tokenhub.env"
BACKEND_PORT=13000
assert_fails validate_fresh_install_ports
BACKEND_PORT=18081
FRONTEND_PORT=13001
validate_fresh_install_ports
unset -f ss
BACKEND_PORT=8080
FRONTEND_PORT=3000
assert_fails validate_safe_path "/opt/../etc" "test path"
assert_fails validate_safe_path "/opt/" "test path"
assert_fails validate_safe_path "/var//lib" "test path"
assert_fails validate_managed_path "/opt" "test path"
assert_fails validate_managed_path "/etc" "test path"
assert_fails validate_managed_path "/var/lib" "test path"
assert_fails validate_managed_path "/custom" "test path"
validate_managed_path "/opt/tokenhub" "test path"

marker_root="$test_root/marker-root"
INSTALL_ROOT="$marker_root/opt/tokenhub"
CONFIG_DIR="$marker_root/etc/tokenhub"
STATE_DIR="$marker_root/var/lib/tokenhub"
SERVICE_NAME="tokenhub-test"
SERVICE_USER="$(id -un)"
SERVICE_GROUP="$(id -gn)"
mkdir -p "$INSTALL_ROOT" "$CONFIG_DIR" "$STATE_DIR"
assert_fails require_managed_directory_marker "$INSTALL_ROOT" "application"
write_managed_directory_marker "$INSTALL_ROOT" "application"
write_managed_directory_marker "$CONFIG_DIR" "configuration"
write_managed_directory_marker "$STATE_DIR" "state"
require_managed_directory_marker "$INSTALL_ROOT" "application"
validate_upgrade_target "0.3.3"
printf 'invalid\n' >"$INSTALL_ROOT/$MANAGED_DIRECTORY_MARKER"
assert_fails require_managed_directory_marker "$INSTALL_ROOT" "application"

unmarked_system_directory="$test_root/etc/ssh"
mkdir -p "$unmarked_system_directory"
: >"$unmarked_system_directory/sshd_config"
assert_fails prepare_managed_directory "$unmarked_system_directory" "configuration" "$SERVICE_USER" "0700"

readiness_config="$test_root/readiness-config"
mkdir -p "$readiness_config"
printf 'TOKENHUB_HTTP_ADDR=:18080\nTOKENHUB_FRONTEND_PORT=13000\n' >"$readiness_config/tokenhub.env"
CONFIG_DIR="$readiness_config"
SERVICE_READY_ATTEMPTS=1
systemctl() {
  [ "$1" = "status" ]
}
curl() {
  return 0
}
sleep() {
  return 0
}
readiness_journal_log="$test_root/readiness-journal.log"
journalctl() {
  printf '%s\n' "$*" >"$readiness_journal_log"
}
assert_fails wait_for_service_ready
[ -s "$readiness_journal_log" ] ||
  fail_test "readiness failure did not request recent service logs"
grep -Fqx -- "-u tokenhub-test -n 50 --no-pager" "$readiness_journal_log" ||
  fail_test "readiness failure requested the wrong journal range"
unset -f systemctl curl sleep journalctl

config_values="$test_root/config-values"
printf 'TOKENHUB_RELEASE_REPOSITORY=first/repo\nTOKENHUB_RELEASE_REPOSITORY=second/repo\n' >"$config_values"
[ "$(read_config_value "$config_values" TOKENHUB_RELEASE_REPOSITORY)" = "second/repo" ] ||
  fail_test "configuration reader did not use the final assignment"

SERVICE_USER="tokenhub"
SERVICE_NAME="tokenhub"
validate_identifiers
SERVICE_NAME="../tokenhub"
assert_fails validate_identifiers
SERVICE_NAME="tokenhub"

valid_bundle="$test_root/valid"
valid_archive="$test_root/valid.tar.gz"
create_bundle "$valid_bundle" "0.3.3"
tar -czf "$valid_archive" -C "$valid_bundle" .
validate_release_archive "$valid_archive"
validate_staged_release "$valid_bundle" "0.3.3"
assert_fails validate_staged_release "$valid_bundle" "0.3.2"

saved_entry_limit="$MAX_NATIVE_ARCHIVE_ENTRIES"
MAX_NATIVE_ARCHIVE_ENTRIES=1
assert_fails validate_release_archive "$valid_archive"
MAX_NATIVE_ARCHIVE_ENTRIES="$saved_entry_limit"
saved_file_limit="$MAX_NATIVE_ARCHIVE_FILE_BYTES"
MAX_NATIVE_ARCHIVE_FILE_BYTES=1
assert_fails validate_release_archive "$valid_archive"
MAX_NATIVE_ARCHIVE_FILE_BYTES="$saved_file_limit"
saved_extracted_limit="$MAX_NATIVE_EXTRACTED_BYTES"
MAX_NATIVE_EXTRACTED_BYTES=1
assert_fails validate_release_archive "$valid_archive"
MAX_NATIVE_EXTRACTED_BYTES="$saved_extracted_limit"

linked_bundle="$test_root/linked"
linked_archive="$test_root/linked.tar.gz"
create_bundle "$linked_bundle" "0.3.3"
ln -s /etc/passwd "$linked_bundle/frontend/external"
tar -czf "$linked_archive" -C "$linked_bundle" .
assert_fails validate_release_archive "$linked_archive"
assert_fails validate_staged_release "$linked_bundle" "0.3.3"

curl() {
  local destination=""
  while [ "$#" -gt 0 ]; do
    if [ "$1" = "-o" ]; then
      destination="$2"
      shift 2
      continue
    fi
    shift
  done
  dd if=/dev/zero of="$destination" bs=2048 count=1 2>/dev/null
}
assert_fails download_file "https://example.com/oversized" "$test_root/oversized" 16 "oversized test asset"
unset -f curl

runner_root="$test_root/runner"
mkdir -p "$runner_root/bin" "$runner_root/frontend"
cp "$script_dir/tokenhub-run" "$runner_root/bin/tokenhub-run"
cat >"$runner_root/bin/tokenhub" <<'EOF'
#!/usr/bin/env bash
[ "${TOKENHUB_RUNNER_MARKER:-}" = "loaded" ] || exit 12
sleep 0.2
exit 7
EOF
cat >"$runner_root/bin/node" <<'EOF'
#!/usr/bin/env bash
trap 'exit 0' TERM
while :; do sleep 1; done
EOF
: >"$runner_root/frontend/server.js"
chmod 0755 "$runner_root/bin/tokenhub-run" "$runner_root/bin/tokenhub" "$runner_root/bin/node"

set +e
TOKENHUB_RUNNER_MARKER=loaded "$runner_root/bin/tokenhub-run"
runner_status=$?
set -e
[ "$runner_status" -eq 7 ] || fail_test "runner returned $runner_status instead of backend status 7"

run_linux_integration() {
  local fixtures="$test_root/fixtures"
  local fake_bin="$test_root/fake-bin"
  local install_root="$test_root/opt/tokenhub"
  local config_dir="$test_root/etc/tokenhub"
  local state_dir="$test_root/var/lib/tokenhub"
  local systemd_dir="$test_root/etc/systemd/system"
  local integration_bundle="$test_root/integration-bundle"
  local integration_upgrade_bundle="$test_root/integration-upgrade-bundle"
  local service_user
  local asset
  local first_output
  local first_status
  local retry_output
  local generated_password
  local postgres_url="postgres://tokenhub:test@db.internal:5432/tokenhub?sslmode=require"
  local invalid_rollback_output
  local invalid_rollback_status
  local downgrade_output
  local downgrade_status
  local upgrade_output
  local -a installer_env

  service_user="$(id -un)"
  mkdir -p "$fixtures" "$fake_bin" "$systemd_dir"
  create_bundle "$integration_bundle" "0.3.3"
  chmod 0755 "$integration_bundle/bin/tokenhub" "$integration_bundle/bin/node" "$integration_bundle/bin/tokenhub-run"
  for asset in \
    tokenhub_0.3.3_linux_amd64.tar.gz \
    tokenhub_0.3.3_linux_arm64.tar.gz; do
    tar -czf "$fixtures/$asset" -C "$integration_bundle" .
  done
  create_bundle "$integration_upgrade_bundle" "0.3.4"
  chmod 0755 \
    "$integration_upgrade_bundle/bin/tokenhub" \
    "$integration_upgrade_bundle/bin/node" \
    "$integration_upgrade_bundle/bin/tokenhub-run"
  for asset in \
    tokenhub_0.3.4_linux_amd64.tar.gz \
    tokenhub_0.3.4_linux_arm64.tar.gz; do
    tar -czf "$fixtures/$asset" -C "$integration_upgrade_bundle" .
  done
  (
    cd "$fixtures"
    sha256sum tokenhub_*.tar.gz >checksums.txt
  )

  cat >"$fake_bin/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
destination=""
url=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o)
      destination="$2"
      shift 2
      ;;
    *)
      url="$1"
      shift
      ;;
  esac
done
[ -n "$url" ]
printf '%s\n' "$url" >>"$TOKENHUB_TEST_CURL_LOG"
if [[ "$url" == http://127.0.0.1:* ]]; then
  exit 0
fi
[ -n "$destination" ]
if [ -n "${TOKENHUB_TEST_CURL_FAIL_ONCE:-}" ] &&
  [ ! -e "$TOKENHUB_TEST_CURL_FAIL_ONCE" ]; then
  touch "$TOKENHUB_TEST_CURL_FAIL_ONCE"
  exit 22
fi
cp "$TOKENHUB_TEST_FIXTURES/${url##*/}" "$destination"
EOF
  cat >"$fake_bin/systemctl" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$TOKENHUB_TEST_SYSTEMCTL_LOG"
EOF
  cat >"$fake_bin/userdel" <<'EOF'
#!/usr/bin/env bash
touch "$TOKENHUB_TEST_USERDEL_LOG"
EOF
  cat >"$fake_bin/ss" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
  chmod 0755 "$fake_bin/curl" "$fake_bin/systemctl" "$fake_bin/userdel" "$fake_bin/ss"

  installer_env=(
    "PATH=$fake_bin:$PATH"
    "TOKENHUB_TEST_FIXTURES=$fixtures"
    "TOKENHUB_TEST_SYSTEMCTL_LOG=$test_root/systemctl.log"
    "TOKENHUB_TEST_CURL_LOG=$test_root/curl.log"
    "TOKENHUB_TEST_CURL_FAIL_ONCE=$test_root/curl-failed"
    "TOKENHUB_TEST_USERDEL_LOG=$test_root/userdel.log"
    "TOKENHUB_INSTALL_ROOT=$install_root"
    "TOKENHUB_CONFIG_DIR=$config_dir"
    "TOKENHUB_STATE_DIR=$state_dir"
    "TOKENHUB_SYSTEMD_DIR=$systemd_dir"
    "TOKENHUB_INSTALLER_ALLOW_NON_ROOT=1"
    "TOKENHUB_SERVICE_USER=$service_user"
    "TOKENHUB_SERVICE_NAME=tokenhub-test"
    "TOKENHUB_PUBLIC_HOST=127.0.0.1"
    "TOKENHUB_DATABASE_URL=$postgres_url"
  )

  set +e
  first_output="$(
    env "${installer_env[@]}" \
      TOKENHUB_RELEASE_REPOSITORY=example/TokenHub \
      bash "$script_dir/install.sh" install --version 0.3.3 2>&1
  )"
  first_status=$?
  set -e
  [ "$first_status" -ne 0 ] || fail_test "integration install unexpectedly survived its forced download failure"
  [ -s "$config_dir/tokenhub.env" ] ||
    fail_test "failed install did not preserve generated configuration: $first_output"
  [ -f "$config_dir/.initial-admin-password-pending" ] ||
    fail_test "failed install did not retain the pending password marker"
  grep -Fqx "TOKENHUB_DATABASE_URL=$postgres_url" "$config_dir/tokenhub.env" ||
    fail_test "first install did not persist the explicit database URL"
  grep -Fqx "TOKENHUB_PUBLIC_HOST=127.0.0.1" "$config_dir/tokenhub.env" ||
    fail_test "first install did not persist the resolved public host"
  [[ "$first_output" != *"Initial admin password:"* ]] ||
    fail_test "failed install printed credentials before installation completed"

  : >"$test_root/curl.log"
  retry_output="$(
    env -u TOKENHUB_RELEASE_REPOSITORY "${installer_env[@]}" \
      TOKENHUB_DATABASE_URL=postgres://ignored:ignored@other.invalid/ignored \
      bash "$script_dir/install.sh" install --version 0.3.3 2>&1
  )"
  generated_password="$(
    awk -F= '$1 == "TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD" { print substr($0, index($0, "=") + 1); exit }' \
      "$config_dir/tokenhub.env"
  )"
  [ -n "$generated_password" ] || fail_test "integration install did not generate an admin password"
  [[ "$retry_output" == *"Initial admin password: $generated_password"* ]] ||
    fail_test "successful install retry did not print the pending admin password"
  [ ! -e "$config_dir/.initial-admin-password-pending" ] ||
    fail_test "successful install retry kept the pending password marker"
  grep -q 'github.com/example/TokenHub/releases/download/' "$test_root/curl.log" ||
    fail_test "install retry did not reuse the configured release repository"
  grep -Fqx "TOKENHUB_DATABASE_URL=$postgres_url" "$config_dir/tokenhub.env" ||
    fail_test "install retry replaced the existing database URL"
  [[ "$retry_output" == *"Existing configuration keeps its current TOKENHUB_DATABASE_URL"* ]] ||
    fail_test "install retry did not warn that a different database URL was ignored"

  [ "$(tr -d '[:space:]' <"$install_root/current/VERSION")" = "0.3.3" ] ||
    fail_test "integration install did not activate v0.3.3"
  [ -s "$config_dir/tokenhub.env" ] || fail_test "integration install did not create configuration"
  grep -q "^TOKENHUB_IMAGE_STORAGE_DIR=$state_dir/images$" "$config_dir/tokenhub.env" ||
    fail_test "integration install did not configure persistent image storage"
  [ -d "$state_dir/images" ] ||
    fail_test "integration install did not create the persistent image directory"
  [ -f "$install_root/.tokenhub-managed-directory" ] ||
    fail_test "integration install did not mark the application directory"
  [ -f "$config_dir/.tokenhub-managed-directory" ] ||
    fail_test "integration install did not mark the configuration directory"
  [ -f "$state_dir/.tokenhub-managed-directory" ] ||
    fail_test "integration install did not mark the state directory"
  grep -q '^is-active --quiet tokenhub-test$' "$test_root/systemctl.log" ||
    fail_test "integration install did not verify systemd readiness"
  grep -q 'http://127.0.0.1:8080/healthz' "$test_root/curl.log" ||
    fail_test "integration install did not probe backend health"
  grep -q 'http://127.0.0.1:3000/' "$test_root/curl.log" ||
    fail_test "integration install did not probe the frontend"
  grep -q "$install_root/current/bin/tokenhub-run" "$systemd_dir/tokenhub-test.service" ||
    fail_test "systemd unit was not rendered with the install root"
  if grep -q '@[A-Z_]*@' "$systemd_dir/tokenhub-test.service"; then
    fail_test "systemd unit still contains template placeholders"
  fi
  if grep -q '^EnvironmentFile=-' "$systemd_dir/tokenhub-test.service"; then
    fail_test "systemd unit treats the production environment file as optional"
  fi

  for invalid_version in 0.3.3 0.3.4; do
    set +e
    invalid_rollback_output="$(
      env -u TOKENHUB_RELEASE_REPOSITORY "${installer_env[@]}" \
        bash "$script_dir/install.sh" rollback --version "$invalid_version" 2>&1
    )"
    invalid_rollback_status=$?
    set -e
    [ "$invalid_rollback_status" -ne 0 ] ||
      fail_test "rollback accepted non-older version $invalid_version"
    [[ "$invalid_rollback_output" == *"must be older than installed version"* ]] ||
      fail_test "rollback version $invalid_version failed for the wrong reason"
  done

  set +e
  downgrade_output="$(
    env -u TOKENHUB_RELEASE_REPOSITORY "${installer_env[@]}" \
      bash "$script_dir/install.sh" upgrade --version 0.3.2 2>&1
  )"
  downgrade_status=$?
  set -e
  [ "$downgrade_status" -ne 0 ] || fail_test "upgrade accepted an older version"
  [[ "$downgrade_output" == *"must not be older than installed version"* ]] ||
    fail_test "upgrade downgrade failed for the wrong reason: $downgrade_output"

  printf 'TOKENHUB_TEST_MARKER=preserved\n' >>"$config_dir/tokenhub.env"
  sed -i '/^TOKENHUB_IMAGE_STORAGE_DIR=/d' "$config_dir/tokenhub.env"
  sed -i '/^TOKENHUB_PUBLIC_HOST=/d' "$config_dir/tokenhub.env"
  env -u TOKENHUB_RELEASE_REPOSITORY "${installer_env[@]}" \
    bash "$script_dir/install.sh" upgrade --version 0.3.3
  grep -q '^TOKENHUB_TEST_MARKER=preserved$' "$config_dir/tokenhub.env" ||
    fail_test "integration upgrade replaced existing configuration"
  grep -q "^TOKENHUB_IMAGE_STORAGE_DIR=$state_dir/images$" "$config_dir/tokenhub.env" ||
    fail_test "integration upgrade did not migrate persistent image storage"
  grep -Fqx "TOKENHUB_PUBLIC_HOST=127.0.0.1" "$config_dir/tokenhub.env" ||
    fail_test "integration upgrade did not migrate the installer public host"

  sed -i 's/^TOKENHUB_FRONTEND_PORT=.*/TOKENHUB_FRONTEND_PORT=23000/' "$config_dir/tokenhub.env"
  : >"$test_root/curl.log"
  upgrade_output="$(
    env -u TOKENHUB_RELEASE_REPOSITORY "${installer_env[@]}" \
      TOKENHUB_PUBLIC_HOST= \
      bash "$script_dir/install.sh" upgrade --version 0.3.4 2>&1
  )"
  [ "$(tr -d '[:space:]' <"$install_root/current/VERSION")" = "0.3.4" ] ||
    fail_test "integration upgrade did not activate v0.3.4"
  grep -q 'http://127.0.0.1:23000/' "$test_root/curl.log" ||
    fail_test "integration upgrade did not probe the configured frontend port"
  [[ "$upgrade_output" == *"Admin console: http://127.0.0.1:23000"* ]] ||
    fail_test "integration upgrade did not print the configured frontend port: $upgrade_output"
  [[ "$upgrade_output" != *"Admin console: http://127.0.0.1:3000"* ]] ||
    fail_test "integration upgrade printed the invocation default frontend port"

  env -u TOKENHUB_RELEASE_REPOSITORY "${installer_env[@]}" \
    bash "$script_dir/install.sh" uninstall
  [ ! -e "$install_root" ] || fail_test "uninstall kept the application directory"
  [ -f "$config_dir/tokenhub.env" ] || fail_test "uninstall removed preserved configuration"
  [ -d "$state_dir" ] || fail_test "uninstall removed preserved state"

  env -u TOKENHUB_RELEASE_REPOSITORY "${installer_env[@]}" \
    bash "$script_dir/install.sh" uninstall --purge
  [ ! -e "$config_dir" ] && [ ! -e "$state_dir" ] ||
    fail_test "purge kept configuration or state"
  [ ! -e "$test_root/userdel.log" ] ||
    fail_test "purge attempted to delete a pre-existing service user"
}

if [ "${TOKENHUB_NATIVE_INTEGRATION:-}" = "1" ]; then
  run_linux_integration
fi

printf 'native installer tests passed\n'
