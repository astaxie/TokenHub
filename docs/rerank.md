# Text reranking

`POST /v1/rerank` scores candidate documents for a query. It uses TokenHub API keys, model permissions, request limits, applicable security policies, gateway hooks, routing and audit. A model must be published as `rerank` with a supported Provider protocol and configured tenant price. New publication also requires provider prices.

```json
{"model":"public-reranker","query":"How do I renew a certificate?","documents":["Renewal instructions","An unrelated document"],"top_n":1,"return_documents":true}
```

The response contains `model` and `results` with original `index`, `relevance_score` and optional `document.text`. Duplicate texts remain distinct by index. Scores are not rescaled or comparable across models. The gateway does not split a listwise request into batches. Empty queries/documents, invalid `top_n`, unsupported fields, invalid upstream indices and missing results are errors. Only text is supported; images, video and asynchronous jobs are outside this release.

`documents` must contain 1–2048 nonempty text strings or objects of the form `{"id":"doc-1","text":"Document text"}`; both forms may be mixed. When provided, `top_n` must be between 1 and the document count; omitting it returns all documents.

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

For custom `openai-compatible` and `local` providers without a vendor catalog (or with the `custom` / `local` / `openai-compatible` catalog), automatic protocol selection uses the Jina-compatible `/rerank` contract. Explicit protocol settings and named vendor defaults take precedence. Unknown vendor catalogs still require an explicit supported protocol; TokenHub does not infer a native vendor API from a model name.

For `http://model-host:8000/v1/rerank`, set Base URL to `http://model-host:8000/v1` and leave the path empty or enter `/rerank`. If the service uses `/rerank` at its root, omit `/v1` from Base URL. Confirm the actual serving endpoint and protocol.

Published routes remain callable with unconfirmed zero upstream costs. Missing cost evidence stays unknown, not free; token usage and tenant charges are preserved. New publication still requires both prices, while runtime continues to enforce tenant pricing, capability and inventory checks. v0.8 did not expose a native `/v1/rerank` endpoint; this is a local-service configuration fix, not a claim of identical rerank endpoint behavior across those releases.

### Console guidance

The advanced settings show the effective protocol and protocol-specific default path. An empty path uses that default; the provider endpoint preview appends it to Base URL without changing the saved configuration. Expand the inline example for the Base URL, path, and resulting URL. Resource-account overrides may change the actual connection; a preview is not a connectivity test. Enter a path beginning with `/`, not a full URL or a filesystem path. Embedding and rerank settings are independent.

Request details show the saved error message directly above the payload. A route-selection rejection explicitly states that no upstream request was sent; requests without usage records display a dash rather than fabricated zero amounts. Provider inventory distinguishes an unconfirmed zero embedding input cost from confirmed free usage. Saving costs confirms the entered values, including zero. Native search-unit procurement costs remain unknown when their native price is missing; auxiliary token counts never substitute for that price.

When editing a compatible provider, the console keeps the saved runtime catalog (and therefore automatic protocol defaults) separate from the catalog template used to discover or import models. The admin provider PATCH request can opt into this behavior with `preserve_catalog: true`; it preserves `catalog_id`, `catalog_source`, and `doc_url` only when the provider type is unchanged. Create requests, explicit type changes, and callers that omit the flag retain their previous behavior. Invalid endpoint paths and malformed embedding-space JSON are rejected by the console before saving, including when the advanced tab is closed.

## Document identity, limits and privacy

An object document requires a nonblank string `id` (at most 256 UTF-8 bytes) and a nonempty string `text` (at most 64 KiB of UTF-8). String documents have the same text limit. IDs are opaque: duplicate IDs and duplicate texts remain distinguishable by their original `index`. Each result includes the original top-level `id` when supplied, even when `return_documents` is false. Only `document.text` is controlled by `return_documents`. Upstreams receive text, never caller IDs. Plugins cannot change IDs; cache keys include them, and final responses validate IDs against the original index without restoring redacted document text.

The normal JSON request limit applies (8 MiB by default), with at most 2048 documents. Every plugin request patch must fit the configured JSON request limit; oversized patches return `502 gateway_hook_patch_invalid` before reaching an upstream. Native content-security masking must preserve the per-document and total request limits; an oversized masked request returns `413 request_too_large` before cache lookup or provider invocation. Upstream JSON and final plugin/cache responses are limited to 8 MiB. Provider timeouts and cancellation use the gateway transport policy. Client cancellation, including cancellation returned by a provider-call hook, does not trigger failover or count as a provider-resource failure. Decoding errors, including missing or wrongly typed `id`/`text` fields and blank IDs, return `400 invalid_request`; other invalid inputs such as empty text, excessive lengths or invalid `top_n` return `400 invalid_rerank_request`; malformed, oversized, duplicate-index or invalid-score responses return `502 invalid_rerank_response`. Oversized total requests use the shared request-limit error. Provider authentication, rate-limit and timeout classifications remain available, while provider error messages are replaced with a generic message so echoed queries/documents cannot enter errors or attempt logs.

The gateway records `document_count` in durable metering evidence, independently of audit payload storage and reported token/search-unit usage. Missing supplier usage remains unknown; document counts do not invent token costs. If a complete leading JSON response reports usage before a read error, trailing invalid data, or response-size rejection, its usage remains in route-attempt accounting and request quota counters while delivery still fails. Incomplete JSON does not establish usage. Rerank request/response audits retain model, document/result counts and sanitized policy decisions, with the configured audit retention; queries, IDs, document text and arbitrary provider metadata are omitted, including when trace payload capture is enabled. Plugin error messages, arbitrary error codes and nonnumeric rate-limit headers are sanitized before error responses, attempt logs or internal hook-failure logs. Plugin audit events retain hook/stage/status metadata and an omission marker instead of their arbitrary payload.

Run `TOKENHUB_RERANK_MODEL=public-reranker npm run test:rerank` from `sdk/` with a permitted `TOKENHUB_API_KEY` and `TOKENHUB_BASE_URL`. The shipped Node client checks string/object/mixed inputs, IDs, ordering, Top-N, optional document return and validation. Backend fake-HTTP tests run the same client without real provider credentials.
