#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
test_root="$(mktemp -d)"
volume="tokenhub-plugin-volume-test-$$"
image="node:22.23.1-bookworm-slim"
cleanup() {
  docker volume rm "$volume" >/dev/null 2>&1 || true
  rm -rf -- "$test_root"
}
trap cleanup EXIT

docker volume create "$volume" >/dev/null
cp "$script_dir/tokenhub-entrypoint" "$test_root/tokenhub-entrypoint"
chmod 0755 "$test_root/tokenhub-entrypoint"
mkdir -p "$test_root/bundle/bin" "$test_root/bundle/frontend" "$test_root/bundle/catalog" "$test_root/bundle/deploy"
for file in bin/tokenhub bin/node frontend/server.js catalog/model-catalog.yaml catalog/provider-catalog.json deploy/tokenhub.service; do
  touch "$test_root/bundle/$file"
done
printf '0.0.1\n' >"$test_root/bundle/VERSION"
printf '%064d\n' 1 >"$test_root/bundle/BUILD_ID"
cat >"$test_root/bundle/bin/tokenhub-run" <<'RUNNER'
#!/bin/sh
set -eu
[ "$(id -u)" = "$(id -u node)" ]
[ "$(stat -c %u "$TOKENHUB_PLUGIN_DIR")" = "$(id -u node)" ]
cd "$TOKENHUB_PLUGIN_DIR"
if [ "$TOKENHUB_TEST_PHASE" = restart ]; then
  [ "$(cat .state-test)" = disabled ]
  [ ! -e test.plugin ]
  exit 0
fi
# Exercise the storage operations used by package installation, update,
# rollback, removal, and built-in plugin state persistence as the runtime user.
stage="$(mktemp -d .install-XXXXXX)"
printf '1.0.0\n' >"$stage/VERSION"
mv "$stage" test.plugin
stage="$(mktemp -d .install-XXXXXX)"
printf '2.0.0\n' >"$stage/VERSION"
mv test.plugin .rollback-test
mv "$stage" test.plugin
[ "$(cat test.plugin/VERSION)" = 2.0.0 ]
mv test.plugin .removed-test
mv .rollback-test test.plugin
[ "$(cat test.plugin/VERSION)" = 1.0.0 ]
printf 'disabled\n' >.state-test.tmp
mv .state-test.tmp .state-test
rm -r test.plugin .removed-test
RUNNER
chmod -R a+rX "$test_root/bundle"
chmod 0755 "$test_root/bundle/bin/"*

run_container() {
  docker run --rm \
    -v "$test_root/tokenhub-entrypoint:/usr/local/bin/tokenhub-entrypoint:ro" \
    -v "$test_root/bundle:/image:ro" \
    -v "$volume:$1" \
    -e TOKENHUB_CONTAINER_IMAGE_ROOT=/image \
    -e "TOKENHUB_PLUGIN_DIR=$1" \
    -e "TOKENHUB_TEST_PHASE=$2" \
    --entrypoint /usr/local/bin/tokenhub-entrypoint "$image"
}

# A fresh named volume starts root-owned because the mount target does not
# exist in the base image. Repeat with a configured path outside /app/plugins.
for plugin_dir in /app/plugins /srv/tokenhub/custom-plugins; do
  docker run --rm -v "$volume:$plugin_dir" "$image" sh -c 'test "$(stat -c %u "$1")" = 0' sh "$plugin_dir"
  run_container "$plugin_dir" lifecycle
  run_container "$plugin_dir" restart
  docker volume rm "$volume" >/dev/null
  docker volume create "$volume" >/dev/null
done

for invalid in / // relative /app/../plugins /app/./plugins; do
  if TOKENHUB_PLUGIN_DIR="$invalid" sh "$script_dir/tokenhub-entrypoint" >"$test_root/invalid.log" 2>&1; then
    printf 'invalid plugin path accepted: %s\n' "$invalid" >&2
    exit 1
  fi
  grep -q 'TOKENHUB_PLUGIN_DIR must' "$test_root/invalid.log"
done
printf 'container plugin volume tests passed\n'
