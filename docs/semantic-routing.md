# Jev model routing strategy

Jev is a routing strategy alongside fixed weights, adaptive, quality, cost, primary/backup, and balanced routing. A client sends an ordinary public model name to TokenHub. TokenHub asks Jev to classify the latest user task and choose a configured model, then calls that model through its existing Provider adapter. Jev does not generate the answer and does not need to be registered as a Provider.

## Configure and call

1. Create a public model, for example `auto-chat`, and add routes to the approved upstream models. This is an ordinary model alias: any name can use any routing strategy; the name does not activate Jev.
2. In **Routing → Model routing policy**, select **Jev Smart Routing**.
3. Select candidate models from the existing routes, describe each model's task criteria, enter the selection instructions, and choose a default model and minimum confidence. Apply the strategy.
4. Call `/v1/chat/completions` or `/v1/responses` with that public model name. Both endpoints support streaming. Tools and other generation parameters remain in the target-model request.

For example, one candidate can handle extraction and translation, while another handles complex code changes. Describe suitability using workload evidence; a model name alone does not establish quality. Multiple resource accounts for the same Provider/model appear as one candidate. Other routing strategies use the same public-model and route configuration flow.

After changing a route's Provider or upstream model, the Jev editor refreshes the candidate while retaining the saved candidate order. Review the replacement model's task criteria before applying the policy; enter criteria if its catalog does not supply them.

```json
{"model":"auto-chat","messages":[{"role":"user","content":"Explain this code change."}],"max_tokens":128}
```

```json
{"model":"auto-chat","input":"Explain this code change.","max_output_tokens":128,"stream":true}
```

## Server configuration

External Jev calls default to off. The server must allow the project and hold the TypeSafe key:

```dotenv
TOKENHUB_SEMANTIC_ROUTING_ENABLED=true
TOKENHUB_SEMANTIC_ROUTING_PROJECTS=prj_example
TOKENHUB_TYPESAFE_API_KEY=<server-side-secret>
TOKENHUB_TYPESAFE_MODEL=jev-1.13.0
TOKENHUB_SEMANTIC_ROUTING_TIMEOUT_MS=1000
```

The project allowlist contains exact IDs separated by commas. An empty list permits no external evaluation; startup rejects an enabled deployment without a key or allowlist. Pin the evaluator version. The timeout covers candidate metadata lookup and evaluation, defaults to 1000 ms, and cannot exceed 10000 ms. Each process permits eight simultaneous evaluations, without a queue or retry.

## Selection and fallback

TokenHub authenticates the request, applies quotas, privacy processing and guardrails, and checks eligible response-cache entries before evaluation. Responses for aliases that have used explicit Jev routing, and all `previous_response_id` continuations, bypass result-cache lookup/write because cache entries do not carry verified route bindings. Existing project/API key restrictions, scoped policies, Provider health, resource availability and protocol support constrain the routes first. Jev can only select explicitly configured candidates remaining in this admitted set.

Jev selects across candidate models regardless of their route priority tiers. The existing planner still orders resource accounts within each Provider/model. On a valid choice at or above the threshold, execution tries that model's resources, then the default model, then the other configured candidates in configuration order. Failover follows existing retry rules and never calls Jev again. Unselected routes outside the configured candidate set are excluded.

Timeouts, unavailable evaluators, saturated concurrency, invalid decisions, `no_preference`, and low confidence use the default model. Disabling the server gate or excluding the project also uses that default without sending text. If the default is not eligible, the first eligible configured candidate becomes the fallback. If no configured candidate can serve the request, or catalog lookup fails, TokenHub returns 503 instead of choosing an unconfigured model. A single eligible candidate needs no evaluation.

Candidate catalog entries must be active. Declared modalities, supported wire parameters and context limits constrain eligibility; a parameter from another protocol is not treated as equivalent. Context checking uses a conservative serialized-byte estimate plus the output-token budget. Missing declarations do not establish capability or suitability. Keep Provider model metadata accurate. Catalog normalization independently adds `max_tokens` for an advertised Chat Completions endpoint and `max_output_tokens` for an advertised Responses endpoint, even when another budget field is already declared. Explicit declarations are retained; request parameter names are forwarded unchanged. The shipped GPT-6 Astra entries explicitly declare `max_tokens`, `max_completion_tokens`, and `max_output_tokens`. Reimport previously saved Provider models to refresh their declarations.

Existing cache/session affinity and sticky-route ordering take precedence. An explicit scoped strategy override skips Jev and ranks only within the configured candidate set. Requests with session identifiers do not trigger a new Jev decision. The route simulator does not invoke Jev and cannot predict its classification. Anthropic Messages, embeddings and image APIs retain their existing routing behavior.

## Responses continuation

For a Jev Responses request, TokenHub saves the selected route, Provider/model, resource account and upstream response ID. Public upstream response IDs remain unchanged. A request using `previous_response_id` must use the same API key and public model; it reuses the original route without another classification. The binding also applies if the routing strategy is subsequently changed. Background Responses bind their public job ID to the actual upstream response ID atomically with successful job completion, after output hooks approve the response. Pending, rejected, cancelled, or ownership-lost jobs do not publish a binding. If an output plugin rewrites an ID, TokenHub maps that public ID to the original upstream ID.

Detailed route bindings are shared through SQLite or PostgreSQL and expire after 30 days. A non-expiring keyed hash marker, containing no upstream ID, account or prompt, remains for each protected response ID. It blocks replay through another public alias after the detailed binding expires; marker storage grows with protected IDs. All instances must use the same stable server secret. Expiry, an unknown ID on a Jev model, removal from its candidate set, or an unavailable/unauthorized original route produces HTTP 409. Continuations also recheck the bound catalog model's active status, wire parameters, modalities, and context limits; failure returns 409 without reclassification or failover. A binding read/write failure produces 503. A continuation never fails over to another model or account; start a new request with full input if the previous route cannot be used. These bindings do not extend an upstream provider's own retention period.

## Data handling and billing

The classifier receives only the latest eligible user text, selection instructions, candidate identifiers, upstream model names and configured task criteria. System/developer instructions, assistant history, tool definitions/results, media, credentials, headers and arbitrary model metadata are not sent to Jev. The full processed generation request is preserved for the selected Provider. Text over 8192 bytes, missing user text, or non-text content in the latest user message uses the fallback instead of truncating or sending media.

The fixed endpoint is `https://api.typesafe.ai/v1/systemone`; redirects are rejected. Jev returns a constrained TypeSafe `Choice`. TokenHub checks candidate membership and confidence locally. Restrict external routing to approved workloads. The initial `0.65` threshold is a starting value, not a quality guarantee: confidence is a choice distribution, not task success probability.

Audit action `routing.semantic` records selection/fallback, strategy, protocol, selected model and candidate ID, evaluator version, confidence/probabilities, evaluator tokens and latency under the gateway request ID. It contains no user text or credentials. Generation request logs retain the executed upstream model. Public-model pricing still applies; the alias is not automatically repriced by selected model. Evaluator usage is separate and is not added to the generation token usage or customer bill.

## Administration API and compatibility

`PATCH /api/admin/model-routing-policies/{model}` atomically saves the strategy, every route and the model-selection policy. Example for two existing routes:

```json
{
  "strategy": "jev",
  "routes": [
    {"route_id":"route_fast","weight":100,"quality_score":50,"cost_score":50},
    {"route_id":"route_deep","weight":100,"quality_score":50,"cost_score":50}
  ],
  "semantic_routing": {
    "mode":"enforce",
    "min_confidence":0.65,
    "instructions":"Choose using the configured task criteria.",
    "default_candidate_id":"fast",
    "candidates":[
      {"id":"fast","provider_id":"provider_a","provider_model":"small-model","criteria":"Simple extraction and translation"},
      {"id":"deep","provider_id":"provider_b","provider_model":"reasoning-model","criteria":"Complex analysis and code changes"}
    ]
  }
}
```

The Jev strategy requires `enforce`, explicit instructions, 1–32 distinct Provider/model candidates, nonempty criteria, a default in that set, and an explicit finite threshold between 0 and 1. Instructions are limited to 4096 bytes and each criterion to 2048 bytes. Candidate IDs must be unique and cannot be `no_preference`. Every candidate must reference the public model's routes; invalid settings roll back the entire update.

Policy remains in `Model.metadata.tokenhub_semantic_routing`. Ordinary model edits and catalog imports preserve it. Previously saved `off`/`shadow`/`enforce` overlays without explicit candidates retain the old Chat-only, same-priority behavior until reconfigured. The console identifies such legacy settings. Applying an ordinary strategy disables the overlay; applying Jev replaces it with explicit candidates. Omission of `semantic_routing` preserves legacy overlays; switching an explicit Jev policy to an ordinary strategy disables classification even when this field is omitted. Choosing `jev` requires the full policy. The server-managed `response_binding_required` flag remains true once the alias uses Jev: future Responses on that alias still save and validate bindings after strategy changes, and unknown, foreign or expired continuations remain rejected. Clients cannot clear this flag through policy updates.

Schema migration 6 adds the durable `jev_response_bindings` table and expiry index. Startup applies this additive migration before admitting requests; no baseline schema is rewritten. Roll back routing behavior by selecting an ordinary strategy, or disable external evaluation with the server gate. Neither removes existing continuation bindings. Binary rollback must satisfy the database compatibility manifest; do not delete migration history or the binding table to force it.

## Validation and rollout

Synthetic tests cover the TypeSafe wire contract, bounded choices, policy validation and atomicity, parameter compatibility, candidate restrictions, fallback, Chat/Responses streaming and tools, continuation ownership/resource binding, background jobs, SQLite/PostgreSQL persistence, and UI save/reload. They do not establish real workload accuracy or production latency. Start with an approved test project, compare task outcomes, added latency and total evaluator/generation cost against labeled examples, then expand the project allowlist.

An optional real API smoke is `TestJevRoutingClientLive`. Set `TOKENHUB_LIVE_TYPESAFE_API_KEY` only in the test process environment and run `go test ./internal/server -run ^TestJevRoutingClientLive$ -count=1 -v` from `backend/`. It sends a synthetic request to TypeSafe without calling a generation Provider and is skipped by default.

References: [LangChain harness with Jev](https://www.langchain.com/blog/building-a-harness-with-jev), [TypeSafe API](https://docs.typesafe.ai/api), [Choice](https://docs.typesafe.ai/primitives/choice), [Confidence](https://docs.typesafe.ai/confidence).
