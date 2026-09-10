#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repository_root="$(cd "$script_dir/../.." && pwd)"
test_root="$(mktemp -d)"
trap 'rm -rf -- "$test_root"' EXIT

# Use the actual build context so .dockerignore cannot silently remove release assets.
docker build --file - --output "type=local,dest=$test_root" "$repository_root" <<'DOCKERFILE'
FROM scratch
COPY data/model-catalog.yaml /catalog/model-catalog.yaml
COPY data/provider-catalog.json /catalog/provider-catalog.json
COPY data/builtin-plugins /catalog/builtin-plugins
DOCKERFILE

cmp "$repository_root/data/model-catalog.yaml" "$test_root/catalog/model-catalog.yaml"
cmp "$repository_root/data/provider-catalog.json" "$test_root/catalog/provider-catalog.json"
diff -r "$repository_root/data/builtin-plugins" "$test_root/catalog/builtin-plugins"
test -f "$test_root/catalog/builtin-plugins/providers/openai/plugin.yaml"
test -f "$test_root/catalog/builtin-plugins/platform/tokenhub.sim.default/plugin.yaml"
printf 'container build context tests passed\n'
