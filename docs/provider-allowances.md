# Provider quota and entitlements

Open **Providers → Quota and entitlements**. This manages upstream accounts. Customer and team budgets remain under **Quota policies**.

## Collection

Quota is cached independently of inference health. Gateway requests never call a quota endpoint. A cluster-coordinated worker checks due accounts with four concurrent collectors. Custom refresh intervals range from 30 seconds to one day; the default is 60 seconds. Failed reads retain the previous successful values and timestamps. Routing distinguishes available, exhausted, unknown and query failure. Observations older than ten minutes, missing required windows and passed reset times cannot establish spare capacity.

Subscription providers use their existing quota adapters. API-key accounts can enable a separate collector. Presets cover Fusecode, DeepSeek, Moonshot, OpenRouter, StepFun, AiHubMix, ModelScope, GLM/Z.ai, Kimi Code, OpenCode Go, MiniMax, Command Code, Ollama, Mistral, NewAPI, Sub2API, Volcengine and Alibaba/Tencent cloud plans. Presets are reading rules, not a guarantee that a provider grants every key quota access. Cloud plan queries require account access keys and the relevant cloud permissions. Tencent requires an explicit TeamId so independent plans are not added together. Mistral queries the key kind before selecting its API or Vibe budget. StepFun's web plan query requires a valid session in encrypted request headers.

For Fusecode, select its preset or enter your Magpie rules:

```text
URL: https://fusecode.ai/v1/usage
Expression: $remaining; Week: subscription.weekly_usage_usd / subscription.weekly_limit_usd %; Month: subscription.monthly_usage_usd / subscription.monthly_limit_usd %
```

The expression reader supports labeled parts, currencies, percentages, arithmetic, collection filters and `sum`, `min`, `max`, `count`. One missing part stays unknown while readable parts remain visible. A completely unreadable response is a query failure. Structured windows additionally preserve amounts, limits, units, reset/expiry times, model include/exclude rules and pool membership. No reset date is invented when a supplier does not return one.

Use the account key, a separate encrypted quota token, or encrypted custom headers. `{key}` substitutes the runtime key in an endpoint or header. Saved header values are redacted in read APIs; leaving the separate token blank retains it. Explicitly select its removal to delete it. Outbound requests use the gateway's existing URL, DNS, redirect and proxy guards. Cloud signing uses fixed control-plane hosts. Alibaba console tokens are encrypted and cached for twenty minutes.

## Usage and entitlement policies

Model routing and account routing remain separate. Account policies only reorder or hold candidates already authorized for the current project/key, route, model and priority tier. The four strategies are quota smart, expiry first, least used and until exhausted. The default keeps the established order.

Account and per-window caps can hold an account before its supplier limit. Amounts stay in their reported unit. Informational windows are excluded unless explicitly capped; pool caps apply only to their member models. Wallet amount caps use durable observed spending counters. Top-ups do not erase spending, and stale readings, restarts or history pruning cannot rewind a counter. Windows without a reset use a UTC day.

Credit policies are existing behavior, never, allow, first and last. Explicit first/last ordering stays within the same authorized route and priority tier. User caps cannot be bypassed by credits or automatic resets.

Automatic Codex resets are off by default. Enable weekly exhaustion, impending card expiry, or both. Weekly exhaustion additionally requires an actual exhausted invocation and a recent intent containing only that request's authorized candidates; an available or unknown alternative prevents spending. Expiry handling requires used windows and prefers the earliest expiring card. The default lead is thirty minutes and the default weekly automatic limit is one. Durable reservations and the existing reconciled reset executor prevent duplicate spending. An uncertain outcome stops automatic retries. Operation records remain visible in the console.

Manual wallet estimates are computed in the background and read from a revision-fenced cache. Failed computations retain the last value and its age; stale or uncollected estimates remain unknown. Manual wallets track a starting balance, expiring grants and optional recurring refills. They consume expiring grants first and can carry unused balance forward. Estimates deduct recorded provider costs only; spending outside this gateway is absent. Non-USD wallets require an explicit conversion rate. Shared wallets must be selected explicitly and are pinned to the source account's identity. Equal balances do not establish sharing. Shared manual wallets count gateway spending across linked accounts; model limits remain independent. Replacing account credentials or an API key clears the old quota configuration and observations.

## History, alerts and API

Changed successful observations are retained for 45 days. The console plots each window separately and estimates wallet run-out from observed decreases. Currencies and credit units are never added together. High usage, low balance and unused quota before reset can produce alerts. Alerts rearm after recovery or top-up and are deduplicated within a cycle. Select existing notification channels for delivery; otherwise events stay in the alert console. Delivery is claimed durably before sending, with no automatic duplicate retry after an uncertain outcome.

Administrator APIs require provider permissions:

- `GET /api/admin/provider-allowances` and `/presets`: cached accounts and collection presets.
- `GET|PUT /api/admin/provider-resources/{id}/allowance/config`: redacted configuration with revision-based updates.
- `GET /api/admin/provider-resources/{id}/allowance/history` and `/events`: bounded history and operation records.
- `POST /api/admin/provider-resources/{id}/allowance/refresh`: explicit upstream refresh.

Project API keys can read `GET /v1/allowance?model=<alias>`, optionally with `wait_seconds=0..60`. This returns aggregate cached availability for authorized routes, without supplier identities, wallet amounts or configuration. Waiting never queries the supplier.

Schema expansion 12 adds indexed settings, sessions, history, entitlement events and wallet counters. It is additive and does not alter the frozen adoption baseline. No new environment variables are required. See [Database evolution](database-evolution.md).

## Magpie provenance

The expression, plan-response and cloud-signing readers reference Magpie commit `47b51c8225a66e753dd249cb782472b0dcbee6c4`. Account storage, project authorization, background jobs, counters, database coordination and UI are TokenHub adaptations. Desktop sign-in switching, the system tray and Magpie's local CLI are outside the server quota module. See [third-party notices](../backend/internal/server/SUBSCRIPTION_THIRD_PARTY_NOTICES.md).
