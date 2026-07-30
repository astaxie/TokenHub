# Gateway trace export (OTLP / Langfuse v4)

Export one OpenTelemetry trace per gateway call so operators can inspect prompts,
token usage, cost, routing and failover in Langfuse — or any other OTLP/HTTP backend.

## Design decisions

**Generic OTLP, not a Langfuse client.** Langfuse is a configured endpoint plus an
attribute mapping. The exporter itself is backend agnostic, so the same build also
targets Jaeger, Phoenix or an OTel Collector. No Collector is required for Langfuse:
Langfuse accepts OTLP over HTTP directly, and the Collector in comparable gateway
integrations exists only to bridge gRPC, which this exporter does not speak.

**Target Langfuse v4.** v4 is generally available for self-hosted deployments and is
the default for Langfuse Cloud organizations created after 2026-04-14. Three v4 rules
shape the mapping and are not optional:

- Trace-level input/output is deprecated. Overall input/output belongs on the root
  observation.
- Filterable attributes must be present on *every* span. An attribute set only on the
  root is unavailable when filtering or aggregating that root's children.
- Re-ingesting the same span ID creates duplicate observations and inflates metrics.
  Each gateway call must therefore export exactly once.

**Usage and cost live on the generation children, never on the root.** v4 aggregates
over observations, so carrying the same usage on both the root and its children would
double-count every request.

**Payload capture is a separate switch from tracing, and both default to off.**
Sending prompts to an external system is a data-egress decision distinct from wanting
latency and cost telemetry. With payload capture off a trace still carries status,
latency, provider selection, failover, tokens, cost, transport and upstream request
IDs. Upstream error text is treated as payload for the same reason: it can embed
response bodies, URLs and account identifiers.

## Stage 1: Completion contract

**Goal**: A single request-independent description of a finished gateway call, shared
by persistence, metrics and tracing, with usage priced exactly once.

`priceUsage` currently runs inside `GormStore.FinishCall` and its result never leaves
that function, so any caller emitting telemetry afterwards holds an unpriced `Usage`
with `CostUSD == 0`. `priceUsage` is pure and idempotent, so the fix is to price at
the boundary and let the store's own call become a no-op.

The completion sites are not confined to `http.go`: `FinishCall`, `CompleteImageJob`,
`RecordPlaygroundRequest` and `RecordRejectedRequest` together account for dozens of
call sites across `http.go`, `anthropic_messages.go`, `provider_account_codex.go` and
`image_generation.go`. Image jobs finish asynchronously from `s.imageContext`, so the
contract must not carry an `*http.Request`.

**Success Criteria**
- `GatewayCallCompletion` carries call, route, priced usage, attempts, status, error
  code, client IP, user agent, payload candidates and a completion kind.
- Routed handlers in `http.go` and `anthropic_messages.go` funnel through one helper.
- `TraceEmitter` is a narrow interface with a no-op default; nothing else changes when
  tracing is off.
- Every routed path emits exactly one completion.

**Tests**
- A recording emitter asserts exactly one completion per handler, for success, error,
  streaming, and rejected paths.
- The completion's `Usage.CostUSD` is non-zero for a priced model, proving the
  pricing moved ahead of the emission point.
- `priceUsage` applied twice equals `priceUsage` applied once.

**Status**: Complete

## Stage 2: Per-attempt usage

**Goal**: Make failover legible. `RouteAttempt` records status, error, invocation and
latency but discards the `Usage` that `executeRoutedWithStore` already receives from
each candidate, so per-candidate tokens and cost cannot be reported and a failed
attempt's partial usage is lost.

**Success Criteria**
- `RouteAttempt` carries usage, start and end timestamps, and the served model.
- A failed attempt keeps whatever usage the upstream reported before failing.
- `RouteAttemptLog` persists the added fields; existing rows migrate without loss.

**Tests**
- A two-candidate failover run reports usage on both attempts, not just the winner.
- Attempt timestamps are ordered and fall inside the call's own window.

**Status**: Complete

## Stage 3: OTLP exporter

**Goal**: Ship spans to an OTLP/HTTP endpoint without ever affecting a gateway request.

Uses the OpenTelemetry Go SDK with a TokenHub-owned `TracerProvider`; the process
global is left alone so tests stay isolated and an embedding binary keeps its own
tracing. The batch processor is non-blocking: under pressure spans are dropped, never
queued into request latency.

**Success Criteria**
- `TOKENHUB_TRACING_*` configuration, off by default, validated fail-closed at
  startup: a malformed endpoint, header, sample ratio, queue size or timeout stops
  the process instead of silently disabling export.
- Header parsing splits on the first `=`, rejects CR/LF, and the header value is
  never logged.
- Export runs on a context detached from request cancellation.
- Export failures and dropped spans surface as Prometheus counters.
- Shutdown flushes after the HTTP server and image workers stop producing spans,
  within a reserved slice of the graceful shutdown budget.

**Tests**
- An in-process fake OTLP endpoint receives the spans; assertions run against the
  decoded protobuf.
- Configuration validation rejects each malformed input.
- A blocked endpoint does not delay a gateway request.
- A cancelled request context still exports.

**Status**: Complete

## Stage 4: Langfuse v4 mapping

**Goal**: One root span plus one generation child per invoked attempt, shaped so
Langfuse v4 renders it correctly.

Identity is deliberately split into four fields, because the existing signals mean
different things: `usageAttributionUserID` resolves to the API key owner or project
owner and is an administrative identity, while the cache-locality affinity hash is a
tenant-scoped routing key that may or may not correspond to a session.

Route candidates that were never invoked become events on the root, so a routing
rejection is visible without inventing an observation that never called a provider.

**Success Criteria**
- Root: `langfuse.observation.type=span`, overall input/output when payload capture
  is on, no usage and no cost.
- Child per invoked attempt: `langfuse.observation.type=generation`,
  `gen_ai.provider.name` (`gen_ai.system` is deprecated), request and response model,
  its own usage and cost.
- Trace-level attributes are copied onto every span.
- One canonical usage mapping. TokenHub's prompt tokens include cached and
  cache-write tokens; Langfuse `usage_details` buckets must be mutually exclusive, so
  the mapping subtracts rather than emitting both forms.
- Rejected requests are traced. Playground traffic is traced with a distinguishing
  tag so it can be excluded from cost analysis.

**Tests**
- Golden attribute assertions for success, failover, rejected and playground calls.
- The sum of the usage detail buckets equals the total token count.
- Payload capture off leaves input, output and error text absent.

**Status**: Complete

## Stage 5: Configuration surface and documentation

**Goal**: The new variables satisfy the repository's environment contract, and
operators can follow a working setup.

**Success Criteria**
- `backend/.env.example`, all `deploy/docker-compose*.yml` and `docs/deployment.md`
  agree; `node tools/check-env-contract.mjs` passes.
- English, Simplified Chinese and Japanese documentation stay in sync.
- The guide states the Langfuse v4 self-hosting prerequisite of ClickHouse >= 25.12
  and explains why Langfuse is not bundled into TokenHub's own Compose files.

**Tests**
- `node tools/check-env-contract.mjs`, `node tools/check-doc-translations.mjs`,
  `node tools/check-source-lines.mjs`, `node --test tools/*.test.mjs`.

**Status**: Complete

## Out of scope

**Image job tracing.** Image work completes asynchronously through `CompleteImageJob`
rather than `FinishCall`, starts from `s.imageContext` rather than a request context,
and can roll back a completion and then finish again — which under v4 would produce
duplicate observations and inflated metrics. It needs its own idempotency design and
is deliberately left for a follow-up.

**Streaming response capture.** `streamWriteTracker` passes bytes straight through to
the client and never accumulates them, so streamed calls carry input, usage and
latency but no output text, matching what the payload log already records. A general
SSE accumulator has to understand the OpenAI, Anthropic and Gemini delta formats
separately and belongs in its own change.
