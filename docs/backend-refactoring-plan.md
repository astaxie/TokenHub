# Backend Refactoring Plan

This document records the staged migration of `backend/internal/server` into
smaller domain, transport, and persistence modules. It is the forward-looking
plan for issue #169. The architecture document describes the current runtime
architecture only.

## Planning rules

- Preserve existing HTTP, authentication, authorization, persistence, and
  transaction behavior at every phase.
- Define persistence ports in the domain or application package that consumes
  them. Keep GORM rows, HTTP DTOs, and database concerns in adapters.
- Keep the dependency direction explicit. Domain packages must not import
  `server`, HTTP handlers, or GORM.
- Record the dependency changes, compatibility seams, remaining work, and
  validation evidence for each phase before starting the next phase.

## Migration status

This summary shows the dependency transition, status, exit condition, and
remaining work for each phase without exposing internal task identifiers.

| Phase | Dependency transition | Status | Exit condition | Remaining work |
| --- | --- | --- | --- | --- |
| 0. Baseline and constraints | Freeze observable behavior before introducing new package edges; add dependency guards only after the target directories stabilize | Complete, with dependency guards deferred | The characterization matrix is complete and the guard design is ready for a separate PR | Add the guard after the final package layout is stable; do not present it as an active repository gate before then |
| 1. Business modules | Move billing and reconciliation callers from `server.Store` to consumer-owned ports; then introduce analytics ports and adapters | In progress | Billing, reconciliation, and analytics production paths use their public interfaces; no business implementation remains in `server` | Complete analytics extraction, remove obsolete server projections, and migrate remaining tests |
| 2. Gateway, protocols, and providers | Change `server` from protocol/provider implementation to composition over `gateway`, `protocol/*`, and `provider` | Planned | OpenAI, Anthropic, and Gemini protocol adapters plus provider invocation run behind the normalized gateway contract | Define the contract, then move provider management, invocation, protocol adapters, and gateway error mapping |
| 3. Projects and routing | Move authentication, authorization, quota admission, candidate planning, and routing policy behind `project` and `routing` ports | Planned | Gateway no longer uses the global `Store` for identity, admission, or candidate planning | Separate project and API-key state, quota semantics, routing policy, and failover planning while preserving admission order |
| 4. Request lifecycle and background capabilities | Move request records, usage, audit, payload retention, image jobs, and observability behind `usage`, `image`, and `observability` modules | Planned | `Server` no longer owns request or worker domain state; lifecycle and recovery behavior remain unchanged | Extract usage writes and audit semantics before moving gateway orchestration, image workers, and telemetry adapters |
| 5. Adapter and composition cleanup | Consolidate HTTP adapters and persistence implementations so `server` only assembles dependencies, routes requests, and owns process lifecycle | Planned | Compatibility constructors, obsolete Store projections, and migration shims are removed; the final dependency graph is enforced | Move remaining admin and persistence adapters, add dependency guards, and verify the complete architecture against issue #169 |

The phases are ordered dependencies, not independent parallel features. Phase 1
must finish the analytics boundary before Phase 2 changes the request path;
Phase 3 depends on the gateway contract; Phase 4 depends on the project,
routing, and usage seams; and Phase 5 removes shims only after all production
callers have moved.

## Completed work in phase 1

### Billing

The billing domain and its adapters were extracted in the preceding PRs. The
domain owns synchronization, retry, cursor, and scheduler behavior. The
remaining server code is a composition seam for callers that have not moved
to the public billing interfaces yet.

### Reconciliation adapters

The current phase moves reconciliation persistence and administrative HTTP
handling out of `server`.

The resulting dependency direction is:

```text
server -> admin -> reconciliation
server -> reconciliation/persistence -> reconciliation
```

The reconciliation domain owns its `Store` and `BillingReader` ports. The
`admin` package owns request, response, CSV, and audit transport mapping.
`reconciliation/persistence` owns GORM rows, transactions, cost evidence, and
the billing projection adapter for SQLite and PostgreSQL. The composition root
still opens the database, supplies the shared persistence mutex and audit
callbacks, and performs route authorization.

The compatibility contract includes the existing HTTP responses and errors,
table names and migrations, attribution snapshots, explicit zero-cost
evidence, pagination, CSV escaping, locking, recalculation, and audit
behavior. The PostgreSQL CI job executes the tagged production-adapter
contract test for concurrent snapshot backfill.

Remaining work in this phase is to migrate the remaining server tests to the
public module boundaries and remove compatibility constructors and test
projections after their callers have moved.

## Next phase: analytics

The next phase is split into two reviewable steps:

1. Extract the analytics query and application module, including analytics
   credentials, cursor and snapshot semantics, aggregation, and consumer-owned
   persistence ports.
2. Move the SQLite/PostgreSQL query adapters and administrative HTTP adapter,
   then remove the server analytics implementation while preserving its
   aggregation, authorization, and consistency behavior.

The phase exits when analytics production paths use the new public interfaces,
the adapters run on both SQLite and PostgreSQL, and no production analytics
caller depends on the global server `Store`.

The analytics dependency transition is:

```text
server -> admin -> analytics
server -> analytics/persistence -> analytics
analytics -> usage (read-only usage projections)
```

The phase must preserve credential scope, cursor and watermark semantics,
snapshot consistency, aggregation boundaries, authorization, pagination, and
query limits. Its adapter work is complete only when both SQLite and
PostgreSQL paths have focused coverage and the existing HTTP responses remain
compatible.

## Later phases

### Gateway, protocols, and providers

The gateway phase introduces a protocol-neutral request and result contract.
`protocol/openai`, `protocol/anthropic`, and `protocol/gemini` translate HTTP
representations into that contract, while `provider` owns capability checks,
credentials, upstream transport, health, and error normalization. The gateway
must depend on routing and project ports rather than concrete protocol or
database types. The phase is complete when the three supported protocol paths
and provider invocation have independent contract tests and `server` only
registers them.

### Projects and routing

The project phase moves API-key authentication, project authorization, RBAC,
quota admission, concurrency, candidate planning, weights, affinity, failover,
and breaker decisions behind `project` and `routing`. The dependency change is
from gateway code reading the global Store to gateway consuming narrow project
and routing ports. The phase must preserve authentication order, rejection
status and headers, quota reservation/settlement atomicity, candidate order,
and the rule that committed provider calls are not retried.

### Usage, workers, and observability

The lifecycle phase extracts usage, request logs, audit, payload retention,
image jobs, response workers, tracing, metrics, and structured logging. Gateway
orchestration calls these modules through commands and projections; it does not
own their rows or worker state. The exit condition is unchanged usage and audit
output, lease and fencing behavior, recovery of abandoned jobs, shutdown
ordering, and observability labels with no domain state stored directly on
`Server`.

### Final adapter and composition cleanup

The final phase moves any remaining admin and persistence adapters, then turns
the dependency rules into an enforceable guard. Compatibility constructors and
test projections are removed only after their last production or
characterization caller has migrated. The final review must show the target
package graph, passing repository gates, backend tests, and SQLite/PostgreSQL
integration coverage for every persistence boundary.

Each phase retains the same compatibility contract, records its dependency
changes and remaining work in this document, and adds focused SQLite and
PostgreSQL coverage whenever database behavior is part of the boundary.
