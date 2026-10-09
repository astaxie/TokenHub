# Model Catalog Updates

Language: English | [简体中文](zh-CN/model-catalog-updates.md) | [日本語](ja/model-catalog-updates.md)

The 2026-10-10 catalog review adds current upstream model offers and records access, retirement, compatibility, and pricing boundaries. Catalog inclusion lets administrators discover an offer; it does not establish call support, account entitlement, live health, or production deployment.

## Review scope

The update covers the standard model templates, official Provider catalogs, and generated built-in Provider plugin catalogs. It includes OpenAI GPT-6 Sol/Luna and newer media offers; Claude 5.5 and Fable 5.1; Gemini 3.8 and Embedding 2; Qwen 3.8, text retrieval, images, and realtime models; DeepSeek V4.1 Flash; Grok 4.7 and Imagine; MiMo V2.6; Doubao Seed 2.1; Mistral Large 4; Cohere Embed 5/Rerank 4; Voyage Rerank 3; and reviewed Kimi, GLM, MiniMax, and StepFun offers. A model can appear under several regions or subscription providers, so Provider entries and unique model templates are different counts.

The standard candidate templates also include OpenAI `text-embedding-3-small` and `text-embedding-3-large`. Amazon Nova 2 Sonic and Nova Multimodal Embeddings are catalog-only (`call_support=unsupported`); Bedrock native inference and billing support still require separate validation.

This review also retains evidence for earlier offers that are deprecated, retired, or redirected. It does not automatically rename models, move routes, change saved costs, or deploy a production release.

Newly listed image, audio, video, and live-session offers are catalog-only where marked `call_support=unsupported`. This update adds no new media or realtime protocol adapter. Existing media integrations retain their own support scope; see [Media APIs](media-apis.md). Embedding and reranking support remains limited to the validated text contracts described in [Text Embeddings](embeddings.md) and [Text Reranking](rerank.md).

## What administrators see

Provider selection, model creation, and the Provider inventory show notices for preview access, subscription or special access requirements, retirement dates, replacement IDs, redirected aliases, unsupported operations, and prices that require confirmation. Dates are rendered in the selected UI locale and local timezone.

- `active` describes the upstream offer. It does not guarantee that a particular account can call it.
- `preview` retains the upstream preview status. `restricted` or an access requirement identifies a named plan, approval, or special entitlement.
- `deprecated` requires migration planning. A valid `shutdown_at` becomes a new-route restriction when the time is reached.
- `retired` prevents publishing a new active route to that exact official offer.
- `redirected` means the upstream accepts the old name but serves another model. It is not automatically treated as an unavailable endpoint.
- `call_support=unsupported` prevents publishing a new active route even when the model remains visible in the catalog.

Lifecycle policy is matched by the Provider's `catalog_id` and the exact upstream model ID. An official API retirement is not applied globally to a third-party host or a self-hosted copy with a similar name. Existing published routes are not automatically disabled or migrated. Unrelated edits that preserve an existing route's target and status remain allowed; changing the target or re-enabling a route runs publication checks again. Existing runtime capability checks still apply.

## Confirm upstream costs before publishing

The catalog's price fields are reference data, not a supplier bill or an automatic update to saved Provider costs. A missing or zero reference price does not mean free usage. Prices can depend on region, subscription, context tier, time window, cache type, image quality, resolution, seconds, characters, or search units.

When a Provider inventory item is marked `pricing_status=unverified` and has no configured positive input/output cost or explicit confirmation, publication of a new active route is blocked. Open that Provider's model inventory, verify the account's actual price and billing unit, enter the upstream costs, and save. Saving reviewed costs marks them as configured. If the actual price is zero, enter and save zero deliberately after verification; leaving an unverified catalog zero untouched is not confirmation. Retrieval models also require the relevant token or search-unit price and their existing pricing confirmation checks.

A positive number can still be only a catalog reference. If `pricing_status=unverified` and `catalog_price_reference=true`, that number alone does not satisfy new-route cost confirmation. The reviewed GPT-6 entries retain verified short-context reference rates, but requests above 272,000 input tokens use another tier. Verify the applicable costs and explicitly save them as `pricing_status=configured` to clear this restriction. The advisory overlay does not replace an existing inventory item's configured positive costs or copy these pricing flags onto it.

For an exact match to a currently unverified catalog entry, a legacy inventory item with zero input/output costs and no pricing-status or explicit confirmation also receives the unverified notice during listing and new-route checks. This overlay is not persisted and does not change existing route billing. Saved positive costs and explicitly configured costs remain intact. Inventory outside that exact catalog match is not retroactively audited. Keep tenant prices separate from upstream costs. The refresh does not convert CNY to USD, flatten peak/off-peak or long-context tiers, reuse PAYG prices for a subscription, or convert image/second/character charges into token prices. Structured pricing notes describe the upstream terms; they do not install a new billing formula. Confirm support for the required unit before publishing.

## Metadata reference for maintainers

Catalog model `metadata` is transported as string values. Serialize structured evidence as JSON strings; it is descriptive unless code explicitly consumes the field.

| Field | Meaning and maintenance rule |
| --- | --- |
| `upstream_source`, `verified_at` | Primary source URL and verification date (`YYYY-MM-DD`). Record the source that owns the ID and parameters. |
| `catalog_reviewed_at` | Review marker used to retain that exact model entry when a later public catalog refresh contains stale data. Re-review marked entries when upstream facts change. |
| `availability`, `access_requirement` | `active`, `preview`, or `restricted`, plus any required plan or approval. Do not infer subscription access from PAYG availability. |
| `lifecycle_status`, `lifecycle_source` | `active`, `deprecated`, `retired`, or `redirected`, supported by an official notice for the specific Provider offer. |
| `shutdown_at`, `replacement_model` | Enforced shutdown time in RFC 3339 with an explicit offset, and the exact suggested replacement ID. Keep date-only uncertainty in `shutdown_time_note`; do not invent an authoritative hour. |
| `redirect_at`, `replacement_note` | Planned upstream redirection and its conditions. A redirection date must not be modeled as an unconditional call failure. |
| `call_support`, `support_note`, `call_support_scope` | Use `unsupported` for catalog-only operations; describe the missing contract or a scope such as `text-only`. Omitting the flag is not an account or integration test result. |
| `pricing_status`, `pricing_source`, `pricing_note` | `reference` for usable catalog reference prices, `unverified` when an operator must configure costs, and `configured` after explicit operator review. Preserve source and conditions. |
| `catalog_price_reference` | `true` means a populated price is only a reference. Combined with `pricing_status=unverified`, even positive input/output prices require explicit cost confirmation before a new active route can be published; saving as `configured` clears that restriction. |
| `original_currency`, `original_unit`, `original_prices` | Original quoted currency, billing unit, and structured prices. These are evidence, not implicit currency conversion. |
| `pricing_tiers`, `pricing_schedule`, `reference_token_prices` | Conditional or promotional reference prices. Keep their conditions and dates; do not silently write the lowest number as a universal cost. |
| `billing_unit`, native-unit price fields | Distinguish tokens, search units, images, seconds, and characters. A search-unit rate is not a per-million-token rate. |
| `max_input_tokens`, `max_output_tokens`, reasoning and embedding fields | Keep input, output, context, thinking budgets, dimensions, and vector-space identity separate. Omit unverified limits and record uncertainty. |

A catalog refresh preserves reviewed model entries while allowing unreviewed entries and new upstream IDs to refresh. Advisory overlays update lifecycle and support information without overwriting an operator's stored costs or custom metadata.

## Verification and update workflow

1. Inspect all three catalog layers and compare exact Provider IDs, upstream model IDs, regions, and plan types. Preserve unrelated local changes.
2. Read the official model card, API contract, pricing page, and retirement notice. Distinguish a release announcement, a preview, a hosted API offer, and downloadable weights. Resolve conflicting limits against the current API/model page and record remaining uncertainty.
3. Verify modalities, endpoints, thinking controls, tool-history requirements, context/output limits, and billing units. Catalog an unsupported operation explicitly rather than claiming a new adapter exists. Changing an embedding model may require rebuilding the index; matching dimensions alone do not establish vector-space compatibility.
4. Update the standard templates and appropriate official Provider entries. Keep review/source metadata, then regenerate the built-in plugin catalogs with `node tools/generate-builtin-provider-plugins.mjs`. Do not copy official retirements or prices onto aggregator offers automatically.
5. Run the catalog, publication-policy, protocol-contract, and translation checks relevant to the change. Test new and existing route behavior separately, including explicit zero-cost confirmation and unsupported/retired offers.
6. Before enabling customer traffic, verify the real account's region, model entitlement, request parameters, streaming/tool use, metering, and actual price. A local test or PR is not that account verification or a production rollout. Plan any route migration separately.

## Primary sources

These links are the starting points used for the 2026-10-10 review. Per-model metadata carries the more specific source; recheck it before later updates.

| Vendor | Official sources |
| --- | --- |
| OpenAI | [API changelog](https://developers.openai.com/api/docs/changelog) |
| Amazon Nova | [Core inference](https://docs.aws.amazon.com/nova/latest/nova2-userguide/core-inference.html) |
| Anthropic | [Release notes](https://platform.claude.com/docs/en/release-notes/overview) |
| Google | [Gemini changelog](https://ai.google.dev/gemini-api/docs/changelog), [deprecations](https://ai.google.dev/gemini-api/docs/deprecations) |
| Qwen / Alibaba Cloud | [Model releases](https://help.aliyun.com/zh/model-studio/newly-released-models), [China pricing](https://help.aliyun.com/zh/model-studio/model-pricing), [international pricing](https://www.alibabacloud.com/help/en/model-studio/model-pricing) |
| DeepSeek | [Models and pricing](https://api-docs.deepseek.com/quick_start/pricing/), [Chat API](https://api-docs.deepseek.com/api/create-chat-completion/) |
| xAI | [Models](https://docs.x.ai/developers/models), [pricing](https://docs.x.ai/developers/pricing), [Image Quality migration](https://docs.x.ai/developers/migration/imagine-image-quality-nov-2) |
| Xiaomi MiMo | [Models](https://mimo.mi.com/docs/en-US/quick-start/model), [pricing](https://mimo.mi.com/docs/pricing) |
| Doubao | [Models](https://docs.volcengine.com/docs/ark/model-list?lang=zh), [retirement notices](https://docs.volcengine.com/docs/ark/model-deprecation-notice?lang=zh) |
| Mistral | [Models and lifecycle](https://docs.mistral.ai/models), [changelog](https://docs.mistral.ai/resources/changelogs) |
| Cohere | [Models](https://docs.cohere.com/docs/models), [pricing](https://cohere.com/pricing) |
| Voyage AI | [Rerank](https://docs.voyageai.com/docs/reranker), [pricing](https://docs.voyageai.com/docs/pricing) |
| MiniMax | [Models](https://platform.minimax.io/docs/guides/models-intro), [M Plan](https://platform.minimax.io/docs/m-plan/intro) |
| Kimi | [Models](https://platform.kimi.ai/docs/models) |
| GLM / Z.AI | [Model documentation](https://docs.z.ai/guides/llm/glm-5.3) |
| StepFun | [Model migration](https://platform.stepfun.ai/docs/en/guides/model-migration) |
