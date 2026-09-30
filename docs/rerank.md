# Text reranking

`POST /v1/rerank` scores candidate documents for a query. It uses TokenHub API keys, model permissions, request limits, applicable security policies, gateway hooks, routing and audit. A model must be published as `rerank` with a supported Provider protocol and configured tenant price. New publication also requires provider prices.

```json
{"model":"public-reranker","query":"How do I renew a certificate?","documents":["Renewal instructions","An unrelated document"],"top_n":1,"return_documents":true}
```

The response contains `model` and `results` with original `index`, `relevance_score` and optional `document.text`. Duplicate texts remain distinct by index. Scores are not rescaled or comparable across models. The gateway does not split a listwise request into batches. Empty queries/documents, invalid `top_n`, unsupported fields, invalid upstream indices and missing results are errors. Only text is supported; images, video and asynchronous jobs are outside this release.

`documents` must contain 1–2048 nonempty text strings. When provided, `top_n` must be between 1 and the document count; omitting it returns all documents.

## Configure a provider

In **Advanced → Text rerank settings**, choose the protocol that the deployed server actually implements. The endpoint path is appended to the Base URL and may be explicitly overridden with `rerank_path`.

| Protocol | Base URL ending | Default endpoint | Examples |
| --- | --- | --- | --- |
| `jina` | `/v1` | `/rerank` | Jina, SiliconFlow, compatible vLLM/Xinference deployments; local BGE requires a serving API |
| `cohere` | `/v2` | `/rerank` | Cohere text rerank |
| `voyage` | `/v1` | `/rerank` | Voyage; maps `top_n` to `top_k` |
| `qwen` | regional compatible API base | `/reranks` | Alibaba `qwen3-rerank` |
| `dashscope` | `/api/v1` | `/services/rerank/text-rerank/text-rerank` | Alibaba `gte-rerank-v2` native envelope |
| `tei` | server root | `/rerank` | Hugging Face TEI; maps documents to `texts` |

A bare model name does not identify the serving protocol. Xinference uses the deployed model UID. Alibaba URLs and availability vary by region; its documentation contains differing URL prefixes, so verify the exact deployed endpoint. No automatic alias from `qwen-rerank` to a different model is created.

`instruction` is available on the applicable Jina-compatible/Qwen/DashScope profiles, with native parameter mapping; `truncation` is available on Voyage and TEI. Support ultimately depends on the selected model. Unsupported protocol options fail explicitly.

## Prices, unsupported models and existing configuration

Token-metered models use the configured input price. Explicitly confirm free retrieval prices rather than relying on an empty legacy zero. Cohere's reported `search_units` use the separate **Price per search unit (USD)** on both the upstream inventory and external model. API configuration stores this decimal as `metadata.search_unit_price_usd`; explicit free token pricing uses `metadata.retrieval_pricing_confirmed="true"`. Provider costs and tenant charges are calculated separately and retain metering evidence; missing usage stays unreported rather than becoming measured zero.

Uncallable catalog models remain visible, are marked unsupported and cannot be selected for new routes. New publication validates capability and prices. Existing working routes are not disabled by upgrade; changing their model/provider or republishing applies the new checks. Startup idempotently normalizes reranker aliases and legacy reranker-shaped inventory stored as `chat`. A public `chat` alias is corrected only when all its configured upstreams are recognizable rerankers; mixed-purpose aliases remain unchanged. Prices, route settings and historical bills are preserved.

An administrator can test before publication using `POST /api/admin/playground/rerank`:

```json
{"provider_id":"configured-provider","request":{"model":"actual-upstream-model-or-uid","query":"example query","documents":["candidate document"]}}
```

Use the administrator session, optionally supply `resource_id`, and inspect `response`, `usage_evidence` and `pricing_status`. This is an administrative upstream test, not a tenant billing/authorization acceptance test; its audit stores model and metering evidence rather than query/documents.

## Validation boundary

Local tests cover representative protocol payloads and responses, gateway authentication, indices and search-unit charges. The configuration screen has a browser fixture. Production account permissions, regional URLs and exact serving versions require real-upstream validation. Dify, LangChain and LlamaIndex require the matching HTTP/rerank integration; OpenAI SDK does not define a native rerank method. Do not infer full client or supplier acceptance from a model appearing in the directory.

Final response validation preserves post-processing and safety redactions; it never restores document text from the original request. Administrator tests load decrypted Provider credentials and the selected resource overrides using the normal execution configuration path. Native-unit billing applies the same nonnegative usage protection as token billing, and free tenant requests still retain provider-cost records.

Contradictory primary usage (for example, explicit total_tokens=0 with prompt_tokens>0) and invalid native quantities return `502 invalid_provider_usage` rather than a successful zero-cost result. Valid search units are priced independently of malformed auxiliary token counters; counters remain nonnegative and the evidence records their inconsistency. Provider plugins declare `rerank_protocol=cohere` for search-unit billing and report `usage.retrieval_evidence` with `unit`, nullable `quantity`, and `source` (`upstream`, `plugin`, or `unreported`). A legacy zero-only token object remains unreported, not measured free usage.

Built-in `qwen` and `local` providers support configured rerank protocols. Publication checks each eligible resource override and any supported provider fallback; inventory availability includes resource-only capabilities. Scoped provider-call hooks remain eligible. TPM reservation includes `instruction`; search-unit-only or unreported token usage retains the admission token estimate for quota enforcement without converting billing units.

Provider inventory keeps retrieval prices and save actions together. The model directory labels configured search-unit and token rates separately when both exist; the route protocol determines billing. Request/response tabs and expandable metadata keep long rerank results readable without removing audit evidence.

Imports correct obvious reranker IDs when a legacy catalog still declares the generic chat fallback. Runtime routing rechecks operation, text capability and tenant pricing. Missing procurement prices do not disable a published route. Final plugin/cache/post-hook results must remain sorted by descending relevance score; invalid ordering is rejected without reconstructing redacted documents. Retrieval USD prices follow the selected console language.

Rerank cache hooks must use and echo the host-provided `cache_key` (`rerank:v1:`), which binds caller scope, the post-privacy request and the selected effective route configuration. Missing or mismatched keys are cache misses. Cache lookup probes eligible priced routes in plan order and uses the matching route for protocol and post-hook validation. Fallback responses are cached under the route that actually served them, so subsequent requests can reuse them without repeating upstream calls. Ineligible routes are never probed. Route-scoped request transforms cannot change ranking input after cache binding; perform content redaction in pre-routing hooks. Billing units are validated against the selected route protocol, including cached and plugin responses. Plugins cannot change `top_n` or `return_documents`; final responses must omit `document` when `return_documents` is false, while omitted or redacted documents remain untouched when it is true.

## Local compatible services and upgrades

For custom `openai-compatible` and `local` providers without a vendor catalog (or with the `local` / `openai-compatible` catalog), automatic protocol selection uses the Jina-compatible `/rerank` contract. Explicit protocol settings and named vendor defaults take precedence. Unknown vendor catalogs still require an explicit supported protocol; TokenHub does not infer a native vendor API from a model name.

For `http://model-host:8000/v1/rerank`, set Base URL to `http://model-host:8000/v1` and leave the path empty or enter `/rerank`. If the service uses `/rerank` at its root, omit `/v1` from Base URL. Confirm the actual serving endpoint and protocol.

Published routes remain callable with unconfirmed zero upstream costs. Missing cost evidence stays unknown, not free; token usage and tenant charges are preserved. New publication still requires both prices, while runtime continues to enforce tenant pricing, capability and inventory checks. v0.8 did not expose a native `/v1/rerank` endpoint; this is a local-service configuration fix, not a claim of identical rerank endpoint behavior across those releases.
