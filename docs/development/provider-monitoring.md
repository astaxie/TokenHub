# Provider monitoring performance

## Problem and scope

The Providers page previously fetched all observations from the last 30 days on every monitoring request, copied and sorted them in Go, and waited for monitoring before displaying the provider list. Issue #425 reports 1,770,175 retained observations and two OOM terminations; the available evidence does not establish that this endpoint caused those terminations.

## Repair design

1. Aggregate observations in SQL by provider, source and UTC day. Return at most 32 buckets per provider/source, including the partial oldest day and one bounded future bucket. Only the currently configured providers and the two supported observation sources participate.
2. Select one latest sample and the exact lower median of successful positive latencies for each provider/source. Keep the rolling 24-hour signal window, 30 UTC calendar-day trend, stale/no-sample states, status thresholds, and cached quota behavior. Equal latest timestamps use descending observation ID as a deterministic tie-breaker.
3. Execute all aggregate, latest and median reads in one read-only transaction (repeatable read on PostgreSQL) to avoid mixing observations inserted during a refresh.
4. Cache observation summaries for 15 seconds per backend process. Concurrent requests with the same provider membership share one refresh. Provider configuration, resources, routes and quota snapshots remain freshly loaded. Provider membership changes bypass the old cache; failures are never cached.
5. Bound each shared refresh to 10 seconds. A canceled HTTP request stops waiting immediately; cancellation of the last waiter also cancels database work. Other waiters can continue when one caller disconnects. Database errors/timeouts return a sanitized HTTP 503 instead of a misleading empty success.
6. Load monitoring separately in the console. The provider list and actions become available while monitoring is pending. A monitoring failure preserves the list and reports the error. Starting another load, leaving the view, changing the session/backend, or unmounting aborts the obsolete monitoring request; aborted responses cannot overwrite current data.

## Cost and operational limits

Application result size and allocations depend on provider count rather than retained observation count. SQL aggregation and exact median selection still scan/sort qualifying history, so database work is not constant as traffic grows. The deadline, cache and refresh coalescing bound individual work duration and repeated demand; replicas each maintain their own cache. A timeout leaves monitoring unavailable while the base list remains usable. Persistent database pressure may require separately designed incremental rollups or indexes; no approximate percentile or arbitrary row limit is introduced here.

Monitoring observations may be up to 15 seconds stale after a completed refresh. No schema migration, environment variable, deployment change, or `/v1` contract change is required. Rollback uses the previous application version.

## Validation

- SQLite and PostgreSQL parity against the previous in-memory calculations, including UTC boundaries, stale samples, unknown sources, future timestamps, failures and even-sized median populations.
- A large synthetic history verifies bounded returned rows; benchmarks report time and allocations for 10,000 and 100,000 observations.
- Cache reuse, expiry, provider membership, concurrent refreshes, cancellation, error retries, authorization and sanitized failure responses.
- Named browser fixtures `providers independent-monitoring-success` and `providers independent-monitoring-failure` verify that delayed or failed monitoring cannot block the provider list and that successful monitoring updates it afterward.
- The `providers cancels-obsolete-monitoring` fixture verifies that navigation cancels the old request and returning to the page keeps the new monitoring state.
