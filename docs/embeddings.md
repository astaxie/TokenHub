# Text embeddings

TokenHub exposes `POST /v1/embeddings` with the normal API-key permissions, routing, limits and request audit. Send `model` and either one text or an array of independent texts. A batch returns one dense vector per input, preserving `index`; malformed upstream counts, indices and dimensions are errors.

Optional parameters are `dimensions`, `encoding_format` (`float` or `base64`), `input_type` (`query` or `document`), `task`, `normalized`, `truncation`, `late_chunking` and `user`. Availability depends on the selected protocol. Unsupported options are rejected rather than silently discarded. Sparse, quantized and multimodal inputs/outputs and asynchronous Batch jobs are outside this endpoint's current scope. Token ID inputs remain available on compatible upstreams.

The gateway accepts at most 2048 inputs per request; individual protocols may impose lower limits. `dimensions` must be between 1 and 65536. Specify either `task` or `input_type`, not both.

## Provider configuration

In the Provider's advanced settings, open **Text embedding settings**. `embedding_protocol` selects `openai`, `cohere`, `jina`, `voyage`, `dashscope` or `tei`; Gemini uses its native adapter. An empty value uses the catalog default or OpenAI compatibility. Custom providers must select their actual protocol. The path is appended to the Provider Base URL; `embedding_path` overrides that relative endpoint without changing the host or credentials.

| Protocol | Example Base URL ending | Default path | Notes |
| --- | --- | --- | --- |
| OpenAI compatibility | `/v1` | `/embeddings` | OpenAI, SiliconFlow, compatible vLLM/Xinference and Alibaba endpoints |
| Cohere | `/v2` | `/embed` | `input_type` or `task` required; dense float output |
| Voyage / Jina | `/v1` | `/embeddings` | Task and dimension fields are mapped to the native names |
| DashScope native | `/api/v1` | `/services/embeddings/text-embedding/text-embedding` | Text input, dense output; gateway limit of 10 inputs per native call |
| TEI native | server root | `/embed` | Text batches; no invented usage when the server omits it |
| Gemini | configured Gemini base | `:embedContent` / `:batchEmbedContents` | Independent texts remain independent requests |

Use the provider's documented regional URL. A catalog listing or passing HTTP connection test does not establish that a model supports every parameter. Native protocol fixtures validate conversion locally; real account, regional and model availability require upstream verification.

## Vector compatibility

By default, sources must match the Provider, upstream model, adapter type, effective Base URL, embedding protocol and endpoint path. Resource overrides participate in this identity; different deployments need an explicit shared space. To permit verified equivalent deployments, configure `embedding_spaces` as a JSON mapping from **upstream model ID** to an administrator-confirmed space ID, for example `{"my-embedding-model":"space-v1"}`. Every participating deployment must use equivalent model versions and encoding behavior. Equal dimensions alone are insufficient. Changing spaces requires rebuilding the application's stored vector index; TokenHub never edits that external index.

Missing upstream usage remains unreported in metering evidence, distinct from reported zero. Estimates are not substituted for measured tokens. Provider costs and tenant prices remain separate.

New or republished routes require configured tenant and provider prices. The tenant uses the embedding price, while the upstream inventory uses the input price. To confirm a free token price explicitly, set `metadata.retrieval_pricing_confirmed="true"` on the corresponding public or upstream model; an empty legacy zero is not confirmation.

```json
{"model":"public-embedding","input":["first document","second document"],"dimensions":1024,"encoding_format":"float"}
```

OpenAI SDK clients can use `client.embeddings.create`. Configure the public model name and TokenHub base URL in Dify or the relevant LangChain/LlamaIndex integration. Actual client/version acceptance is separate from local protocol tests.

Model discovery prefers explicit `type`, `modality` or `model_type`. When absent, established BGE, GTE, E5, Voyage and sentence-transformer names are recognized as embedding candidates; reranker names take precedence. Inferred type still does not prove deployed capability.

All active route definitions, resource overrides and possible provider fallbacks must agree on one space before any cache lookup or routing. Health/cooldown changes and weighted ordering cannot change that contract. Conflicting configurations return `409 embedding_space_conflict`; remove incompatible routes or assign the same verified space only after confirming compatibility.

Gateway hooks may rewrite text but must preserve input cardinality, dimensions, encoding and task semantics. Cached, plugin and post-processed results are validated against the original client contract before delivery. Cache plugins receive a host-generated `cache_key` bound to the verified space, request and caller scope; a hit must echo the stored key in its `cache_key` write. Missing or mismatched keys are treated as misses. Cache-write hooks receive the same key. Existing cache plugins need this contract to serve embedding hits safely.

The public model must have modality `embedding` and a configured embedding price or explicit free-price confirmation before hooks execute. Hooks cannot change text/token input modes or rewrite token IDs. When token usage is unreported, TPM settlement retains the admission estimate for quota enforcement only; billing evidence remains unreported.

The model directory displays the embedding rate rather than the chat input rate. Provider inventory uses a compact retrieval price editor; hidden chat/cache rates remain unchanged when saving. Request details show the response first, with request payloads selectable and additional metadata/usage breakdown expandable.

Explicitly confirmed free embeddings remain zero-priced even if a legacy chat input rate or adapter-reported charge is present. Provider costs and token counters are retained. The directory displays confirmed free retrieval rates as zero and unconfigured rates as unknown. Deployment identity changes invalidate prior embedding cache entries; no database migration is required.

Token-ID inputs reserve TPM using the exact sum of token IDs, including batches. Global pre-routing hooks may rewrite text before cache binding; route-scoped request transforms cannot change embedding input. The cache namespace changes so entries created under the previous transform contract become misses. Before execution, non-mock routes must still have a matching text-embedding inventory entry, even if inventory or public model settings changed after publication.

## Diagnosing rejected routes

When runtime filtering rejects every candidate, the API retains HTTP `501` and `provider_capability_not_supported` for compatibility. The response and saved request-log response include an actionable `error.message` and `error.details` with `stage="route_selection"`, `upstream_attempted=false`, and a `reasons` array. Each entry includes a stable `code`, a remediation `message`, and `route_count`. The count reflects candidate routes, not upstream attempts; only the first blocking reason per candidate is reported. Multiple reasons are sorted by code. No provider identifiers, endpoints, credentials, or exact costs are included.

| Reason code | Check |
| --- | --- |
| `provider_capability_or_protocol_unsupported` | Provider adapter and Embedding protocol support |
| `tenant_price_not_configured` | Public model Embedding price or explicit free-price confirmation |
| `upstream_model_inventory_missing` | Matching provider inventory entry and route upstream model name |
| `upstream_model_modality_mismatch` | Inventory model type must be `embedding` |
| `upstream_text_input_unsupported` | Inventory must support text input |

No upstream request has been sent in this case, so an empty route-attempt list is expected. Filling in `/embeddings` cannot resolve an inventory or tenant-pricing rejection. Startup may backfill missing inventory from existing routes; its unconfirmed zero procurement cost no longer blocks an existing route. Earlier public-model admission failures remain `400 embedding_model_not_configured`; other failures such as vector-space conflicts retain their existing errors. Valid candidates continue normally when only some routes are rejected.

## Upgrading local embedding routes

Published routes remain callable when the upstream input cost is zero or not yet confirmed. This preserves the v0.8 behavior for self-hosted models without claiming that an unknown procurement cost is free. Usage and tenant charges are still recorded; missing provider-cost evidence stays pending in metering and is not a confirmed zero. Administrators can save the actual cost (including an explicitly confirmed zero) in the provider inventory. New route publication still requires tenant and provider price configuration. Model type, text capability, authorization and vector-space checks remain enforced.

For an OpenAI-compatible local endpoint at `http://model-host:8000/v1/embeddings`, set Base URL to `http://model-host:8000/v1`, select automatic or `openai` for Embedding, and leave its path empty or enter `/embeddings`. The path is appended to Base URL; it is not a full URL. No database rewrite or automatic free-price confirmation is performed.

### Console guidance

The advanced settings show the effective protocol and protocol-specific default path. An empty path uses that default; the provider endpoint preview appends it to Base URL without changing the saved configuration. Expand the inline example for the Base URL, path, and resulting URL. Resource-account overrides may change the actual connection; a preview is not a connectivity test. Enter a path beginning with `/`, not a full URL or a filesystem path. Embedding and rerank settings are independent.

Request details show the saved error message directly above the payload. A route-selection rejection explicitly states that no upstream request was sent; requests without usage records display a dash rather than fabricated zero amounts. Provider inventory distinguishes an unconfirmed zero embedding input cost from confirmed free usage. Saving costs confirms the entered values, including zero. Native search-unit procurement costs remain unknown when their native price is missing; auxiliary token counts never substitute for that price.

When editing a compatible provider, the console keeps the saved runtime catalog (and therefore automatic protocol defaults) separate from the catalog template used to discover or import models. The admin provider PATCH request can opt into this behavior with `preserve_catalog: true`; it preserves `catalog_id`, `catalog_source`, and `doc_url` only when the provider type is unchanged. Create requests, explicit type changes, and callers that omit the flag retain their previous behavior. Invalid endpoint paths and malformed embedding-space JSON are rejected by the console before saving, including when the advanced tab is closed.
