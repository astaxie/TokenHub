#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BENCHTIME="${TOKENHUB_BENCHMARK_BENCHTIME:-3s}"
COUNT="${TOKENHUB_BENCHMARK_COUNT:-5}"
RESULT_PATH="${TOKENHUB_BENCHMARK_GO_RESULT:-$ROOT_DIR/output/benchmarks/go-benchmarks.txt}"
SUMMARY_PATH="${TOKENHUB_BENCHMARK_GO_SUMMARY:-$ROOT_DIR/output/benchmarks/go-benchmarks.json}"
REPORT_PATH="${TOKENHUB_BENCHMARK_GO_REPORT:-$ROOT_DIR/output/benchmarks/go-budget-report.json}"
PROFILE="sqlite"
if [ -n "${TOKENHUB_BENCHMARK_POSTGRES_URL:-}" ]; then
  PROFILE="sqlite-postgresql"
fi
BASELINE_PATH="${TOKENHUB_BENCHMARK_GO_BASELINE:-$ROOT_DIR/benchmarks/baselines/internal/$PROFILE.json}"
BUDGET_PATH="${TOKENHUB_BENCHMARK_GO_BUDGET:-$ROOT_DIR/benchmarks/internal-budget.json}"

mkdir -p "$(dirname "$RESULT_PATH")" "$(dirname "$SUMMARY_PATH")" "$(dirname "$REPORT_PATH")"
cd "$ROOT_DIR/backend"
go test ./internal/server \
  -run '^$' \
  -bench '^BenchmarkGateway' \
  -benchmem \
  -benchtime "$BENCHTIME" \
  -count "$COUNT" | tee "$RESULT_PATH"

if [ "${TOKENHUB_BENCHMARK_UPDATE_BASELINE:-0}" = "1" ]; then
  go run ./cmd/tokenhub-benchmark summarize-go \
    --input "$RESULT_PATH" \
    --output "$BASELINE_PATH"
  printf 'Updated internal benchmark baseline: %s\n' "$BASELINE_PATH"
  exit 0
fi

if [ ! -f "$BASELINE_PATH" ]; then
  printf 'Missing internal benchmark baseline for profile %s: %s\n' "$PROFILE" "$BASELINE_PATH" >&2
  printf 'Generate it from a clean revision with TOKENHUB_BENCHMARK_UPDATE_BASELINE=1.\n' >&2
  exit 1
fi

go run ./cmd/tokenhub-benchmark summarize-go \
  --input "$RESULT_PATH" \
  --output "$SUMMARY_PATH"
go run ./cmd/tokenhub-benchmark check-go \
  --baseline "$BASELINE_PATH" \
  --current "$RESULT_PATH" \
  --budget "$BUDGET_PATH" | tee "$REPORT_PATH"
