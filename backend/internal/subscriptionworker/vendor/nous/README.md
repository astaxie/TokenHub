# @magpie-community/opencode-nous-auth

Signs in to [Nous Portal](https://portal.nousresearch.com) with its OAuth
Device Authorization flow and sends model requests in
[magpie](https://usemagpie.ai) and [OpenCode](https://opencode.ai). Provider id:
`nous-portal`. This is separate from magpie's `nous` API-key preset.

## Signing in

Once the package is available from magpie's plugin registry:

```sh
magpie plugin add @magpie-community/opencode-nous-auth
magpie plugin login nous-portal
```

Magpie displays a verification URL and a code. Open the page, sign in and
approve the code there; finish any CAPTCHA in your browser. The plugin polls
for approval and completes sign-in without a local callback server.
Hermes Agent does not need to be installed or running.

In OpenCode, add `"@magpie-community/opencode-nous-auth"` to the `plugin`
array in `opencode.json`, then run `opencode auth login`.

The flow uses Nous Portal's `/api/oauth/device/code` and `/api/oauth/token`
with public client `hermes-cli`, scope `inference:invoke`, and no client
secret. Device polling follows [RFC 8628](https://www.rfc-editor.org/rfc/rfc8628.html#section-3.5),
including the server's interval and `slow_down` responses. Nous has not
confirmed whether independent clients may use the public `hermes-cli`
OAuth client ID.

For ChatGPT plan sharing, see [Nous Portal's documentation](https://portal.nousresearch.com/help/chatgpt-plan-sharing).

## Where the sign-in is kept

Magpie keeps the account in its plugin credential store; OpenCode uses its own
sign-in store. The saved OAuth fields are `access` (inference JWT), `expires`
(epoch milliseconds) and `refresh` (versioned JSON containing the refresh
token, scope, token type and inference endpoint). **The entire `refresh` value
is secret**, even though it looks like JSON.

Expiry uses the earlier of the token response's lifetime and the JWT's `exp`.
If both are absent, renewal is scheduled one hour after the exchange, with
the same two-minute lead. This is a local fallback, not a Nous lifetime
guarantee; a 401 can still trigger renewal sooner. Explicitly expired or
malformed lifetimes are not extended.

Nothing is read from or written to `~/.hermes/auth.json`. A separate device
sign-in is intentional: Nous refresh tokens are single-use, and copying one
from Hermes could invalidate Hermes's session when either program renews it.

Magpie uses `auth.refresh` with a two-minute lead and saves the rotated
tokens. OpenCode does not call that hook, so the plugin also checks before
each request and persists renewals through `client.auth.set`. Concurrent
refreshes within one plugin instance share one exchange. A rotated pair is
kept in memory before a save is attempted, so a failed save does not cause
that instance to reuse the old refresh token.

**Do not run multiple hosts against the same saved account.** The per-request
save is not an atomic cross-process operation; a crash during an unsaved
rotation may require signing in again. Only explicit `invalid_grant`,
`invalid_token` or `refresh_token_reused` errors mark the grant expired.
Other refresh refusals, including HTML or empty 401/403 responses, keep the
credentials and allow a later retry.

## Requests

Models use OpenAI Chat Completions (`@ai-sdk/openai-compatible`) at
`https://inference-api.nousresearch.com/v1`. Magpie translates requests from
other agent protocols. Every request uses the current inference JWT as a
Bearer token; the refresh token is sent only to Portal's token endpoint in
`x-nous-refresh-token`, never to inference.

The plugin accepts only known inference paths (`/models`, `/chat/completions`,
`/completions`, `/embeddings`) at the production Nous inference or welcome
host. A Portal response cannot redirect Bearer tokens to an arbitrary host;
HTTP redirects are refused. Caller-supplied credentials and magpie-internal
headers are removed before forwarding.

A 401 triggers at most one refresh and retry. Other upstream errors keep
their status, and streaming responses pass through without buffering or
rewriting their content. The upstream `content-encoding` and `content-length`
headers are removed because fetch may already have decoded the body.

## Models

After sign-in, the account's live `/v1/models` list replaces the single
bootstrap entry (`Hermes-4-70B`). Model ids are preserved, including the
vendor prefix such as `openai/gpt-6-luna`. In magpie an exposed model is
addressed as `nous-portal/openai/gpt-6-luna`.

Magpie may initially expose only part of the catalog. To choose a model
listed for your account and test it:

```sh
magpie provider refresh nous-portal
magpie provider models nous-portal +openai/gpt-6-luna
magpie provider test nous-portal openai/gpt-6-luna
```

The `provider test` command takes the upstream id (`openai/gpt-6-luna`), not
the magpie-prefixed id. Model availability and billing depend on the account;
a model appearing in `/models` does not guarantee access. The plugin does
not guess model prices, context limits or vision/reasoning capabilities when
the catalog does not provide them. A failed catalog read does not become an
empty entitlement list.

## Not included

- Nous Portal API keys (`nous` remains magpie's separate built-in preset).
- Importing Hermes credentials or running Hermes's subscription proxy.
- Direct ChatGPT/Codex OAuth, billing or quota reporting, or daily check-in.
- An arbitrary inference URL override or cross-process token coordination.

## Test

Run the offline checks from the repository root with a temporary HOME:

```sh
h=$(mktemp -d)
HOME="$h" node --test packages/nous/index.test.mjs
HOME="$h" bun test
rm -rf "$h"
bun scripts/check.mjs
```

Before publication, try the local package with magpie's isolated test script:

```sh
export MAGPIE=/absolute/path/to/magpie
sh scripts/try.sh nous
sh scripts/try.sh nous login nous-portal
sh scripts/try.sh nous provider refresh nous-portal
sh scripts/try.sh nous provider test nous-portal openai/gpt-6-luna
```

`try.sh` uses `.sandbox/nous` rather than your real magpie sign-ins. It clears
ordinary environment variables, including proxy settings; configure a proxy
inside the sandbox if needed. Do not use a portable Windows magpie binary
that ignores HOME isolation. Sign-in and `provider test` contact Nous Portal;
the latter may consume account credits. Test a model your account can use,
and never share the sandbox's credential files.

Protocol reference: Hermes Agent's
[device flow](https://github.com/NousResearch/hermes-agent/blob/8b9ac5773ea07189f23aa9dfcdba1e59fd759c21/hermes_cli/auth_device_flow.py),
[refresh handling](https://github.com/NousResearch/hermes-agent/blob/8b9ac5773ea07189f23aa9dfcdba1e59fd759c21/hermes_cli/auth_nous.py)
and [Nous Portal proxy adapter](https://github.com/NousResearch/hermes-agent/blob/8b9ac5773ea07189f23aa9dfcdba1e59fd759c21/hermes_cli/proxy/adapters/nous_portal.py).
