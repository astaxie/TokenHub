# A2A 1.0 Agent Gateway

Language: English | [简体中文](zh-CN/a2a-agent-gateway.md) | [日本語](ja/a2a-agent-gateway.md)

TokenHub can expose reviewed upstream agents through an A2A 1.0 JSON-RPC gateway with SSE streaming. A2A 0.3, HTTP+JSON, and gRPC are not accepted by this surface.

## Rollout

Set `TOKENHUB_A2A_ENABLED=true` and restart the backend. The default is `false`; setting it back to `false` removes the public Agent Card, A2A gateway, MCP admission, and `agent/<slug>` Responses bridge without deleting registry or task data.

Production upstreams must use HTTPS and must not resolve to loopback, link-local, or private addresses. `TOKENHUB_A2A_ALLOW_PRIVATE_UPSTREAMS=true` exists for controlled local development only.

## Register and review an Agent

Open **Agent Gateway** in the admin console and provide a lowercase slug plus an Agent Card URL. TokenHub fetches and validates the card, requires an A2A 1.0 `JSONRPC` interface, strips upstream security declarations from the published card, encrypts configured static headers, and creates an immutable revision. A database-managed revision can be restored from the same page. Entries synchronized from `data/agent-catalog.yaml` are read-only in the console.

Registration is also available through `POST /api/admin/agents`. Use either `card_url`, or an inline `card`; `upstream_url` may override the reviewed JSON-RPC interface. Do not commit credentials to `data/agent-catalog.yaml`. Slugs must be unique in that file. A config entry takes ownership of an existing database Agent with the same slug, disables its previous instances, and becomes read-only in the console; an administrator cannot overwrite a config-managed Agent.

```yaml
agents:
  - slug: research
    card_url: https://research.example/.well-known/agent-card.json
    status: active
    max_concurrency: 8
    allowed_forward_headers: [X-Request-ID, traceparent]
```

## Access control

Agent invocation is default-deny. Create at least one active allow binding through the console or `POST /api/admin/agent-access-bindings`. Bindings can target `global`, `team`, `project`, `api_key`, `end_user`, `agent`, or `access_group`; an active matching deny binding always wins. A binding may restrict access to Agent Card skill IDs.

`X-TokenHub-End-User-ID` is accepted only when the API Key metadata contains `allow_end_user_identity=true`. This prevents an untrusted caller from selecting another end-user identity.

## A2A calls

Every JSON-RPC request must include `A2A-Version: 1.0` and a TokenHub API Key. Agent Card discovery also requires the API Key and returns only allowed skills. Missing or different protocol versions return A2A error `VERSION_NOT_SUPPORTED`; unauthorized discovery returns the same not-found response as an unknown Agent.

```bash
curl "$TOKENHUB_URL/a2a/research" \
  -H "Authorization: Bearer $TOKENHUB_API_KEY" \
  -H "A2A-Version: 1.0" \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc":"2.0",
    "id":"request-1",
    "method":"SendMessage",
    "params":{"message":{"messageId":"message-1","role":"ROLE_USER","parts":[{"text":"Summarize the release"}]}}
  }'
```

The gateway supports `SendMessage`, `SendStreamingMessage`, `GetTask`, `ListTasks`, `CancelTask`, and `SubscribeToTask`. Push notification methods are not supported. Public cards are available at `/a2a/<slug>/.well-known/agent-card.json` and `/.well-known/agent-card.json?agent=<slug>`.

TokenHub replaces every upstream task ID with a gateway ID and permanently binds that task to its selected instance and API Key. Task continuation, reads, cancellation, and subscriptions use the same instance. A message that may have reached an upstream is never automatically retried on another instance.

Configured static upstream headers override any client-derived value. Client headers are forwarded only when their names appear in `allowed_forward_headers`; authentication credentials, cookies, hop-by-hop headers, `A2A-Version`, and TokenHub delegation headers are always rejected from this allowlist. Static credentials may be supplied through the admin API or secret-rendered deployment configuration, but must not be committed to the tracked catalog.

Each upstream call receives a five-minute `X-TokenHub-Delegation-Token`; an instrumented Agent can use it as a Bearer token when calling TokenHub model or Agent APIs. The signed identity carries the project, Key, end user, caller Agent, execution, parent step, depth, chain, and expiry. Repeated Agent IDs are rejected as loops. Existing before-provider content security policies also evaluate direct A2A text parts and Responses-bridge input before an upstream call.

## Responses bridge

Applications that only implement the OpenAI Responses API can call an Agent as model `agent/<slug>`:

```json
{"model":"agent/research","input":"Summarize the release","stream":true}
```

TokenHub converts the input to an A2A user message and converts Agent text, status messages, and text artifacts back to Responses output. Existing non-Agent `/v1/responses` behavior is unchanged.

## Runtime governance and MCP accounting

Each root invocation creates a database-backed execution and step graph. The defaults limit Agent hops, model calls, instrumented MCP calls, runtime, tokens, cost, and concurrent Agent steps. Configure them with the `TOKENHUB_A2A_MAX_*` variables described in the deployment guide.

An instrumented Agent admits an MCP call before execution and reports actual usage afterward with its delegation token:

```bash
curl "$TOKENHUB_URL/api/a2a/executions/mcp" \
  -H "Authorization: Bearer $TOKENHUB_DELEGATION_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"phase":"admit","step_id":"mcp-step-1"}'
```

Send a second request with `phase: "complete"`, the same `step_id`, and non-negative `tokens` and `cost_usd`. TokenHub cannot count an opaque MCP call made entirely inside an uninstrumented upstream Agent; operators must require this admission protocol when MCP limits are mandatory.

Registry revisions, instance health, task snapshots/events, execution edges, model/MCP counters, tokens, and costs are persisted in SQLite or PostgreSQL. Static credentials and delegation signing material are never returned by admin APIs.
