# @magpie-community/opencode-cline-auth

Cline (cline.bot) on your own Cline account: the models Cline's gateway
serves — usage-billed models, ClinePass's and Cline's free models alike.
Provider id: `cline`.

## Signing in

- **Cline device sign-in** — the flow Cline's own client runs: the plugin asks
  WorkOS for a device code and opens the approval page, polls until you
  approve, then trades the WorkOS tokens at Cline's `/api/v1/auth/register`
  for the account's own token pair.
- **Cline API key** — create one at [app.cline.bot → Settings → API Keys](https://app.cline.bot/dashboard/account)
  and paste it. Sent as a plain Bearer key.

The sign-in is kept where OpenCode keeps auth (`auth.json` under the data
directory); magpie keeps plugin sign-ins in its own store
(`~/.config/magpie/plugin-auth.json`).

Refreshing:
- Access tokens are renewed through `/api/v1/auth/refresh`. Cline rotates
  the refresh token and refuses one already spent, so the rotated pair is
  kept in memory beside the store the moment it arrives: a save that fails
  can't leave the next request spending the token that was already spent.
- magpie renews the token ten minutes before it ends, through the plugin's
  `auth.refresh`, once for the account and before its requests, models and
  usage need it; magpie saves the new pair. OpenCode doesn't call that hook:
  there the token is refreshed five minutes before it ends, before a
  request, and the new pair is saved.
- Refreshes are serialized, magpie's renewal included, so two at once can't
  spend one token and lose the other; one that finds the token already
  renewed uses that pair.
- A refresh that fails for a while keeps the token that is still good, as
  Cline's own client does (magpie tries its renewal again shortly); only a
  refresh Cline refuses, or a token that has actually expired, is treated as
  the sign-in gone.

## Requests

Chat completions go to `https://api.cline.bot/api/v1/chat/completions` in
OpenAI's format (streaming included), with `Authorization: Bearer
workos:<access token>` for a signed-in account or the raw API key, and the
full client-identity header set Cline's own clients send (`HTTP-Referer`,
`X-Title`, `User-Agent: Cline/<version>`, `X-CLIENT-TYPE: cline-cli`,
`X-CLIENT-VERSION`, `X-PLATFORM`, `X-PLATFORM-VERSION`, `X-CORE-VERSION`,
`X-IS-MULTIROOT`) — pinned to Cline's current CLI release, which is also
what the free models' gate reads; when Cline ships a new release, move the
pinned numbers in `index.mjs` along. All models use one `X-Task-ID` per
named chat session, derived as a UUID from the session. With no named session
(including magpie's synthetic id from the first user message), no task id
is sent. The session crosses the plugin's hooks in a private header that is
removed before the request goes to Cline. Auth and account requests carry
the same identity. A finished non-streaming answer rides in a
`{"data": …}` envelope the official clients take apart; the plugin takes it
apart too. The model list is read from
`/api/v1/ai/cline/recommended-models` and `/api/v1/ai/cline/models` (neither
needs a sign-in); usage reads `/api/v1/users/me` and the account's `/balance`
(its balance is counted in millionths of a dollar), and ClinePass's 5-hour,
weekly and monthly limits from `/api/v1/users/me/plan/usage-limits`
(`{limits: [{type, percentUsed, resetsAt}]}`, what app.cline.bot's
subscription page shows). An account without ClinePass (a 404 there) has
none; limits that fail to read for any other reason say so on the card beside
the balance, and limits read are kept when the balance can't be.

### DeepSeek models only through DeepSeek's own API

Cline's gateway picks among the providers that serve a model. On ClinePass
its own routing for a model wins over `only` alone, so since 0.1.11 a request
for a ClinePass DeepSeek model (`cline-pass/deepseek-…`) carries, by default:

```json
"providerOptions": {"gateway": {"only": ["deepseek"], "has": ["vision", "implicit-caching"]}}
```

`has` is the gateway's capability filter: the upstream must cache a repeated
prefix by itself, and take images — `vision` is asked only of a model Cline's
catalog says takes images (or a request carrying one), never of the text-only
`deepseek-v4-pro`. With it the request is answered by DeepSeek's own API
(hy125245 on X: `finalProvider` `deepseek`, and a fake upstream in `only`
refused with "no matching upstream"). A pinned request that fails with no
upstream matching it, or with a 5xx, goes once more as sent, so the pin
never fails a request that would have worked; a refusal of the account (401,
402, 403, 429) is answered as it is. A request with `provider` or
`providerOptions` of its own — magpie's extra body, say — goes as sent. Set
`pinUpstream` to `false` to send ClinePass's DeepSeek models unpinned.

With the `pinUpstream` option on, a request for any DeepSeek model (not the
`cline-free/` ones) is pinned too, the ClinePass ones as above without the
way back, the others with `"providerOptions": {"gateway": {"only":
["deepseek"]}}`, so it is answered by DeepSeek's own API only, and one for a
GLM model (`cline-pass/glm-…`) carries `["zai"]`, so it is answered by Z.ai's
own API only — the same as magpie's built-in ClinePass tick. Cline sends
`glm-5.3-flash` through OpenRouter rather than Vercel's gateway, and OpenRouter
ignores `providerOptions`, so that model's request carries OpenRouter's own
`"provider": {"only": ["z-ai"]}` instead (since 0.1.10). Other models, and a
request with `provider` or `providerOptions` of its own, go as sent. It is off
by default. Turn it on:

- in magpie: the provider's editor › Upstream › tick "DeepSeek and GLM
  models only from their makers' own APIs";
- in OpenCode: `"provider": {"cline": {"options": {"pinUpstream": true}}}` in
  `opencode.json`, or the plugin's own options:
  `"plugin": [["@magpie-community/opencode-cline-auth", {"pinUpstream": true}]]`.

## Models

The list is Cline's own, merged from the two feeds its clients read: the
recommended-models feed (its recommended picks, its free models and
ClinePass's) and the whole cloud catalog (`/ai/cline/models`, where the
usage-billed models the recommended feed doesn't name live). Refreshed from
the live feeds whenever they answer; until then a bundled snapshot of the
recommended list, Cline's own default (`anthropic/claude-sonnet-5`) among it.
Cline's `clineCloud` group is left out, as Cline's own clients leave it out
unless they opt in.

Each model's context window is the cloud catalog's (`context_length`). Its
reply limit is left unsaid: the catalog's `max_completion_tokens` can be most
of the window, and an agent keeps that much free for the reply. The `cline-free/…`
and `cline-pass/…` models aren't in that catalog, so they say none, and magpie
takes the window models.dev gives the model after the prefix
(`cline-free/mimo-v2.6-flash` is `mimo-v2.6-flash`).

- The **free** group (`cline-free/…`, `stealth/…`) costs nothing — the names
  say "(free)". They are gated on the client surface a request claims; the
  plugin claims what it is, and the gate takes it. If Cline ever narrows that
  gate to its own apps, those models go away.
- The **ClinePass** group (`cline-pass/…`) needs a ClinePass subscription —
  the names say so, and asking without one answers 403.
- The rest are usage-billed against the account's Credits balance.

## Not included

- Cline's MCP and web-search endpoints — a chat provider only.
- Team/organization account switching.
