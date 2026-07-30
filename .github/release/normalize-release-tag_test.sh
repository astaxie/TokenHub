#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
normalizer="$script_dir/normalize-release-tag.sh"

expect_version() {
  local tag="$1"
  local expected="$2"
  local actual
  actual="$("$normalizer" "$tag")"
  [ "$actual" = "$expected" ] || {
    printf 'normalize %s = %s, want %s\n' "$tag" "$actual" "$expected" >&2
    exit 1
  }
}

expect_invalid() {
  if "$normalizer" "$1" >/dev/null 2>&1; then
    printf 'expected invalid release tag: %s\n' "$1" >&2
    exit 1
  fi
}

expect_version "v0.3.8" "0.3.8"
expect_version "v1.2.3-rc.1" "1.2.3-rc.1"
expect_invalid "0.3.8"
expect_invalid "v01.2.3"
expect_invalid "v1.2.3-01"
expect_invalid "v1.2.3+build.1"

printf 'release tag tests passed\n'
