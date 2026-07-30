#!/usr/bin/env bash
set -euo pipefail

raw_tag="${1:-}"
numeric_identifier='(0|[1-9][0-9]*)'
prerelease_identifier='(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)'
semver_pattern="^v${numeric_identifier}\.${numeric_identifier}\.${numeric_identifier}(-${prerelease_identifier}(\.${prerelease_identifier})*)?$"

if [[ ! "$raw_tag" =~ $semver_pattern ]]; then
  printf 'Release tag must be v-prefixed semantic version, for example v0.3.1 or v0.3.1-rc.1\n' >&2
  exit 1
fi

printf '%s\n' "${raw_tag#v}"
