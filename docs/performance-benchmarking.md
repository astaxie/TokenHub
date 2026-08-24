# Performance Benchmarking

TokenHub includes two complementary benchmark layers. Black-box benchmarks exercise an OpenAI-compatible HTTP endpoint and can compare TokenHub with another gateway. In-process Go benchmarks isolate TokenHub's routing and governance costs and report allocations. Neither layer calls a real model provider.

## What the numbers mean

The black-box runner reports achieved requests per second, success rate, response bytes, P50/P95/P99 end-to-end latency, and streaming time to first byte (TTFT). It also reports **estimated gateway overhead**:

```text
max(0, end-to-end client latency - configured fake-upstream latency)
```

This estimate includes HTTP transport and scheduling noise. It is not an internal gateway timer and must not be compared directly with a benchmark that excludes JSON serialization or HTTP calls. In particular, Bifrost's published microsecond overhead is measured with specific exclusions; use the same external runner and upstream for a fair product comparison.

Every JSON result records the scenario, commit, timestamp, Go version, operating system, architecture, CPU model and count, and system memory. It deliberately excludes API keys, hostnames, usernames, and local paths.

## Build the tool

From the repository root:

```bash
mkdir -p .tmp
(cd backend && go build -o ../.tmp/tokenhub-benchmark ./cmd/tokenhub-benchmark)
```

The tool provides six commands:

- `mocker`: deterministic OpenAI-compatible Chat Completions, Responses, Embeddings, and SSE endpoints.
- `gateway`: self-contained in-memory TokenHub plus the deterministic upstream, for a zero-configuration TokenHub smoke baseline.
- `run`: fixed-concurrency or fixed-rate HTTP load with warmup and unique prompts that defeat response caching.
- `check`: scenario-compatible baseline comparison using a tolerant performance budget.
- `summarize-go`: convert repeated standard Go benchmark output into median JSON metrics.
- `check-go`: compare internal `ns/op`, `B/op`, and `allocs/op` metrics with a tracked baseline.

For a quick TokenHub-only smoke run, start the self-contained gateway in one terminal. The key is synthetic, exists only in the in-memory benchmark database, and is still passed through the environment rather than a command-line argument:

```bash
TOKENHUB_BENCHMARK_API_KEY=thk_benchmark_local \
./.tmp/tokenhub-benchmark gateway \
  --listen 127.0.0.1:18080 \
  --model benchmark-model \
  --upstream-latency 5ms
```

Then use `run` against `http://127.0.0.1:18080`. The tracked `benchmarks/baselines/tokenhub-local-smoke.json` was produced this way from the median-throughput result of five runs.

## Run the deterministic upstream

```bash
./.tmp/tokenhub-benchmark mocker \
  --listen 127.0.0.1:18081 \
  --latency 5ms \
  --response-bytes 1024 \
  --stream-chunks 8 \
  --chunk-interval 1ms
```

Configure both gateways with a route named `benchmark-model` that forwards to `http://127.0.0.1:18081/v1`. Give each gateway a local benchmark API key. The mocker can also inject deterministic failures with `--failure-every` to test failover.

## Compare TokenHub and Bifrost

Start each gateway on the same otherwise idle machine, using the same database class and telemetry settings. Avoid running both measured gateways at once when they would compete for CPU. Run each target multiple times and alternate their order.

```bash
export TOKENHUB_BENCHMARK_TOKENHUB_URL=http://127.0.0.1:8080
export TOKENHUB_BENCHMARK_BIFROST_URL=http://127.0.0.1:8082
export TOKENHUB_BENCHMARK_TOKENHUB_API_KEY=YOUR_TOKENHUB_BENCHMARK_KEY
export TOKENHUB_BENCHMARK_BIFROST_API_KEY=YOUR_BIFROST_BENCHMARK_KEY
export TOKENHUB_BENCHMARK_MODEL=benchmark-model

./benchmarks/run-comparison.sh
```

The script alternates target order across five runs and writes numbered JSON and Markdown files under `output/benchmarks/`, which is intentionally ignored by Git. Change repetitions, duration, warmup, concurrency, result directory, expected upstream latency, and the database/telemetry deployment profile with the `TOKENHUB_BENCHMARK_*` variables defined at the top of the script.

For streaming, Responses, Embeddings, or fixed-rate saturation tests, invoke the runner directly:

```bash
TOKENHUB_BENCHMARK_API_KEY=YOUR_LOCAL_BENCHMARK_KEY \
./.tmp/tokenhub-benchmark run \
  --label tokenhub-stream-c32 \
  --base-url http://127.0.0.1:8080 \
  --model benchmark-model \
  --protocol chat \
  --stream \
  --mode concurrency \
  --concurrency 32 \
  --warmup 5s \
  --duration 30s \
  --upstream-latency 13ms \
  --upstream-ttft 5ms \
  --json output/benchmarks/tokenhub-stream-c32.json \
  --markdown output/benchmarks/tokenhub-stream-c32.md
```

In fixed-rate mode, set `--mode rate --rate N --concurrency 0`. A `load_generator_saturated` failure means the client reached `--max-in-flight`; `load_generator_missed_schedule` means the client could not dispatch at the requested cadence. Neither is a gateway response, and a run containing either must not be used as a passing latency comparison.

For a streaming run, `--upstream-latency` means total upstream response duration, while `--upstream-ttft` means upstream time to first byte. With the mocker defaults, total duration is initial latency plus `stream-chunks * chunk-interval` (5 ms + 8 * 1 ms = 13 ms). Supplying both keeps full-response and TTFT overhead estimates from attributing deterministic chunk delivery to the gateway.

## Run the internal benchmark matrix

```bash
./benchmarks/run-internal.sh
```

The matrix covers Chat Completions, Responses, streaming, failover, SQLite, isolated payload-audit persistence with identical 32 KiB controls, direct small/large audit rendering, metrics, and tracing with and without payload capture. It uses `ReportAllocs`, so standard Go output includes `ns/op`, `B/op`, and `allocs/op`. The script aggregates repeated samples by median and exits non-zero when any metric exceeds `benchmarks/internal-budget.json` (25% time, 15% bytes, or 10% allocations over baseline).

PostgreSQL is opt-in to prevent ordinary test runs from accessing a shared service. Each benchmark case runs in a transaction that is rolled back, so repeated cases do not retain routes or audit rows:

```bash
TOKENHUB_BENCHMARK_POSTGRES_URL='postgres://tokenhub:password@127.0.0.1:5432/tokenhub_benchmark?sslmode=disable' \
./benchmarks/run-internal.sh
```

Use a disposable empty database. Setup and migrations are outside the timed interval, while request persistence is included.

Internal baselines are profile-specific: the default uses `benchmarks/baselines/internal/sqlite.json`, while a run with `TOKENHUB_BENCHMARK_POSTGRES_URL` uses `sqlite-postgresql.json`. Generate or intentionally refresh one only from a clean committed revision:

```bash
TOKENHUB_BENCHMARK_UPDATE_BASELINE=1 ./benchmarks/run-internal.sh
```

Set the PostgreSQL URL as well when updating that profile. Baseline checks fail closed when the benchmark set, Go version, OS, architecture, CPU model/count, or system memory differs.

## Check and update a baseline

The tracked budget in `benchmarks/budget.json` requires at least 99.9% success, at least 90% of baseline throughput, no more than 15% mean-latency regression, and no more than 20% P99 regression. These tolerances reduce false failures from shared-runner jitter while still exposing material changes. Tracked local baselines cover Chat, Responses, streaming, and invoked failover.

```bash
./.tmp/tokenhub-benchmark check \
  --baseline benchmarks/baselines/tokenhub-local-smoke.json \
  --current output/benchmarks/tokenhub-local-smoke.json \
  --budget benchmarks/budget.json \
  --markdown output/benchmarks/budget-check.md
```

The check refuses results whose schema, protocol, stream mode, load mode, load level, model, request size, configured upstream latency, or runtime/hardware profile differs. Update a tracked baseline only after an intentional performance change, using a stable machine and at least five repeated runs. Keep the median run, describe the hardware and command in the pull request, and never add credentials or ad hoc local output.

To check all four scenarios, place current results with the same filenames as `benchmarks/baselines/*.json` in one directory, then run:

```bash
./benchmarks/check-suite.sh output/benchmarks/current
```

The scenario fingerprint also includes duration, warmup, timeout, maximum in-flight requests, response size, stream shape, and a deployment profile describing database and telemetry settings. The comparison fails closed when any of these differ or when the load generator drops offered requests.

## Interpreting a comparison

Treat a run as invalid if success rate differs materially, the load generator saturates, either gateway retries a different number of times, or CPU and memory pressure differ. Compare throughput at fixed concurrency, latency at fixed offered RPS, streaming TTFT, and allocations separately. A single maximum-throughput number does not explain the cost of TokenHub's persistence, audit, routing, metrics, and tracing guarantees.
