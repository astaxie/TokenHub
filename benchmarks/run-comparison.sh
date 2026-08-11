#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RESULT_DIR="${TOKENHUB_BENCHMARK_RESULT_DIR:-$ROOT_DIR/output/benchmarks}"
DURATION="${TOKENHUB_BENCHMARK_DURATION:-30s}"
WARMUP="${TOKENHUB_BENCHMARK_WARMUP:-5s}"
CONCURRENCY="${TOKENHUB_BENCHMARK_CONCURRENCY:-32}"
MODEL="${TOKENHUB_BENCHMARK_MODEL:-benchmark-model}"
UPSTREAM_LATENCY="${TOKENHUB_BENCHMARK_UPSTREAM_LATENCY:-5ms}"
REPEATS="${TOKENHUB_BENCHMARK_REPEATS:-5}"
DEPLOYMENT_PROFILE="${TOKENHUB_BENCHMARK_DEPLOYMENT_PROFILE:-sqlite-audit-on-metrics-off-tracing-off}"

: "${TOKENHUB_BENCHMARK_TOKENHUB_URL:?set TOKENHUB_BENCHMARK_TOKENHUB_URL}"
: "${TOKENHUB_BENCHMARK_BIFROST_URL:?set TOKENHUB_BENCHMARK_BIFROST_URL}"
: "${TOKENHUB_BENCHMARK_TOKENHUB_API_KEY:?set TOKENHUB_BENCHMARK_TOKENHUB_API_KEY}"
: "${TOKENHUB_BENCHMARK_BIFROST_API_KEY:?set TOKENHUB_BENCHMARK_BIFROST_API_KEY}"

mkdir -p "$RESULT_DIR"

run_target() {
  local label="$1"
  local base_url="$2"
  local key_variable="$3"

  (
    cd "$ROOT_DIR/backend"
    env "$key_variable=${!key_variable}" go run ./cmd/tokenhub-benchmark run \
      --label "$label" \
      --base-url "$base_url" \
      --api-key-env "$key_variable" \
      --model "$MODEL" \
      --protocol chat \
      --mode concurrency \
      --concurrency "$CONCURRENCY" \
      --duration "$DURATION" \
      --warmup "$WARMUP" \
      --upstream-latency "$UPSTREAM_LATENCY" \
      --deployment-profile "$DEPLOYMENT_PROFILE" \
      --json "$RESULT_DIR/$label-$run_number.json" \
      --markdown "$RESULT_DIR/$label-$run_number.md"
  )
}

for ((run_number = 1; run_number <= REPEATS; run_number++)); do
  if ((run_number % 2 == 1)); then
    run_target tokenhub "$TOKENHUB_BENCHMARK_TOKENHUB_URL" TOKENHUB_BENCHMARK_TOKENHUB_API_KEY
    run_target bifrost "$TOKENHUB_BENCHMARK_BIFROST_URL" TOKENHUB_BENCHMARK_BIFROST_API_KEY
  else
    run_target bifrost "$TOKENHUB_BENCHMARK_BIFROST_URL" TOKENHUB_BENCHMARK_BIFROST_API_KEY
    run_target tokenhub "$TOKENHUB_BENCHMARK_TOKENHUB_URL" TOKENHUB_BENCHMARK_TOKENHUB_API_KEY
  fi
done

printf 'Results written to %s\n' "$RESULT_DIR"
