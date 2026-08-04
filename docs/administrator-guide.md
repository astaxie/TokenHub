# Administrator Guide

Language: English | [简体中文](zh-CN/administrator-guide.md) | [日本語](ja/administrator-guide.md)

This guide is for platform administrators, security operators, and infrastructure owners who run TokenHub as an enterprise AI gateway.

## Administrator Scope

| Area | Responsibility |
| --- | --- |
| Provider Channels | Configure upstream connections, import model inventory, and maintain actual Provider costs |
| Model Directory | Choose from the built-in model catalog, create external API models, select initial Provider routes, and set unified client-facing prices |
| Routing Policies | Fine-tune Provider mappings, priority, weight, project scope, and failover strategy |
| Projects and Teams | Define ownership boundaries for keys, quota, and cost attribution |
| Identity Sources | Configure OAuth or OIDC login providers for enterprise sign-in |
| Security and Audit | Review request logs, admin events, key rotation, and policy changes |

## Production Setup Order

1. Configure at least one identity source and keep a controlled administrator account.
2. Add an upstream Provider such as `OpenAI Production`, `Azure East US`, or `Internal Model Gateway`. Before saving, use **Test Connection** to validate its Base URL and API Key and review the measured response latency, then import the upstream models that it can serve.
3. Record each imported Provider model's actual input, cache-read, and output costs for audit.
4. Create the external models to expose to applications and set their unified client-facing prices.
5. Add routes from each external model to one or more imported Provider models.
6. Create teams, projects, cost centers, and default quota policies.
7. Validate the flow with Model Playground and request logs.
8. Review usage attribution before issuing keys broadly.

## Model Playground Diagnostics

Open **Model Playground** from the console to validate a model through the same routing and Provider adapters used by gateway traffic. Every assistant turn keeps its own compact diagnostic summary: delivery mode, gateway-measured time to first token (TTFT), output throughput, total duration, full-context input tokens, output tokens, estimated cost, local completion time, and request ID. Expand **Diagnostics** to inspect millisecond timestamps plus the actual response details. Sessions remain in the current browser page unless explicitly exported.

TokenHub streams a unified SSE event format to the Playground. If the selected upstream supports streaming, TTFT is measured from gateway admission to the first content delta and output throughput is calculated over the first-to-last content interval. If the upstream supports only buffered responses, TokenHub automatically falls back to buffered mode, marks TTFT as not applicable, and reports end-to-end output throughput instead of inventing a first-token measurement. Stopping a request preserves the partial text and marks the candidate as cancelled; authoritative token counts are shown only when the Provider returns them.

Rerunning an assistant turn creates another candidate for that turn and removes later turns so the new branch cannot silently reuse stale context. Changing models starts a new session by default; keeping the existing context requires an explicit choice. Parameter controls follow the selected model's declared `supported_parameters`, so the Playground does not send controls that the model catalog says are unsupported.

All permitted Playground users can see performance, usage, request ID, and their response details. Provider, resource, upstream request ID, and per-attempt routing details are visible only to roles with routing-read permission. Cost is labelled as an estimate because it uses the external model's configured price rather than an upstream invoice.

## API Key Ownership and Usage Attribution

When issuing an API Key, select the actual user in **Owner User**. The issuer remains in audit metadata, but the Key's usage is attributed to its owner. Platform administrators may select any active user; team leaders may select an active user in their own team; ordinary users can only assign Keys to themselves.

Each new usage record snapshots the attributed user, so later ownership changes or Key deletion do not rewrite that recorded history. Records created before this field existed fall back to the Key's current owner, then its legacy issuer, then the project owner, and finally `unknown`. The individual ranking shows distinct used Keys and currently owned non-revoked Keys separately.

## Per-Key RPM and TPM Limits

Each API Key can have optional requests-per-minute (RPM) and tokens-per-minute (TPM) limits. An unset or `null` value inherits the applicable global, project, and team policies. A value of `0` adds no Key-specific cap but cannot bypass an upper-level limit, while a positive value adds a Key-specific cap. When several positive limits apply, TokenHub enforces the strictest one. Disabling a Key rejects every request regardless of its limit values.

RPM is consumed before a Provider is invoked. TPM is reserved at the same point from the request's estimated input and maximum output; text requests without an explicit maximum reserve 4,096 output tokens. After the request finishes, the reservation is settled to the Provider's reported total tokens, or to prompt plus completion tokens when a total is unavailable. Cached and reasoning tokens are already included in those totals and are not added again. Failed or interrupted requests return the unused reservation.

An exceeded limit returns HTTP 429 with `api_key_rpm_exceeded` or `api_key_tpm_exceeded`, plus `Retry-After` and the relevant `X-RateLimit-Limit-*`, `X-RateLimit-Remaining-*`, and `X-RateLimit-Reset-*` headers. Minute buckets are database-backed and shared across TokenHub instances on both SQLite and PostgreSQL. Metrics expose only a short hashed Key reference, never the complete API Key.

## Provider Catalog Availability

TokenHub stores the last known-good provider catalog in the database. On every backend startup, it validates and loads the configured local `provider-catalog.json`, then atomically replaces the database snapshot. Ordinary **Provider Channels** requests only read the database snapshot, and administrators can manually refresh the same local catalog. If local catalog reading, parsing, or completeness validation fails, TokenHub keeps using the last known-good snapshot.

## Codex Usage Reset Credits

For an active OpenAI Codex Subscription account, open **Provider Channels**, edit the Provider, and expand **Advanced > Subscription quota**. The account card shows the authoritative number of available reset credits and the nearest expiry reported by OpenAI. **Reset usage window** opens a second confirmation before it consumes one non-recoverable credit; it resets eligible Codex usage windows but does not change the ChatGPT billing plan. A completed or idempotently repeated operation refreshes both quota and reset-credit details.

Reset idempotency state is stored as `AdminResource` records of kind `codex-quota-reset-operations` in the existing admin-resources table, so this feature requires no schema migration. Succeeded and failed records are retained and are not automatically deleted because they prevent a replay from consuming another credit; include them in normal database retention and backups. During an upgrade, preserve the database. A `pending` or `unknown` record blocks a different reset for the same account, is shown to the console after restart, and may only be retried with the same idempotency key, expected count, and credit ID until OpenAI returns a definitive outcome.

## Provider Inventory, Model Directory, and Publication

TokenHub separates the model lifecycle into three control areas:

| Control area | Meaning |
| --- | --- |
| **Provider Channels** | Upstream connections and their imported model inventory. Creating a catalog-based Provider requires selecting at least one model, but importing inventory alone never exposes it to clients. Custom Providers can be created empty and populated after the upstream connection is available. |
| **Model Directory** | Only the external models that form the API contract for applications. Creation starts by choosing a template from the built-in model reference catalog, or a blank custom model, then selecting one or more imported Provider models for its initial routes. Its prices are the unified client-facing prices, independent of which Provider route serves a request. |
| **Routing Policies** | Manage an external model's Provider mappings and fine-tune priority, weight, project scope, traffic allocation, and failover strategy. |

The responsibilities remain separate: add a Provider and import inventory first; then choose one of the built-in reference models, create its external contract, select at least one initial Provider route, and set its unified external price. The selected template pre-fills the name, capabilities, context, and suggested prices, all of which can be adjusted before saving. After creation, add, change, or remove mappings only under Routing Policies. Model Directory keeps a read-only upstream summary and opens Routing Policies filtered to the selected external model; the Provider list's Configure Routes action opens the complete Routing Policies workspace so new mappings can be added. For example, an administrator can expose the external model `DeepSeek` while routing it to `OpenAI Production / gpt-4.5`. The same Provider model may back several external aliases, and one external model may route to several Providers.

Provider-model prices represent actual upstream cost and are used for internal audit. Model Directory prices represent the unified external charge used for client billing estimates, quota accounting, metrics, and usage reports. A route selects the upstream implementation but does not change the external price.

When Provider Channels, Model Directory, or Routing Policies has no configured data, the console shows the same three-step setup guide: import Provider inventory, create an external model from the built-in 165-model catalog, then configure routing. The primary action always points to the earliest incomplete prerequisite, so administrators are not sent into a form that cannot yet be completed.

Publication and runtime health are different states. Membership in `GET /v1/models` requires an active external `Model`, at least one active `ModelRoute`, and API-key access when a model allowlist is configured. It does not change when a Provider or Provider Resource is temporarily unhealthy. Health affects whether a request can be served and is shown separately in the directory and routing diagnostics. Disabling the external model removes it from `GET /v1/models` while retaining its mappings for later re-publication.

## Model Routing Policies

The admin console configures one routing strategy for the whole external model. Open the model card and select a strategy tab; the active tab explains its best use case, actual selection behaviour, parameter meaning, and a concrete example. Adjust the Provider parameters shown for that strategy, then choose **Apply Strategy**. The policy and every Provider parameter are saved atomically, so a model never runs with a partially updated configuration.

For fixed-ratio routing, enter the relative weight beside each Provider. Two Providers with weights 75 and 25 display target shares of 75% and 25%. Adaptive routing uses the same values as base weights and dynamically adjusts effective shares. Quality, cost, and balanced modes expose only their relevant scores. All of these strategies place eligible Providers in one traffic-allocation pool. Sequential failover is the only mode that uses Provider order; drag the rows to set first, second, and later choices.

| Strategy | Behaviour |
| --- | --- |
| `priority_weighted` | Uses the configured weights as the target traffic ratio across routes at the same priority. For example, weights 75 and 25 target a 75:25 split over a representative request volume. |
| `adaptive` | Starts from the configured weights and adjusts the effective weights using invoked attempts from the last 15 minutes. A route begins adapting after 5 samples; recent success rate and successful-request latency influence its share, with bounded adjustments to prevent starvation or extreme shifts. |
| `quality` | Always tries the highest quality score first, with weight used only to break score ties. |
| `cost` | Always tries the highest cost-efficiency score first. A higher score means a cheaper, more preferred Provider. |
| `priority_only` | Uses the Provider list as a strict primary/backup order and does not distribute normal traffic. |
| `balanced` | Preserves legacy behaviour by using `weight + quality score + cost score` as the effective probabilistic weight. New configurations should normally use fixed ratio or adaptive routing instead. |

Provider connection details and project restrictions remain route-specific. Editing one Provider route changes its upstream model, project scope, sticky-session setting, or status; overall strategy, weight, and scores are edited in the model policy instead. `all` makes a route available to every project, `include` limits it to the selected projects, and `exclude` makes it available to every project except those selected. Project scope is evaluated before traffic allocation and failover, and displayed traffic shares are recalculated across the eligible Providers.

For a private-project boundary, create an internal Provider route with scope `include` and select the private projects. Create the corresponding external Provider route with scope `exclude` and select the same projects. The private projects can then use only the internal route, while other projects continue to use the external Provider.

Project route scope also controls model discovery: `GET /v1/models` includes an external model only when the calling API key's project has at least one active eligible route, in addition to the normal model and API-key allowlist checks.

### Scoped Routing Policies

Use **Scoped Policies** to bind an independent routing policy to the global gateway, a project, or an API Key. TokenHub resolves exactly one effective policy in this order: API Key, then project, then global. Finding a higher-priority binding ends resolution even when that policy is disabled, conflicting, or leaves no eligible candidate; the router fails closed and never falls back to a lower scope. Only one policy may be bound to a given target, while unbound definitions can be prepared without affecting traffic.

Model access is evaluated before routing. Projects and API Keys each support `inherit` and `restricted` access modes. A restricted list is intersected with every upper-level list, so an API Key cannot expand project access; a restricted empty list denies every model. For compatibility, records created before access modes existed that have a blank mode and an empty list continue to inherit. `GET /v1/models` applies the same effective access scope and requires a route allowed by the effective routing policy.

A scoped policy can constrain model names, Providers, Provider Resources, required route tags, resource regions, and resource environments, and can override the routing strategy. Configure tags on model routes and region/environment metadata on Provider Resources. Existing route-level project scopes are intersected with these constraints. Traffic allocation, session/cache affinity, half-open recovery, and failover run only after filtering and can never add an excluded route back to the candidate pool. This makes an internal-only policy fail safely instead of silently crossing to an external Provider.

The preview/simulation panel accepts a project, API Key, and model and displays the effective policy, access decision, selected route, and a safe allow/exclude reason for every candidate. Policy failures use diagnostic codes such as `routing_policy_unavailable`, `routing_policy_conflict`, and `routing_policy_no_candidate` without exposing credentials. Request logs record `routing_policy_id`, `routing_policy_scope`, and `routing_policy_priority`; generic policy create/update/delete operations and explicit bind/unbind operations also write administrator audit events.

The management API uses generic resource CRUD at `/api/admin/resources/routing-policies`, plus `POST /api/admin/routing-policies/{id}/bind`, `POST /api/admin/routing-policies/{id}/unbind`, and `POST /api/admin/routing-policies/simulate`. The same enforcement applies to OpenAI-compatible model requests, Anthropic Messages, image generation, and the administrator playground.

## Provider Resource Recovery

A provider resource that fails `TOKENHUB_RESOURCE_FAILURE_THRESHOLD` times in a row is parked: it stops receiving traffic and enters a cooldown. Recovery is automatic and needs no admin action.

| Phase | Behaviour |
| --- | --- |
| Parked | The resource is excluded from routing for `TOKENHUB_RESOURCE_COOLDOWN_SECONDS` |
| Half-open | Once the cooldown lapses, exactly one request is allowed through as a trial; every other request is still rejected |
| Recovered | The trial reaching the upstream successfully clears the breaker, resets the failure count and raises a `provider_resource_recovered` alert |
| Re-parked | A failed trial immediately arms the next cooldown, doubling each time up to `TOKENHUB_RESOURCE_COOLDOWN_MAX_SECONDS` |

Only the trial request's own success closes the breaker. A request that was already in flight when the breaker tripped cannot close it, however it ends. What counts is decided by what the upstream failure proved about the resource, not by the status the caller was given. A client that disconnects mid-stream, a policy refusal, an unsupported model, a request the upstream rejected as malformed, and anything else that is about the request rather than the account count neither for nor against the resource: they add no failure, but they also clear none, so an alternating failure/disconnect pattern still trips the breaker. A rejected credential, an unpayable account, a rate limit and any upstream fault do count against it.

Testing a resource from the console still recovers it immediately when the adapter supports probing, because that probe issues a real upstream request. Disabling a resource remains an administrative override: a disabled resource is never readmitted by recovery, whatever the upstream reports.

## Upstream Error Classification

An upstream failure is graded on three separate questions, because one status code cannot answer all of them: what the caller is told, whether the router tries another candidate, and whether the attempt counts against the Provider Resource. A malformed request is malformed at every provider, so it is reported to the caller and no other candidate is tried. A rejected credential is specific to one account, so the request moves on and the account is blamed.

| Upstream | Caller sees | Error code | Another candidate | Counts against the resource |
| --- | --- | --- | --- | --- |
| `400`, `422` | the same status | `provider_invalid_request` | No | No |
| `401`, `403` | `502` | `provider_auth_error` | Yes | Yes |
| `402` | `502` | `provider_payment_required` | Yes | Yes |
| `404` | `502` | `provider_model_not_found` | Yes | No |
| `408` | `504` | `provider_upstream_timeout` | Yes | Yes |
| `413` | `413` | `provider_invalid_request` | No | No |
| `429` | `429` with `Retry-After` | `provider_rate_limited` | Yes | Yes |
| `502`, `503`, `504` | the same status | `provider_upstream_unavailable` | Yes | Yes |
| other `5xx` | `502` | `provider_upstream_error` | Yes | Yes |
| other `4xx` | `502` | `provider_error` | No | No |

An upstream `401` or `403` is not forwarded as such: it means the gateway's own credential for that provider was rejected, and a caller reading `401` would conclude their TokenHub API key had expired. For the same reason the upstream body is withheld on those two, since providers quote the rejected key back in it. The original status is recorded as `upstream_status` on each route attempt, where an operator can see it.

Every route attempt records both: `status_code` is what the caller was told, `upstream_status` is what the provider answered.

## Request Usage Audit

Each request row in **Request Logs** includes its total tokens and external billing amount. For administrators with global operations visibility, the detail panel also shows the Provider's actual cost calculated from the selected Provider model; other users do not receive that cost field. The panel retains the upstream billing breakdown when it is available: cached, cache-write, and audio input tokens, plus reasoning, audio, accepted-prediction, and rejected-prediction output tokens. Providers that do not return a field are shown as zero. Input and output totals already contain their detail categories, so do not add the detail values to the totals again.

## Metrics

TokenHub can expose Prometheus metrics at `GET /metrics`. Collection is off by default; set `TOKENHUB_METRICS_ENABLED=true` to enable it. While disabled, nothing is collected and the endpoint returns 404. The endpoint always authenticates: metrics disclose model names, provider and resource identifiers, and spend, so it is never anonymous. Send `Authorization: Bearer <token>` using `TOKENHUB_METRICS_TOKEN`, or the admin token when that variable is empty. A dedicated token is recommended so the scrape configuration does not carry the admin credential. A token supplied in the query string is rejected, because it would be captured in access logs.

| Metric | Type | Meaning |
| --- | --- | --- |
| `tokenhub_gateway_requests_total` | counter | Logical model API requests. A request that failed over across several candidates counts once. |
| `tokenhub_gateway_request_duration_seconds` | histogram | End-to-end latency including failover attempts. Buckets run to 300s. |
| `tokenhub_gateway_requests_in_flight` | gauge | Model API requests currently being served. Admin traffic and scrapes are excluded. |
| `tokenhub_gateway_tokens_total` | counter | Tokens by kind: `prompt`, `completion`, `cached`, `cache_write`, `reasoning`. |
| `tokenhub_gateway_cost_usd_total` | counter | Unified external billing estimate from Model Directory prices. Provider actual cost remains in privileged request audit rather than this metric. |
| `tokenhub_gateway_rate_limit_hits_total` | counter | Rejected requests by effective policy scope and limit type. `key_ref` is a short hash only for `api_key` limits; inherited global, project, and team limits use `none` to bound series cardinality. |
| `tokenhub_gateway_trace_completions_total` | counter | Finished calls by what trace export did with them: `converted` or `dropped`. Only present when tracing is on. |
| `tokenhub_gateway_trace_spans_total` | counter | Spans by what the OTLP exporter did with them: `exported` or `failed`. Only present when tracing is on. |

Go runtime and process metrics are exposed alongside them.

**Token kinds are not a partition and must not be summed.** `prompt` already contains the `cached` and `cache_write` tokens, and `reasoning` is a subset of `completion`. Summing the kinds double-counts.

Requests refused before routing — a bad API key, an exhausted quota, an unknown model — increment the request counter only. They never reached a provider, so they contribute no tokens, cost or duration. A model name that the catalog does not know is reported as `unknown` rather than verbatim, so a client looping over invented model names cannot inflate the series count.

Labels are `model`, `provider_type`, `provider_id`, `resource_id`, `status_code`, `error_code` and `stream`. Upstream failures no longer report a single `provider_error` at `status_code="502"`; they carry the codes and statuses listed under Upstream Error Classification, so dashboards and alerts that match on the old pair need updating. Upstream failures no longer report a single `provider_error` at `status_code="502"`; they carry the codes and statuses listed under Upstream Error Classification, so dashboards and alerts that match on the old pair need updating. Setting `TOKENHUB_METRICS_PROJECT_LABEL=true` adds `project_id`, which multiplies the series count of every gateway metric by the number of active projects; leave it off unless you need per-project dashboards, and use the usage reports for per-key attribution instead.

To push metrics instead of having them scraped, point an OpenTelemetry Collector's `prometheus` receiver at this endpoint and forward from there. Traces are a separate signal and are pushed directly; see below.

## Trace Export

TokenHub can export one OpenTelemetry trace per gateway call over OTLP/HTTP. Each trace is a root span for the request plus one generation span for every candidate that entered the invocation path, so a failover shows both candidates with the tokens and cost each of them consumed. A candidate skipped for lack of capacity never reached a provider and is recorded as an event on the root instead. Metrics tell you that latency rose; a trace tells you which account served the request and what it cost. Export is off by default: it sends operational data to another system.

Set `TOKENHUB_TRACING_ENDPOINT` to the signal-specific OTLP traces URL, including its full path. It is used verbatim — nothing is appended — because a guessed path suffix fails as a silent 404 rather than as a startup error. A URL without a path, or one carrying a query, fragment or userinfo, is rejected at startup rather than quietly exported somewhere else. Any OTLP/HTTP backend works; no OpenTelemetry Collector is needed, because the gateway speaks OTLP over HTTP directly rather than gRPC.

For Langfuse:

```bash
TOKENHUB_TRACING_ENABLED=true
TOKENHUB_TRACING_ENDPOINT=https://cloud.langfuse.com/api/public/otel/v1/traces
TOKENHUB_TRACING_HEADERS="Authorization=Basic $(printf '%s' 'pk-lf-...:sk-lf-...' | base64),x-langfuse-ingestion-version=4"
```

The attribute mapping targets the Langfuse v4 ingestion model, which is generally available for self-hosted deployments and the default for Langfuse Cloud organizations created after 2026-04-14. Self-hosting Langfuse v4 requires ClickHouse 25.12 or newer alongside PostgreSQL, Redis and object storage. TokenHub does not bundle Langfuse into its own Compose files: that is a second stateful stack with its own upgrade cycle, and coupling the two would make a Langfuse migration a gateway outage.

Whether prompts and responses are exported is a separate decision from whether tracing is on, and `TOKENHUB_TRACING_CAPTURE_PAYLOADS` is off by default. With it off a trace still carries status, latency, the provider and resource that served each attempt, tokens, cost, transport and upstream request IDs. With it on, request and response bodies go through the same redaction and truncation as the stored payload log, and upstream error text is treated as payload for the same reason: an upstream error can embed a response body, a URL or an account identifier.

Usage and cost are attached to the generation spans and never to the root. Langfuse v4 aggregates over observations, so repeating them on the root would double every token and every dollar in the project totals. Token counts are also rewritten into mutually exclusive buckets on the way out. TokenHub's input and output totals already contain their detail categories — cached, cache-write and audio input; reasoning, audio and prediction output — while Langfuse sums the buckets it is given, so each detail is subtracted from its total and capped by what remains. The cost exported is the billed figure only; the Provider's own cost stays in the privileged request audit, where TokenHub deliberately confines it.

![A failover trace in Langfuse: the root request span, two failed attempts on the unreachable account, and the generation that served it with its own token count and cost](assets/screenshots/tracing-langfuse-trace-en.png)

Trace and span IDs are both derived from the request ID, so the `x-request-id` header returned to a client leads straight to its trace without a lookup table. That is a lookup convenience and not a deduplication guarantee: Langfuse can still create a duplicate observation for a span ID it has already seen. Export retries are therefore disabled and delivery is at-most-once — a batch lost to a transient failure is not resent. For traces carrying token counts and spend, inflated cost is the worse failure, and this pipeline already drops rather than waits when it is saturated. Playground traffic is exported with a `playground` tag so it can be excluded from cost analysis, and requests refused before routing are exported too, since a quota or admission failure is usually what you are trying to explain.

![The Langfuse trace list showing gateway, playground and rejected traffic side by side, filterable by tag](assets/screenshots/tracing-langfuse-list-en.png)

Export never delays a request. Completions are queued and turned into spans on a separate goroutine; when the queue is full the completion is dropped rather than made to wait. Drops are counted in `tokenhub_gateway_trace_completions_total` with `outcome="dropped"` and logged at most once a minute, so a gap in Langfuse is distinguishable from a gateway that received no traffic. Delivery is counted separately in `tokenhub_gateway_trace_spans_total`, because a full queue and a backend that refuses every batch are different problems with different fixes. Image generation jobs are not traced yet: they complete asynchronously on a worker and need their own idempotency design first.

## Prompt Cache Pricing

The model catalog accepts an optional cache read price in USD per 1 million tokens. When it is configured, cached input tokens use that price in estimated costs. When it is left blank, TokenHub estimates the cache read price at about 0.83% of the standard input price for DeepSeek V4 Pro, 2% for other DeepSeek models, and 10% for other non-embedding models. The model pricing table marks estimated values and explains the applied ratio on hover.

## Catalog Metadata Recovery

Deleting an external model removes its database record and routes, but it does not edit `data/model-catalog.yaml` or the file configured by `TOKENHUB_MODEL_CATALOG_FILE`. Backend startup synchronizes tracked catalog metadata from that file again; administrators can trigger the same synchronization without a restart from **Settings → Base Settings → Sync Model Reference Catalog**. This synchronization does not import a model for a Provider, create a route, or publish it in `GET /v1/models`; those remain explicit administrative actions in their respective control areas.

## External Billing Connectors

Platform administrators manage external billing sources from **Cost Billing**. TokenHub supports Aliyun `QueryInstanceBill`, NewAPI quota data, and OneAPI-compatible log sources. A connector can be tested, synchronized immediately, scheduled at a minute interval, disabled without deleting its history, and re-enabled later.

For Aliyun, configure the billing RPC base URL, AccessKey ID, AccessKey Secret, source time zone, and optional product code. TokenHub signs each RPC request with HMAC-SHA1 and advances across billing cycles. For NewAPI, configure the base URL, access token, `New-Api-User` ID, currency, and the quota units that equal one currency unit. TokenHub calls `GET /api/data/self` with the documented authentication headers and automatically splits ranges into windows of at most 30 days. For OneAPI-compatible sources, configure the base URL, API token, log path, currency, and quota conversion. All connectors accept a per-second request limit; synchronization uses bounded exponential retries for temporary network, `429`, and `5xx` failures.

Manual synchronization may specify `from` and `to` RFC 3339 timestamps. Without an explicit range, TokenHub continues from the last successful end time. It saves the provider cursor after every page, so a retry resumes the failed range rather than starting over. Normalized records use `(connector_id, external_id)` as the idempotency key and retain currency, source time zone, tax, discount, refund, billing period, and usage timestamps. Recent runs show pages, request attempts, inserted and updated records, and a sanitized failure code.

Connector credentials and raw billing snapshots are AES-GCM encrypted with `TOKENHUB_SECRET_KEY`. They are not returned by admin APIs or written to audit payloads. Keep that key stable across restarts and replicas. The relevant endpoints are `GET/POST /api/admin/billing/connectors`, `PATCH /api/admin/billing/connectors/{id}`, `POST /api/admin/billing/connectors/{id}/test`, `POST /api/admin/billing/connectors/{id}/sync`, `GET /api/admin/billing/records`, and `GET /api/admin/billing/sync-runs`.

## Security Checklist

| Control | Requirement |
| --- | --- |
| API keys | Show the full secret once, then store only prefix and suffix |
| OAuth redirect URI | Register local and production callback URLs with the identity provider |
| RBAC | Separate user, team leader, administrator, finance, security, and operator scopes |
| Audit retention | Keep request logs and admin events long enough for compliance review |
| Cost controls | Attribute every request to user, project, team, and cost center when possible |

## Chinese Enterprise Identity Sources

In **Identity Sources**, select a built-in DingTalk, Feishu, or WeCom template. The template fills the public endpoints and claim mappings; only override the advanced endpoints when traffic must pass through an enterprise proxy or a compatible private deployment.

Creating an identity source uses three required steps: choose the source, enter its connection settings, and configure the login entry plus first-login grants. The connection step links to the selected provider's official setup guide so you can create the application and obtain its credentials. Generic OIDC and OAuth2 templates instead tell you to consult the actual provider's application-registration guide and link to the relevant protocol reference. From the third step, templates with complete endpoint defaults can use **Skip and Finish**; otherwise the advanced endpoint fields become required. You can also open advanced settings to override endpoint, scope, and claim defaults. Editing an existing source keeps the complete form available on one screen.

Use the public TokenHub backend URL with the callback path `/api/admin/auth/oauth/callback`. You may leave Callback URL blank to derive it from the incoming backend host; when setting it explicitly, the complete URL must exactly match the redirect URL registered with the identity provider.

| Provider | Required application configuration | TokenHub behavior |
| --- | --- | --- |
| DingTalk | Create a web application, enable user authorization, register the callback URL, and copy its App Key and App Secret | Uses the DingTalk v1.0 JSON token API and user access-token header. If the authorized profile has no email, TokenHub derives a stable internal email from `unionId`. |
| Feishu | Create an enterprise self-built application, enable web authorization, register the callback URL, and copy its App ID and App Secret. Grant profile and enterprise-email access when available. | Uses the Feishu OAuth v2 token API and unwraps the `data` user-info response. If email is unavailable, TokenHub derives a stable internal email from `union_id`. |
| WeCom | Create a custom application and configure its trusted web authorization domain. Copy the Corp ID, application Secret, and Agent ID, and grant the application permission to read the required directory members. | Uses WeCom CorpApp login, exchanges the application token, resolves the callback code to `UserId`, and then reads the member profile. `biz_mail` is preferred; a stable internal email is derived from `userid` when needed. |

The derived addresses end in `<provider>.tokenhub.local`. They are internal account identifiers, not deliverable mailboxes. Keep a controlled password administrator until the new login has been tested end to end.

## Screenshot

![Routing policies](assets/screenshots/routes-en.png)
