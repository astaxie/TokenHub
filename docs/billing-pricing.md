# Model pricing and platform statements

## Model price changes

Platform administrators open **Model Directory → Pricing and margin** for a token-priced chat or embedding model. Edit global input, output, cache prices and time windows directly. Historical analysis and the advanced single-request simulator are optional; neither is required to change a price.

1. Edit the prices. The ordinary model metadata editor does not overwrite token prices.
2. Optionally expand historical analysis, select dates, timezone, projects and cost basis. Project filters select evidence only: the saved model price remains global.
3. Select **Review and save**, review old/new prices and any loss, unknown-evidence or missing-analysis warnings, and acknowledge the impact and risks.
4. Confirm to apply. Cancel writes nothing. New requests use the change immediately; in-flight requests retain their admission-time configuration.

Analysis defaults to the previous 30 complete calendar days, excluding today, with a 7-day shortcut and custom ranges up to 93 days. Dates use the selected IANA timezone. Requests belong to their admission date; all recorded attempts, including retries across midnight, are evaluated at a fixed analysis cutoff. Above 10000 evidence rows the request fails and requires a narrower range; it never samples or truncates silently.

The result separates recorded historical charges, current-price replay and candidate-price replay. Historical cost uses saved attempt estimates. Current-procurement scenarios reprice those same attempts at their original times, routes and cache/retry distribution using current inventory prices; they are not forecasts. Absolute effective dates remain unchanged, and rules not exercised by the sample are identified. Revenue is counted once per request and costs include every attempt. Provider/resource combinations form separate margin groups.

Computable samples are not supplier-verified. Request coverage and known recorded-charge coverage are separate, with unknown charges counted separately. Zero denominators display no percentage. Missing evidence makes overall margin unavailable; the computable subset is shown without extrapolation. Losses and incomplete or absent analysis require acknowledgement but do not prohibit saving. Model-price changes invalidate the confirmation; current-procurement basis changes invalidate that analysis receipt. Repeating an identical change request does not duplicate the change or its audit.

Blank cache-read prices retain the model's default estimation rule; blank cache-write prices inherit default input/write rates. Explicit `0` means free. Provider input, output and cache-read costs distinguish missing configuration from explicit free prices: saving blank inventory fields means unknown, while `0` means free. Unknown costs never fall back to tenant charges. Reimporting an existing Provider model refreshes catalog details while preserving saved prices, time windows and price-presence flags; change costs explicitly in inventory.

Time windows support weekdays (0 = Sunday, 6 = Saturday), IANA timezones, inclusive starts/exclusive ends, and input/cache/output overrides. Overnight windows belong to their starting day. Up to 64 non-overlapping windows are allowed. The preview picker displays the browser's local timezone; each window matches in its own timezone. Embedding windows are unsupported.

**Price changes** lists only applied changes, retaining model, actor, timestamp, old/new configuration and effective base-price evidence, up to the latest 100 entries. Standalone shadow-card publication is retired; `/api/admin/billing/rate-cards` returns 410. Existing evidence is not deleted and historical charges are not recalculated.

Applied changes also retain the analysis summary and cost basis, or explicitly record that analysis was not performed. Drafts and each individual analysis are not saved as price versions.

Usage evidence is collected independently of pricing. Request detail and applied-change history expose reported, derived, estimated, missing, invalid and legacy-unverified fields, explicit zero values, stream completion and attempts. Supplier invocation ID, response ID and trace ID remain separate; a local request ID cannot substitute for a supplier ID. External reconciliation compares amounts only: an amount match does not establish token-field equivalence.

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
| POST | `/api/admin/billing/model-pricing/check` | Validate prices without sample usage |
| POST | `/api/admin/billing/model-pricing/impact` | Analyze `{card, fingerprint, basis, from, to, timezone, project_ids}` and issue a confirmation receipt |
| POST | `/api/admin/billing/preview` | Preview `{card, usage, at, exchange_rate?}`; tenant previews use actual billing logic |
| POST | `/api/admin/billing/model-pricing/apply` | Apply `{card, fingerprint, request_id, confirmed:true, risk_acknowledged:true, analysis_receipt?}` |
| GET | `/api/admin/billing/price-changes` | Recent applied price changes |
| GET | `/api/admin/billing/statements` | `kind=tenant/provider`, RFC3339 `from/to`, filters, grouping and pagination; `format=csv` exports |
| POST | `/api/admin/billing/statements` | Existing customer/provider/margin statement preview |
| GET | `/api/admin/billing/evidence/{request_id}` | Original request and upstream-attempt evidence |

These endpoints are restricted to platform administrators. Model prices and the applied-change record commit in one transaction. CSV excludes credentials, prompts and response bodies, and neutralizes formula prefixes. Existing schema version 4 supports these records; historical migrations are unchanged. Request evidence continues to accumulate without automatic retention cleanup.
