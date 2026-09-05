# Time and cache pricing

## Current behavior

Model and Provider model pricing periods support `weekdays` (0 = Sunday, 6 = Saturday), IANA timezones, RFC3339 effective ranges, and overrides for input, cache read, generic/5-minute/1-hour cache writes, and output. Omitted weekdays mean every day. Overnight windows belong to their starting weekday. Boundaries include the start and exclude the end; identical start/end clocks mean the entire selected day.

Configure no more than 64 periods. Overlapping weekly windows are rejected when their effective ranges overlap. Such ranges must use one timezone. Defaults cover times outside the configured windows. An omitted period price inherits its default; an explicit zero is free.

The Model Directory cache-read field now preserves explicit zero through save and reload. HTTP model PATCH preserves omitted prices. Use `cache_read_price_usd_per_1m: null` to restore legacy estimation, or `metadata.cache_read_price_configured: "false"` with a zero price. Updating unrelated metadata preserves the configured-free state. Cache-write PATCH accepts zero as free and null as inherited. Token quotas and TPM continue to count actual tokens independently of price discounts.

Tenant legacy pricing remains tied to admission. Routed Provider costs capture the Provider model configuration before each attempt, so an in-flight edit cannot change that attempt's cost. Missing Provider prices remain explicit in the new evidence instead of inventing a known cost.

## Exact shadow pricing

Platform administrators can open **Billing → Exact pricing and shadow comparison**, select a tenant model or Provider/model pair, enter decimal-string rates and time windows, preview a specific instant, and publish a new immutable shadow version. Preview success is required before publishing from the form. Changing the form invalidates that preview. Select at least one weekday in each form window.

Shadow rates do not replace current charges or reserve budget. They record comparisons for new requests. Legacy configurations are identified as `legacy_float_configuration`; converting them to decimal strings does not create historical exact-price evidence. Adapter usage currently remains unverified for field presence and provider charging-time rules. Contradictory usage, missing rates, missing usage, or uncertain delivery produces pending evidence. No supplier reconciliation claim is implied.

Rates allow at most 18 integer digits and 12 fractional digits. Charges sum exact rational components before rounding once to 12 decimals using half-even. Currency conversion uses the unrounded sum. Missing FX leaves the original-currency amount available and USD absent. Tenant cards use USD. All six tenant rate fields are currently required; a distinct not-applicable category is not yet supported.

Administrator-only endpoints:

| Method | Path | Purpose |
| --- | --- | --- |
| GET, POST | `/api/admin/billing/rate-cards` | List or publish immutable shadow cards |
| POST | `/api/admin/billing/preview` | Preview `{card, at, usage, exchange_rate}` |
| POST | `/api/admin/billing/exchange-rates` | Publish `{currency, rate, source, effective_from?}`; one original currency unit equals `rate` USD |
| GET | `/api/admin/billing/evidence/{request_id}` | Read admission, prepared attempts, and shadow settlement evidence |

A card contains `kind` (`tenant` or `provider`), `target` (model name or `provider_id:upstream_model`), `currency`, `source`, optional `effective_from`, `rates`, and optional `periods`. Rate keys are `input`, `cache_read`, `cache_write`, `cache_write_5m`, `cache_write_1h`, and `output`. Periods combine the schedule fields above with a `rates` override object. The latest applicable effective time wins; immediate publications sharing a database timestamp use the card revision. Existing snapshots retain their original version. FX selection is frozen with each Provider attempt. Publishing a rate or FX version records an administrator audit event.

New evidence captures project/key IDs and names, user/team/cost-center IDs, UTC admission day/month, and per-attempt Provider/resource identity, prices, FX, upstream request ID and outcome. It stores no prompt, response body or credentials. A prepared attempt without completion evidence means possibly sent; it must not be automatically retried or treated as free. Shadow settlement is committed in the existing request settlement transaction, with one base record per server request ID.

## Rollout and remaining work

Schema expansion 4 adds `metering_entries` and an index without rewriting historical usage or baseline migrations. Runtime compatibility requires schema 4; normal startup upgrades older databases. Restore/rollback must preserve the evidence table and use a compatible binary. Evidence currently has no automatic retention cleanup.

This release is a pricing and shadow-evidence increment. Durable all-scope money reservations, active exact charging, protocol field-presence evidence, crash recovery and the 24-hour manual queue, immutable adjustments, ordinary-user evidence views, not-applicable categories, and links to existing reconciliation workflows remain follow-up work. The reservation rounding helper is tested but is not an active admission control. Background transports that bypass the routed attempt loop do not yet capture equivalent per-attempt evidence. No production PostgreSQL concurrency or supplier-bill equivalence has been validated by this increment.

Model price updates reject non-finite or negative base prices. Legacy Provider zero values without configuration evidence remain unknown per usage category, including inherited cache-write prices; an explicit shadow card or time-window override can declare a free rate. Missing legacy prices leave `legacy_usd` empty even when an exact card supplies a known shadow charge. `tokenhub db verify` validates the expanded schema, including the evidence table and index, while adoption still checks the unchanged historical baseline.
