# Provider quota and entitlements

Open **Providers → Quota and entitlements**. This manages upstream accounts. Customer and team budgets remain under **Quota policies**.

Provider lists and availability monitoring show quota exhaustion separately from service faults. Explicit quota or balance denials still fail the request and allow failover, but do not add resource breaker failures or mark the provider unhealthy. Cached quota marks a whole provider exhausted only when every active account has fresh, account-wide exhaustion; missing, stale, model-only or available alternatives prevent that conclusion. A newer network or authentication failure retains its fault status.


## Collection

Quota is cached independently of inference health. Ordinary requests read cached quota; explicitly enabling `capCareful` permits Magpie-style checks before a request. A cluster-coordinated worker checks due accounts with four concurrent collectors. Custom refresh intervals range from 30 seconds to one day; the default is 60 seconds. Failed reads retain the previous successful values and timestamps. Routing distinguishes available, exhausted, unknown and query failure. Observations older than ten minutes, missing required windows and passed reset times cannot establish spare capacity.

Subscription providers use their existing quota adapters. API-key accounts discover balance queries automatically and can configure a separate collector. Presets cover Fusecode, DeepSeek, Moonshot, OpenRouter, SiliconFlow, StepFun, AiHubMix, ModelScope, GLM/Z.ai, Kimi Code, OpenCode Go, MiniMax, Command Code, Ollama, Mistral, NewAPI, Sub2API, Volcengine and Alibaba/Tencent cloud plans. Presets are reading rules, not a guarantee that a provider grants every key quota access. Cloud plan queries require account access keys and the relevant cloud permissions. Tencent reads the native plan list and each plan's details, retaining list values if a detail query fails. Mistral queries the key kind before selecting its API or Vibe budget. StepFun's web plan query requires a valid session in encrypted request headers.

For Fusecode, select its preset or enter your Magpie rules:

```text
URL: https://fusecode.ai/v1/usage
Expression: $remaining; Week: subscription.weekly_usage_usd / subscription.weekly_limit_usd %; Month: subscription.monthly_usage_usd / subscription.monthly_limit_usd %
```

The expression reader supports labeled parts, currencies, percentages, arithmetic, collection filters and `sum`, `min`, `max`, `count`. One missing part stays unknown while readable parts remain visible. A completely unreadable response is a query failure. Structured windows additionally preserve amounts, limits, units, reset/expiry times, model include/exclude rules and pool membership. No reset date is invented when a supplier does not return one.

Use the account key, a separate encrypted quota token, or encrypted custom headers. `{key}` substitutes the runtime key in an endpoint or header. Saved header values are redacted in read APIs. Renaming a saved header requires re-entering its value; leaving the separate token blank retains it. Explicitly select its removal to delete it. Outbound requests use the gateway's existing URL, DNS, redirect and proxy guards. Cloud signing uses fixed control-plane hosts. Alibaba console tokens are encrypted and cached for twenty minutes.

### Migrate Magpie balance and quota settings directly

Magpie `47b51c8` defines the field syntax, defaults and quota behavior. The primary form keeps Balance URL, GET/POST, Balance field and Account balance. No compatibility switch is required. Additional collection controls are collapsed.

API-key accounts discover known balance/plan endpoints from their provider address. Unknown relays are queried at `/v1/usage`, then `/api/usage/token`. Hits last 24 hours, misses six hours, and transport failures five minutes. The shared encrypted cache is fenced by account identity and config revision. `noBalanceProbe` disables relay probing. Check balance rechecks the current form and can fill the discovered URL without saving observations, history or entitlement settings.

Import Magpie quota settings accepts a complete `providers.json`, a provider array or one provider, plus optional `settings.json`. Select the provider and account. Without an explicit account, match the current API key's Magpie ID (the first five SHA-256 bytes) or account name; ambiguous configurations require selection. Import fills the form; saving applies it.

| Native fields | Migrated behavior |
| --- | --- |
| `balanceURL`, `balancePath`, `balanceMethod`, `balanceToken`, `headers`, `noBalanceProbe` | Original endpoint, expression, method, account token, headers and probe switch |
| `accountCaps`, `accountWindowCaps`, `accountWindowAmounts` | Account percentages and per-window shares or amounts; 100 removes the user cap on that window |
| `capStop`, `capCareful` | Stop with 429 when all accounts are held; refresh near caps and hold unknown readings |
| `keySpendCaps` | Local calendar day/month or total spending; raising a total limit preserves its origin |
| `keyOwnBalances` | USD/CNY balances, weekly/monthly refills and expiring grants with their original start times |
| `codexNoCredits`, `codexCreditsFirst`, `codexCreditsLast` | Reported credits allowed by default, or disabled/first/last |
| `codexAutoReset`, `codexAutoResetOnly`, `codexResetLead` | Exhaustion/expiry resets and lead time in minutes; off by default, one automatic reset per week |
| `usageAlert`, `balanceAlert`, `resetReminder` | Usage, balance and reset reminders; zero disables each, reminders use hours |
| `accessKeyID`, `secretAccessKey` | Separate encrypted Volcengine, Tencent and Alibaba plan credentials |

Account tokens retain explicit schemes; JWTs gain Bearer, opaque tokens are verbatim, and tokens equal to the inference key use key authentication. All Magpie key-placeholder aliases are retained, including `{token}`, which names the inference key. POST has no body by default. Known relay fields can be blank. Account URLs fall back to the corresponding key URL without an account token or explicit Authorization/Cookie header.

Calendar refills restore the configured balance at local midnight, without carry-over, clamping missing month days to month end. Grant expiry dates include that entire day; grants are consumed by earliest expiry. Manual balances may be negative and fall back behind fresh vendor money balances. CNY uses configured exchange rates; spending with no rate remains unknown.

Ordinary requests read cached quota. Opt-in `capCareful` refreshes a reading older than 15 seconds within five percentage points of its cap, or any reading older than five minutes, waiting at most ten seconds and retrying failed reads no faster than every 15 seconds. Alerts tolerate ten-minute reset drift, deduplicate each cycle and include expiring reset cards. Tencent without TeamId uses a sole plan for account caps. Multiple plans remain separate informational windows; an explicit TeamId binds one plan to account caps.

Import does not replace inference keys or routes. TokenHub supplies project authorization, encryption, shared persistence and guarded HTTP. Desktop notifications use existing notification channels. Imported enabled entitlement policies apply after saving. Development and acceptance use synthetic accounts only.


## Usage and entitlement policies

Model routing and account routing remain separate. Account policies only reorder or hold candidates already authorized for the current project/key, route, model and priority tier. The four strategies are quota smart, expiry first, least used and until exhausted. The default keeps the established order.

Account and per-window caps can hold an account before its supplier limit. Window cap names must be unique after trimming whitespace and ignoring case; duplicates block saving and retain the form. Amounts stay in their reported unit. Informational windows are excluded unless explicitly capped; pool caps apply only to their member models. Wallet amount caps use durable observed spending counters. Top-ups do not erase spending, and stale readings, restarts or history pruning cannot rewind a counter. Windows without a reset use a UTC day.

Reported credits are allowed by default; policies also support never, first and last. Explicit first/last ordering stays within the same authorized route and priority tier. User caps cannot be bypassed by credits or automatic resets.

Automatic Codex resets are off by default. Enable weekly exhaustion, impending card expiry, or both. Weekly exhaustion additionally requires an actual exhausted invocation and a recent intent containing only that request's authorized candidates; an available or unknown alternative prevents spending. Expiry handling requires used windows and prefers the earliest expiring card. The default lead is thirty minutes and the default weekly automatic limit is one. Durable reservations and the existing reconciled reset executor prevent duplicate spending. An uncertain outcome stops automatic retries. Operation records remain visible in the console.

Manual wallet estimates are computed in the background and read from a revision-fenced cache. Failed computations retain the last value and its age; stale or uncollected estimates remain unknown. Manual wallets track a starting balance, expiring grants and optional recurring refills. They consume expiring grants first. Calendar refills do not carry balances forward; older interval configurations retain their carry-over option. Estimates deduct recorded provider costs only; spending outside this gateway is absent. CNY wallets use the configured exchange rate or remain unknown after spending when no rate is available. Other units require an explicit conversion rate. Shared wallets must be selected explicitly and are pinned to the source account's identity. Equal balances do not establish sharing. Shared manual wallets count gateway spending across linked accounts; model limits remain independent. Replacing account credentials or an API key clears the old quota configuration and observations.

## History, alerts and API

Changed successful observations are retained for 45 days. The console plots each window separately and estimates wallet run-out from observed decreases. Currencies and credit units are never added together. High usage, low balance and unused quota before reset can produce alerts. Alerts rearm after recovery or top-up and are deduplicated within a cycle. Select existing notification channels for delivery; otherwise events stay in the alert console. Delivery is claimed durably before sending, with no automatic duplicate retry after an uncertain outcome.

Administrator APIs require provider permissions:

- `GET /api/admin/provider-allowances` and `/presets`: cached accounts and collection presets.
- `GET|PUT /api/admin/provider-resources/{id}/allowance/config`: redacted configuration with revision-based updates.
- `GET /api/admin/provider-resources/{id}/allowance/history` and `/events`: bounded history and operation records.
- `POST /api/admin/provider-resources/{id}/allowance/refresh`: explicit upstream refresh.
- `POST /api/admin/provider-resources/{id}/allowance/preview` and `/import-magpie`: query the form or stage native settings without saving.

Project API keys can read `GET /v1/allowance?model=<alias>`, optionally with `wait_seconds=0..60`. This returns aggregate cached availability for authorized routes, without supplier identities, wallet amounts or configuration. Waiting never queries the supplier.

Schema expansion 12 adds indexed settings, sessions, history, entitlement events and wallet counters. It is additive and does not alter the frozen adoption baseline. No new environment variables are required. See [Database evolution](database-evolution.md).

## Magpie provenance

The expression, plan-response and cloud-signing readers reference Magpie commit `47b51c8225a66e753dd249cb782472b0dcbee6c4`. Account storage, project authorization, background jobs, counters, database coordination and UI are TokenHub adaptations. Desktop sign-in switching, the system tray and Magpie's local CLI are outside the server quota module. See [third-party notices](../backend/internal/server/SUBSCRIPTION_THIRD_PARTY_NOTICES.md).
