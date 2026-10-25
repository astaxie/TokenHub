# @magpie-community/opencode-workbuddy-auth

Signs in to the plans of WorkBuddy, Tencent's desktop agent from CodeBuddy.
It has one provider for each of WorkBuddy's two builds:

| Provider id | Build | API |
|---|---|---|
| `workbuddy` | WorkBuddy, the China build (CodeBuddy plan) | `https://copilot.tencent.com/v2` |
| `workbuddy-ai` | WorkBuddy AI, the international build | `https://www.workbuddy.ai/v2` |

The package exports `WorkBuddyAuthPlugin` (`workbuddy`) and
`WorkBuddyAIAuthPlugin` (`workbuddy-ai`).

## Signing in

- **Account (browser).** Opens Tencent's sign-in page for the build, the
  one WorkBuddy's desktop app opens. The plugin then asks WorkBuddy for the
  token every second until you're signed in, for 5 minutes at most. Nothing
  needs to be pasted.
- **Desktop's sign-in.** Uses the account WorkBuddy desktop is signed in
  to. Nothing is copied: the app's file is read on each request, and never
  changed:
  - macOS: `~/Library/Application Support/CodeBuddyExtension/Data/Public/auth/workbuddy-desktop[-ai].info`
  - Windows: `~/AppData/Local/CodeBuddyExtension/…`
  - Linux: `~/.local/share/CodeBuddyExtension/…`

  WorkBuddy 5.6 and later keep the sign-in encrypted, which only the app
  opens, so this way fails there and says so: use the browser sign-in.

A browser sign-in is kept as an OpenCode `oauth` auth: the access and
refresh tokens, their expiry, the user id and the domain; desktop's as a
marker, `{"type": "oauth", "source": "desktop", "accountId", "uid"}`. OpenCode keeps it in
`auth.json`; magpie keeps it in `plugin-auth.json`. The access token is
refreshed a minute before it ends, and the new one is saved. If the refresh
fails, the old token is used while it lasts.

In magpie (0.1.684 or later) the plugin also gives `auth.refresh` with
`refreshLead` of 5 minutes: magpie renews a browser sign-in (an hour's
token) 5 minutes before it ends, once for the account, before its
requests, models and usage ask, and saves what it gives. It goes through
the same one-refresh-at-a-time as a request's refresh, so a refresh token
is never spent twice. A failed renewal, a refresh token past its end
included, doesn't mark the account, as the built-in never did; magpie
tries again later, and the old token is used meanwhile.
OpenCode doesn't call `auth.refresh`; the check before each request stays.

An enterprise (team) account is asked for as the enterprise. A browser
sign-in keeps the enterprise id WorkBuddy names for the account (from
`/login/account`, else its account list), and desktop's is read from the
app's file. Every request then carries `X-Enterprise-Id` and `X-Tenant-Id`,
as WorkBuddy's own client sends them; without them WorkBuddy answers as the
personal account, with the personal plan's models. A browser sign-in made
before 0.1.13 has no enterprise id kept: sign in again.

Desktop's sign-in is refreshed only when the app hasn't refreshed it
itself, and the new token is kept in memory, never written back, as magpie's
built-in WorkBuddy does.

A chat or usage read WorkBuddy answers 401 (since 0.1.31) is renewed once,
whatever the token's time, and sent once more. A token another process
saved since is taken first instead of spending the refresh token again. A
desktop sign-in is never renewed for it: the app's file is read again, and
its token is used if the app signed in again. A 401 again, or no new
token, ends in a 401 that says to sign in again (Providers → WorkBuddy →
this account's Sign in again in magpie; WorkBuddy desktop for a desktop
sign-in). The plugin marks the account expired in magpie
(`X-Magpie-Sign-In: expired`, usage's `signIn`) only on WorkBuddy's word
that the sign-in is gone: the refresh token refused (401, `invalid_grant`),
or the access token refused (`WWW-Authenticate: … error="invalid_token"`)
with no refresh token left to renew it with or just after renewing. A
failed renewal for another reason, or a bare 401 without `invalid_token`,
leaves the account as it is; a desktop sign-in is never marked. A 401
after a renewal takes a mark off (`renewed`).

## Requests

Chats are chat completions (`@ai-sdk/openai-compatible`) at `<API>/chat/completions`.
They are streamed only; WorkBuddy refuses a chat that isn't.

**On WorkBuddy (China), the Claude models are served on Anthropic's Messages
API** instead: `@ai-sdk/anthropic` at `<endpoint>/v1/messages` (streamed only
as well). `claude-opus-5.5`, `claude-sonnet-5.5`, `claude-opus-5` and
`claude-opus-4.8-1m` answered there when each model of `/v3/config`'s `cli`
agent was asked on 2026-10-24; `glm-5.3` and `deepseek-v4.1-flash` answered
500, `gpt-5.5` and `kimi-k3-1` 400, so every other model stays on chat.
WorkBuddy AI has no such route (`/v1/messages` is `404 Route Not Found` there
for every model), so all its models stay on chat, a Claude one too.

A Messages request goes as the agent sent it: no default system message, no
`max_completion_tokens`/`developer` rewrites, no tool reshaping, and nothing
moved for a refused system prompt — Claude Code's own prompt is taken as it
is, billing line and all, where a chat answers 11128 to it. One thing is
left out: a `thinking` block with no `signature`. A conversation that began
on chat (or on another provider) carries such blocks, and once a signed one
from this route sits beside them WorkBuddy refuses the whole request, 11159
"Invalid `signature` in `thinking` block … bound to a different
conversation", on every try. Signed blocks go back as they came. It carries
WorkBuddy's headers as a chat does, the same conversation and turn ids
included: a tool result in a `user` message of `tool_result` blocks answers
the step before it, as a `role: "tool"` message does in a chat.

The point of the route is effort. A chat carries `reasoning_effort` alone,
and WorkBuddy's chat endpoint answers at about the same length whatever it
says: asked the same question four times each (2026-10-24,
`claude-opus-5.5`), the median answer was 3884 output tokens at `low`, 5144
at `max` and 5715 at a value that isn't a level at all. Messages carries
`output_config.effort`, and `max` is honored there: a median of 30850
against 5748 at `low`, same model and question, four runs each. A Claude
model on Messages therefore lists `max` in its levels even where
`/v3/config` stops at `xhigh`.

`/v1/messages/count_tokens` **is not served** (404). magpie treats a 404 on
count_tokens as "no counter" (`unsupportedCount`) and answers with its own
estimate.

Each chat carries WorkBuddy's headers:
- `Authorization`, `X-User-Id`, `X-Domain`, `X-Product: SaaS`, `X-IDE-Type: WorkBuddy`;
- `User-Agent: WorkBuddy/5.5.6`. Without it, WorkBuddy answers error 10085;
- the client's, which WorkBuddy's usage list shows as the client that asked
  (使用端): `X-Agent-Purpose: conversation`, `X-IDE-Name: WorkBuddy`,
  `X-IDE-Version`, `X-Agent-Intent: craft`, `X-Requested-With`. Both builds
  send them, as WorkBuddy's own client does; a chat without them is counted
  under no client at all;
- the conversation's ids: `X-Conversation-ID`, `X-Conversation-Request-ID`,
  `X-Conversation-Message-ID`, `X-Request-ID`. WorkBuddy's backend counts a
  user send as one request by `X-Conversation-Request-ID`, so every step of
  one send — a tool result sent back, a retry, another account answering —
  carries the same one, and the user's next message carries another. The
  conversation's is the session magpie (or OpenCode) names the request by,
  else the chat's first user message; nothing is made up when neither names
  one, a made-up conversation of the request's own being what splits one
  conversation into many in the usage detail;
- WorkBuddy AI also takes `X-Agent-Type: main`.

A chat that doesn't open with a system message is given WorkBuddy's
default one, as the app does.

WorkBuddy refuses some agents' identity lines as an "unapproved channel"
(11128), so those lines are reworded before a chat goes out. Since 0.1.27
(#279) that includes DeepSeek Harness's opening line. A refusal of words
not reworded yet is sent once more, since 0.1.29, with the system prompt
moved into the first user message (inside `<instructions>` tags) and
WorkBuddy's default system prompt in its place: WorkBuddy checks only the
system and assistant messages, and the line it matched is then in neither.
A system message later in the chat becomes a user message where it stands.
The plugin remembers each refused system prompt while it is loaded, so that
agent's next chats go moved at once. The move can't reach an assistant
message, so words the model quotes back into one keep that chat refused
until they are in the list reworded in every message (since 0.1.30, the "Web GUI whose URL the runtime
context names" line, #291); otherwise only a new chat clears it. A refusal
that stands after the move ends in a hint that names no one client
(since 0.1.28): WorkBuddy refused words in this client's prompt; use it from
Hermes, OpenCode or Pi, or add another provider to the group. Neither
refusal rests the account.

The loader also translates OpenAI-compatible request spellings WorkBuddy
doesn't take: `max_completion_tokens` becomes `max_tokens`, `developer`
messages become `system`, and an object `tool_choice` becomes WorkBuddy's
string form.

A chat's tools are given shapes WorkBuddy's models take. Without them
WorkBuddy AI answers 400 "Invalid request parameters" (11133), DeepSeek's
models to each shape below and Kimi's, GPT's, Gemini's and GLM's to some:
- a tool's parameters with no `type`, empty, or a union (`anyOf`, `oneOf`,
  `allOf`) at the root are an object, the union's fields merged;
- a `pattern`'s `\0` is `\u0000`, which matches the same;
- a tuple's `items` (a list) is one schema any of them fits;
- an `enum` with no `type` takes its values' type (or that and `"null"`),
  and an `enum` of several types is an `anyOf` of one typed `enum` for
  each, at any depth; an empty `enum` is left out (Kimi and DeepSeek, an
  MCP server's tools, #122);
- `true` and `false` where a schema goes are `{}` and `{"not": {}}`
  (`additionalProperties` is left); a `required` that isn't a list, and a
  `type` JSON Schema doesn't name, are left out, and `"String"` is
  `"string"`;
- a tool's name with a character other than letters, digits, `_` and `-`
  (`.`, `:`, `/`, a space, CJK) is sent with `_` there, and the model's
  calls come back under the tool's own name.

WorkBuddy AI's `gemini-3.8-flash` answers 400 "the conversation must end
with a user message" (11153) to a chat whose tool results are followed by
the user's message, as magpie sends Claude Code's reminders, words typed
mid-task and a tool's images. Such a chat goes again with an empty
assistant message between the results and the user's message, which it
serves; a model that refused it once gets it that way from then on, until
the plugin restarts. A chat ending with the assistant's message (a prefill)
is refused as before.

## Models

When you are signed in, the models are the ones WorkBuddy's product config
gives its CLI agent (`GET /v3/config`), with their context, image input and
reasoning levels. Until then, or if WorkBuddy doesn't answer, the list below
is used.

`workbuddy`:
- Auto
- Hy4 preview, Hy3, Hy3-X
- Deepseek-V4.1-Flash, Deepseek-V4-Pro
- GLM-5.3, GLM-5.3-Flash, GLM-5.2, GLM-5.1, GLM-5v-Turbo
- MiniMax-M3
- Kimi-K3, Kimi-K2.7-Code, Kimi-K2.6

`workbuddy-ai`:
- Default, Fast, Balanced, Primary, Deep
- GPT-5.5, GPT-5.4, GPT-5.3-Codex
- Gemini-3.1-Pro, Gemini-3.5-Flash
- GLM-5.3, GLM-5.2
- Hy3
- Kimi-K3, Kimi-K2.6
- MiniMax-M3

A model's reasoning levels are its variants (`reasoningEffort`).

A model's price is its `credits` ("x0.29") and the config's
`modelPromotions`. A discount for whole days (Step-5-Preview's 独家限免) is
the price now, with the price before it (`rateWas`). A price that holds only
some hours of the day (Hy4 preview, free 23:00–8:00) is given as its full
price, never as free: magpie keeps the list until it asks again, so a list
asked for at night would say "free" all day (#121).

## Credits in magpie

magpie's usage card shows the account's credits from the resource summary.
It also says when the soonest credits left run out (`runsOutAt`, and how
many: `runsOut`), from the paid and free package lists WorkBuddy's own
client reads. The daily Free Plan slice isn't counted: it ends at the same
midnight on every account. magpie's smart routing spends the account whose
credits run out first.

An enterprise (team) account's resource summary lists no packages. Its
credits are read from the enterprise meter
(`/billing/meter/get-enterprise-user-usage`), the one WorkBuddy's own usage
page reads: the cycle's credits used of its limit, and when the cycle
resets (magpie #1904).

## When free models work but a credit model does not

Check the failing account and model separately before concluding that the
wallet is empty:

- Use the exact API id from the account's model list. For example,
  `glm-5.3-flash` is the id; `GLM-5.3-Flash` is its display name. A route
  copied from another provider need not be a valid WorkBuddy model id.
- The WorkBuddy 5.6+ encrypted-desktop message means this sign-in cannot
  be read, not that the account has zero credits. Use **Account (browser)**
  for the same build/region. Keep the old entry until the new identity and
  model list are confirmed. The desktop file is not decrypted or changed.
- An account restricted by WorkBuddy is a different failure. Code 11140
  with its unusual-activity message calls for **Submit feedback** in
  WorkBuddy; signing in repeatedly or changing the model does not lift it.
  See [magpie#1849](https://github.com/yetone/magpie/issues/1849).
- Inspect each attempted provider/account in magpie's routing details.
  A ZCode trial quota failure, another provider's insufficient credits, or
  one account's restriction is not the balance of every WorkBuddy account.

A model response confirms that the selected account can answer. It does
not, without a matching vendor usage record, prove how many credits that
request deducted. A usage-read error means the balance is unknown.

The encrypted-desktop result can be reproduced without an account using
`packages/workbuddy/usage.test.mjs` in an isolated temporary HOME (and
USERPROFILE on Windows). It covers sign-in, usage and chat against a
synthetic sealed desktop file, and checks that the file stays unchanged.
A successful browser authorization alone is not a migration: compare the
returned identity with the intended desktop account before replacing its
entry. An already signed-in browser can simply renew another saved account.


## Not here

magpie's built-in WorkBuddy does more than this package:
- the daily check-in;
- the credits meter (`/billing/meter/get-user-resource-summary`);
- several accounts under one provider.

OpenCode's plugin API has no place for these.

Credit multipliers accept the international catalog's `x0.34 credits` text as well as `x0.34`. An empty credits value (for example Auto) stays unknown, never free.
