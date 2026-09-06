# Model pricing and platform statements

## Model price changes

Platform administrators open **Cost Governance → Cost Billing → Model pricing** and choose a downstream model. Current model prices are loaded. Chat and embedding token prices are supported; provider-cost previews are reference calculations and do not change Provider cost configuration.

1. Edit input, output, cache prices or time windows, and enter sample usage.
2. Select **Calculate cost**. Preview invokes actual model billing logic without saving prices or creating trial history.
3. Select **Apply to model prices** and review the model, old/new prices, and time windows in the confirmation dialog.
4. Only **Confirm price change** saves the change. Cancel writes nothing; repeated clicks are disabled while submitting.

New requests use the applied prices immediately; in-flight requests retain their admission-time model configuration. If another administrator changes the prices or inherited cache-pricing metadata after preview, application is rejected and requires a new preview and confirmation. Replaying the same change request neither reapplies it nor duplicates successful price-change audits. Scheduled activation is not supported in this increment.

Blank cache-read prices retain the model's default estimation rule; blank cache-write prices inherit default input/write rates. Explicit `0` means free. Provider input, output and cache-read costs distinguish missing configuration from explicit free prices: saving blank inventory fields means unknown, while `0` means free. Unknown costs never fall back to tenant charges. Reimporting an existing Provider model refreshes catalog details while preserving saved prices, time windows and price-presence flags; change costs explicitly in inventory.

Time windows support weekdays (0 = Sunday, 6 = Saturday), IANA timezones, inclusive starts/exclusive ends, and input/cache/output overrides. Overnight windows belong to their starting day. Up to 64 non-overlapping windows are allowed. The preview picker displays the browser's local timezone; each window matches in its own timezone. Embedding windows are unsupported.

**Price changes** lists only applied changes, retaining model, actor, timestamp, old/new configuration and effective base-price evidence, up to the latest 100 entries. Standalone shadow-card publication is retired; `/api/admin/billing/rate-cards` returns 410. Existing evidence is not deleted and historical charges are not recalculated.

## Platform statements

**Cost Billing → Platform statements** requires no billing connector. Choose a month or custom date range, view tenant charges separately from provider cost estimates, group by Provider, resource account, model or project, inspect paginated details, and export CSV.

- Tenant amounts come from settled platform charges, grouped by admission time. Already-posted charges for failed or partially delivered requests remain included; internal retries do not duplicate tenant charges.
- Provider estimates include each upstream attempt and its recorded prices/usage, including retries. Missing prices, usage, completion or conversion evidence remains pending.
- Known subtotals include known amounts only. Pending or incomplete historical evidence makes coverage incomplete, never a supplier-confirmed cost.
- Historical rows use persisted amounts and identities, without recalculation using current prices.
- Summaries, details and reconciliation share the statement-evidence projection. Queries above 10000 rows fail explicitly and require narrower ranges/filters; they are not silently truncated. Platform ranges allow up to 366 days; standalone customer/provider statement previews retain their 93-day range limit.

**Customer statements and margin** preserves the existing detailed preview. Model Directory and Provider inventory can also launch scoped statements. This view requires explicit customer projects, exports the displayed snapshot, and keeps supplier-billed amounts separate from estimates rather than adding them together.

**External reconciliation** compares connector bills with local provider costs. Connectors are optional for platform estimates. Unknown provider cost cannot be reconciled as zero or substituted with a tenant charge; evidence must be completed first. Explicitly free provider cost can be compared as zero. This increment adds no platform-statement locking, PDF, or manual external-bill import.

## Administration API

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/api/admin/billing/models/{model}/pricing` | Current pricing configuration and concurrency fingerprint |
| POST | `/api/admin/billing/preview` | Preview `{card, usage, at, exchange_rate?}`; tenant previews use actual billing logic |
| POST | `/api/admin/billing/model-pricing/apply` | Apply `{card, fingerprint, request_id, confirmed:true}` |
| GET | `/api/admin/billing/price-changes` | Recent applied price changes |
| GET | `/api/admin/billing/statements` | `kind=tenant/provider`, RFC3339 `from/to`, filters, grouping and pagination; `format=csv` exports |
| POST | `/api/admin/billing/statements` | Existing customer/provider/margin statement preview |
| GET | `/api/admin/billing/evidence/{request_id}` | Original request and upstream-attempt evidence |

These endpoints are restricted to platform administrators. Model prices and the applied-change record commit in one transaction. CSV excludes credentials, prompts and response bodies, and neutralizes formula prefixes. Existing schema version 4 supports these records; historical migrations are unchanged. Request evidence continues to accumulate without automatic retention cleanup.
