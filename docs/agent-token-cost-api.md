# Agent Token Cost API

Language: English | [简体中文](zh-CN/agent-token-cost-api.md) | [日本語](ja/agent-token-cost-api.md)

The versioned Agent Token Cost API lets a local reporting or monitoring agent read TokenHub usage without an administrator session, model-invocation API key, Provider credential, or manual export. The API is read-only and uses the same request, token, error, and estimated customer-cost records as the administrator usage view.

## Endpoints

| Endpoint | Authentication | Purpose |
| --- | --- | --- |
| `GET /api/v1/analytics/token-costs` | Analytics credential | Query request-level or aggregated token costs as JSON or CSV |
| `GET /api/admin/analytics/credentials` | Platform administrator | List analytics credential metadata |
| `POST /api/admin/analytics/credentials` | Platform administrator | Create an analytics credential and reveal its token once |
| `DELETE /api/admin/analytics/credentials/{id}` | Platform administrator | Revoke an analytics credential immediately |

Analytics credentials begin with `tha_`. They cannot authenticate to `/v1/models`, model inference endpoints, or administrator endpoints.

## Create a least-privilege credential

Use an administrator session or the configured administrator token to create a project-scoped credential:

```bash
curl -sS https://tokenhub.example.com/api/admin/analytics/credentials \
  -H "Authorization: Bearer $TOKENHUB_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "payments-cost-agent",
    "scope_type": "project",
    "project_id": "prj_payments",
    "expires_at": "2026-12-31T00:00:00Z"
  }'
```

The response includes `credential` metadata and a `token`. Copy the token immediately; later list responses expose only its prefix and suffix. Set `scope_type` to `organization` and omit `project_id` only when the agent must read the entire TokenHub instance. An expiry is optional but recommended.

Store the token in the local agent's secret store:

```bash
export TOKENHUB_ANALYTICS_TOKEN='tha_REPLACE_ME'
```

Revoke a credential when the agent is retired or its token may have been exposed:

```bash
curl -sS -X DELETE \
  https://tokenhub.example.com/api/admin/analytics/credentials/acred_REPLACE_ME \
  -H "Authorization: Bearer $TOKENHUB_ADMIN_TOKEN"
```

## Query request-level costs

`from` is inclusive, `to` is exclusive, and both use RFC 3339. Omitting them selects the previous 24 hours up to the start of the query.

```bash
curl -sS -G https://tokenhub.example.com/api/v1/analytics/token-costs \
  -H "Authorization: Bearer $TOKENHUB_ANALYTICS_TOKEN" \
  --data-urlencode 'from=2026-08-01T00:00:00Z' \
  --data-urlencode 'to=2026-08-02T00:00:00Z' \
  --data-urlencode 'provider_id=prv_openai' \
  --data-urlencode 'model=gpt-4.1-mini' \
  --data-urlencode 'status=success' \
  --data-urlencode 'limit=100'
```

The default `granularity=request` returns one sanitized row per gateway request. A row contains stable IDs and metrics, but never the presented analytics token, an API key secret, a Provider credential, Provider cost, client IP, request payload, response payload, or User-Agent.

## Filter and aggregate

The following example produces daily totals by project, Provider, model, and success/error status:

```bash
curl -sS -G https://tokenhub.example.com/api/v1/analytics/token-costs \
  -H "Authorization: Bearer $TOKENHUB_ANALYTICS_TOKEN" \
  --data-urlencode 'from=2026-07-01T00:00:00Z' \
  --data-urlencode 'to=2026-08-01T00:00:00Z' \
  --data-urlencode 'granularity=day' \
  --data-urlencode 'group_by=project,provider,model,status'
```

Use `granularity=hour`, `day`, or `month` for time buckets. Use `granularity=none` for totals without a time bucket. Supplying `group_by` without `granularity` also selects `none`. `request` cannot be combined with `group_by`.

| Parameter | Values and behavior |
| --- | --- |
| `from`, `to` | RFC 3339 `[from, to)` interval; defaults to the previous 24 hours |
| `project_id` | Exact Project ID; a project credential is always forced to its own Project and receives `403` for another Project |
| `user_id` | Exact attributed user ID |
| `api_key_id` | Exact stored API Key ID, never the API Key secret |
| `provider_id` | Exact Provider ID |
| `model` | Exact external model name |
| `status` | `success` for HTTP status below 400, or `error` for status 400 and above |
| `granularity` | `request` (default), `none`, `hour`, `day`, or `month` |
| `group_by` | Comma-separated or repeated `project`, `user`, `api_key`, `provider`, `model`, and `status` |
| `limit` | 1–1000 rows; defaults to 100 |
| `cursor` | Opaque `next_cursor` from the preceding page |
| `after` | Opaque committed `watermark` for a commit-sequence incremental pull; cannot be combined with `from` or `cursor` |
| `format` | `json` (default) or `csv`; `Accept: text/csv` also selects CSV |

Initial request-level snapshots are limited to 31 days and initial aggregated snapshots to 366 days. Incremental change pulls are bounded by commit sequence, so they may keep the original `from` while advancing `to` beyond those limits without rescanning the original history.

## JSON schema

Every JSON response declares `schema_version: "1.0"` and has this shape:

```json
{
  "schema_version": "1.0",
  "object": "token_cost.list",
  "generated_at": "2026-08-02T00:00:01Z",
  "query": {
    "from": "2026-08-01T00:00:00Z",
    "to": "2026-08-02T00:00:00Z",
    "granularity": "day",
    "group_by": ["project", "model"],
    "filters": {"project_id": "prj_payments"},
    "format": "json",
    "limit": 100,
    "dedupe_by": "dedupe_key",
    "checkpoint_by": "commit_sequence",
    "incremental_mode": "snapshot"
  },
  "data": [
    {
      "dedupe_key": "aggregate_f6d6...",
      "bucket": "2026-08-01",
      "project_id": "prj_payments",
      "model": "gpt-4.1-mini",
      "metrics": {
        "request_count": 42,
        "error_count": 2,
        "input_tokens": 120000,
        "cached_input_tokens": 35000,
        "cache_write_input_tokens": 4000,
        "output_tokens": 18000,
        "reasoning_output_tokens": 2500,
        "total_tokens": 138000,
        "estimated_cost_usd": 1.73
      }
    }
  ],
  "has_more": false,
  "watermark": "OPAQUE_WATERMARK"
}
```

`request_count` and `error_count` come from gateway request logs, including failed requests that have no usage row. Token and cost metrics come from usage records. Cached and reasoning counts are details already represented in the input/output totals; do not add every detail field to `total_tokens`. `estimated_cost_usd` is the configured external customer charge used by the administrator usage view, not TokenHub's confidential Provider cost.

## Pagination and incremental pulls

When `has_more` is true, call the endpoint again with `cursor=next_cursor`. The cursor retains the original filters, `granularity`, `group_by`, and snapshot interval, so those parameters may be omitted. If they are supplied, they must match the cursor. The snapshot's upper bound remains fixed, so requests arriving during pagination do not shift later pages.

The response `watermark` identifies a completed database snapshot. On PostgreSQL, new request logs combine their transaction ID with a persistent offset, while an upgrade assigns frozen history distinct checkpoint values ordered by event time. The history rewrite uses MVCC and does not take a table lock that stops new request-log inserts. A watermark never advances beyond the greatest visible request-log sequence; after `pg_restore`, startup rebases the persistent offset above that restored maximum, so existing Agent watermarks remain valid in the new cluster. The checkpoint also stops before the snapshot's oldest active transaction, so later transactions can commit without waiting on a shared analytics row. SQLite uses its transactionally updated sequence within the database's existing single-writer model. The checkpoint stops before the first matching committed request whose `occurred_at` is at or beyond `to`, so advancing `to` cannot skip an already committed future event. A watermark is returned even when the snapshot has no matching rows. Drain every page until `has_more` is false, process the rows successfully, and only then commit the watermark in the agent's durable state. Start the next run with `after=<committed watermark>`:

```bash
curl -sS -G https://tokenhub.example.com/api/v1/analytics/token-costs \
  -H "Authorization: Bearer $TOKENHUB_ANALYTICS_TOKEN" \
  --data-urlencode "after=$TOKENHUB_COST_WATERMARK"
```

An `after` pull sets `query.incremental_mode` to `changes` and returns only request logs whose commit sequence is greater than the committed watermark and no greater than the new snapshot. Its original filters and event-time `from` are retained, while `to` may advance. A request that commits late is therefore returned regardless of whether its `occurred_at` is earlier than the previous watermark's newest event.

Request-granularity changes use `request_id` as `dedupe_key`. Aggregated change rows are deltas over newly committed requests; their key identifies the committed starting checkpoint, query shape, bucket, and dimensions. Stage or upsert every page by that key, apply the completed delta exactly once, and advance the watermark atomically. Retrying the same committed watermark keeps the same keys and replaces staged values, even if the newer snapshot has advanced. Changing filters or aggregation shape while reusing a watermark is rejected; start a differently shaped report with a new `from` and `to` snapshot.

## CSV export

```bash
curl -sS -G https://tokenhub.example.com/api/v1/analytics/token-costs \
  -H "Authorization: Bearer $TOKENHUB_ANALYTICS_TOKEN" \
  -H 'Accept: text/csv' \
  --data-urlencode 'from=2026-08-01T00:00:00Z' \
  --data-urlencode 'to=2026-08-02T00:00:00Z' \
  --data-urlencode 'granularity=hour' \
  --data-urlencode 'group_by=project,provider,model' \
  -o token-costs.csv
```

CSV uses the same filters, limits, metrics, pagination, and `dedupe_key` as JSON. Metadata is returned in `X-TokenHub-Schema-Version`, `X-TokenHub-Has-More`, `X-TokenHub-Next-Cursor`, `X-TokenHub-Watermark`, `X-TokenHub-Dedupe-By`, `X-TokenHub-Checkpoint-By`, and `X-TokenHub-Incremental-Mode` headers. Text cells that could be interpreted as spreadsheet formulas are prefixed with an apostrophe.

## CLI and MCP assessment

| Option | Decision | Reason |
| --- | --- | --- |
| Versioned HTTP + CSV | Supported now | Works from any language, cron runner, or Agent runtime; has no extra binary distribution; keeps authentication and checkpoints explicit |
| Dedicated CLI | Deferred | A CLI would mainly wrap HTTP while adding installation, upgrade, and local secret-configuration work; reconsider when interactive credential setup or scheduled report packaging is requested |
| MCP server/tool | Deferred | MCP would add another long-running trust boundary and host-specific deployment; reconsider when Agent hosts need tool discovery or shared checkpoint management rather than direct HTTP |

Any future CLI or MCP adapter must accept only an analytics credential, preserve Cursor/watermark semantics, expose the same Schema version, and never request an administrator session or model-invocation API Key.

## Security and operations

- Only platform administrators can create, list, or revoke analytics credentials.
- Every successful query, rejected scope request, invalid query, and invalid credential attempt writes an audit event of type `token_cost_analytics`.
- Analytics credentials and their hashes are excluded from query responses and audit snapshots. Store the one-time token as a secret.
- Analytics reads use a dedicated small connection pool, separate from the gateway's core pool. File-backed SQLite enables WAL so readers do not hold up gateway writes; PostgreSQL uses an independent analytics pool. Every query has a 10-second execution deadline in addition to indexed `created_at` and `(project_id, created_at)` paths, time-range limits, and page-size limits.
- Revocation takes effect on the next request. Rotate a credential by creating a replacement, updating the agent, and revoking the old credential.
- Use pagination instead of parallel unbounded history pulls. A timed-out query returns `503 analytics_query_timeout`; reduce the time range or grouping cardinality before retrying.
