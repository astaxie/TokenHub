#!/usr/bin/env bash
# Shared helper for assembling the Next.js standalone bundle.
#
# Sourced by run-local.sh so a local run is laid out exactly like a deployment,
# and so the tricky parts live in one place:
#
#   * Next infers outputFileTracingRoot from the surrounding workspace. In a
#     repository checkout server.js therefore sits under a rebuilt path prefix
#     such as .next/standalone/<repo>/frontend/ rather than at the top. The
#     Docker build never sees this because its context is the frontend directory
#     alone, so a hardcoded path works there and fails here.
#   * `next start` does NOT work with output: "standalone" — Next says so at
#     startup. The bundle has to be launched as `node <bundle>/server.js`.
#   * The bundle does not include .next/static or public; both must be overlaid
#     afterwards or the console loads without assets.

# Prints the directory containing the standalone server.js.
#
# Searching for server.js by name would be wrong: the bundled dependency tree
# contains several files with exactly that name (next/server.js,
# react-dom/server.js, ...) and find's traversal order is unspecified, so the
# wrong directory could be packaged and still pass a superficial check. Anchor on
# .next/required-server-files.json instead, which Next writes once, in the
# application root, and nowhere else.
standalone_app_dir() {
  local frontend_dir="$1"
  local standalone_dir="$frontend_dir/.next/standalone"
  local marker candidate
  local -a matches=()
  while IFS= read -r marker; do
    candidate="$(dirname "$(dirname "$marker")")"
    [ -f "$candidate/server.js" ] || continue
    matches+=("$candidate")
  done < <(find "$standalone_dir" -maxdepth 15 -path '*/.next/required-server-files.json' -type f 2>/dev/null | sort)

  if [ "${#matches[@]}" -eq 0 ]; then
    printf 'No application root under %s (looked for .next/required-server-files.json next to a server.js).\n' \
      "$standalone_dir" >&2
    printf 'Is output: "standalone" still set in next.config.ts?\n' >&2
    return 1
  fi
  if [ "${#matches[@]}" -gt 1 ]; then
    printf 'Ambiguous standalone layout: %d application roots under %s\n' \
      "${#matches[@]}" "$standalone_dir" >&2
    printf '  %s\n' "${matches[@]}" >&2
    return 1
  fi
  printf '%s' "${matches[0]}"
}

# Assembles a runnable bundle at <target>, which is replaced if it exists.
assemble_standalone_bundle() {
  local frontend_dir="$1" target="$2"
  local app_dir
  app_dir="$(standalone_app_dir "$frontend_dir")" || return 1

  local standalone_dir="$frontend_dir/.next/standalone"
  if [ "$app_dir" != "$standalone_dir" ]; then
    # Anything traced outside the application directory would be dropped by the
    # copy below; in a single-package workspace there is nothing else.
    local stray
    stray="$(find "$standalone_dir" -mindepth 1 -maxdepth 15 -type f -not -path "$app_dir/*" 2>/dev/null | head -n 3)"
    if [ -n "$stray" ]; then
      printf 'WARNING: files traced outside the application root are not packaged:\n' >&2
      printf '  %s\n' $stray >&2
    fi
  fi

  rm -rf "$target"
  mkdir -p "$target"
  # Trailing "/." so dotfiles such as .next/ — which holds the server manifests
  # and compiled routes — are copied too. A glob would silently drop them and the
  # bundle would fail at runtime.
  cp -a "$app_dir/." "$target/"
  mkdir -p "$target/.next"
  cp -a "$frontend_dir/.next/static" "$target/.next/static"
  cp -a "$frontend_dir/public" "$target/public"

  local required
  for required in "$target/server.js" "$target/.next/static" "$target/public"; do
    if [ ! -e "$required" ]; then
      printf 'Frontend bundle is incomplete: missing %s\n' "$required" >&2
      return 1
    fi
  done
  return 0
}
