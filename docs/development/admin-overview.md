# Admin overview and alert history

The overview returns metadata counts, projects and provider configuration, and the latest 20 alerts. Full model definitions are loaded by the pages that use them. Console model search requests at most 20 results through `GET /api/admin/model-search?q=...`.

## Historical statistics

The lifetime usage and request totals in `GET /api/admin/overview` come from `overview_totals`. Global totals occupy two fixed rows. Lifetime breakdowns read the same projection, including model, provider and resource dimensions, and load independently of overview metadata. Scoped totals retain the project, API key and attributed-user tuple so overlapping OR permissions count each record once. The separate `GET /api/admin/usage/summary` endpoint keeps its existing SQL aggregates. Range-based usage reports and timeseries keep their existing date windows and analytics queries.

Schema expansions 12 (SQLite) and 13 (PostgreSQL) add the totals, projection marker columns, indexes and database triggers. Inserts, relevant updates and deletes maintain the projection in the same database transaction, including writes made outside the current Go process. Each source row is projected once. Changes to projected rows subtract the previous contribution and add the new contribution.

Existing rows are processed online in bounded batches through the data-backfill ledger. The indexed pending marker is the durable cursor. Each batch groups its contributions, marks the covered rows and records processed counts atomically. Cluster leases coordinate replicas. Shutdown cancels work and releases owned leases. The existing scheduler starts this worker alongside other background jobs. PostgreSQL migrations 14–17 build the large source indexes independently and concurrently, with retry and exact-definition postconditions. Shared migration 18 adds the durable completion state so completed projections avoid pending-history probes.

During backfill the overview remains usable and `summary_status.state` is `preparing`. Global operators see a processed-record count. Scoped users see the preparation state without global counts. The UI refreshes every three seconds. Complete totals become visible when both sources are covered. Fresh databases can serve complete statistics immediately.

Successful summaries are cached for 15 seconds per effective permission scope. Concurrent refreshes share a query. Cancellation detaches an individual waiter and the last waiter cancels the shared work. Distinct cached scopes are capped at 128. Queries have a 10-second deadline. On refresh failure a successful result up to 60 seconds old may be returned as `stale`, with its computation time. Older results are replaced by an independent summary error state. Other page data remains usable.

## Alert history

`GET /api/admin/alerts` returns `data`, `page`, `page_size` and `total`. The default page size is 50 and the enforced maximum is 100. It accepts `project_id`, `severity`, `from` and `before`. Times use RFC 3339. `from` is inclusive and `before` is exclusive. Ordering is descending by creation time and ID.

The console history view owns server pagination and aborts obsolete reads. It displays the filtered total independently from the current page length. Project quota-event counts and latest occurrence times are queried separately through `GET /api/admin/project-quota-events`, using full retained history for visible projects. Global operators receive combined request-log and alert counts; scoped users receive request-log counts within their effective permissions.

## Verification

Backend regression tests cover bounded responses, filters, overlapping scopes, corrections, deletes, resumable batches, cancellation and stale-cache limits. The PostgreSQL projection test also runs in CI. UI scenarios under `overview-bounds` cover ready, preparing and failure states, model search and paged alerts.

The opt-in `TestOverviewRepresentativeScale` and `TestPostgresOverviewRepresentativeScale` prepare three million usage rows, three million request rows, 100,000 alerts and 300 models. They check cold and warm caches with 10 concurrent callers, record p95 and allocated bytes per request, and require p95 below one second after backfill. Set `TOKENHUB_OVERVIEW_SCALE=1`; the PostgreSQL test also requires `TEST_POSTGRES_URL` and the `integration` build tag. Usage and request timestamps are fixed at 2026-01-01, so the current recent-trend window is empty. These results cover historical projection reads and the empty recent-trend query, rather than populated recent traffic or live write throughput. Results describe their fixed test environment. They do not establish production peak RSS or the cause of a previous OOM.

SQLite monetary projections use compensated summation to retain low-order contributions. SQLite falls back to the original SQL aggregates when integer range or monetary precision is at risk. This exceptional numeric path scans history while retaining the cache and query deadline, preserving source records and corrected totals. PostgreSQL totals use NUMERIC.

The shipped v0.11.0 runner rejects newer migration ledger entries. Coordinate a compatible bridge release before relying on application rollback after deployment. Update the admin frontend together with this backend.
