#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CURRENT_DIR="$(cd "${1:-$ROOT_DIR/output/benchmarks/current}" && pwd)"
REPORT_DIR="${TOKENHUB_BENCHMARK_REPORT_DIR:-$ROOT_DIR/output/benchmarks/checks}"

mkdir -p "$REPORT_DIR"
for baseline in "$ROOT_DIR"/benchmarks/baselines/*.json; do
  name="$(basename "$baseline")"
  current="$CURRENT_DIR/$name"
  if [ ! -f "$current" ]; then
    printf 'Missing current result: %s\n' "$current" >&2
    exit 1
  fi
  (
    cd "$ROOT_DIR/backend"
    go run ./cmd/tokenhub-benchmark check \
      --baseline "$baseline" \
      --current "$current" \
      --budget "$ROOT_DIR/benchmarks/budget.json" \
      --markdown "$REPORT_DIR/${name%.json}.md"
  )
done
