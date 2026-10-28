# @magpie-community/opencode-zed-auth

Signs in to a **Zed** subscription (Zed Pro, its trial, a student or business
plan) and calls the models Zed hosts: Anthropic's, OpenAI's, Google's and
xAI's, through `cloud.zed.dev`, the way the Zed editor calls them. Provider
id: `zed`. Ported from magpie's built-in Zed account.

## Sign-in

**Sign in with Zed (browser)** is Zed's own sign-in:

1. The plugin makes an RSA-2048 key and listens on a port of `127.0.0.1`.
2. The browser opens `zed.dev/native_app_signin` with that port and the
   key's public half.
3. Once you sign in, zed.dev sends the browser back to the port with your
   user id and an access token encrypted to the key. The browser is then
   sent on to zed.dev's "signed in" page.
4. The plugin asks Zed who the account is (`/client/users/me`). That gives
   the organization the models are called under, and its plan. An
   organization with Zed's hosted models turned off is refused.

## Organization

Zed asks for a model token per organization. The Zed editor starts in the
account's default organization and lets you switch to another from its
title bar. The plugin has no switcher, so it picks the organization with a
plan that calls models (any plan but Free), in this order (#232):

1. the organization it saved before, while that one still has such a plan;
2. the default organization, if it has one;
3. the personal organization, if it has one;
4. any other organization that has one.

When none has such a plan, it keeps what Zed picks: the saved organization,
else the default, else the first. The plan shown is the picked
organization's own (none when Zed lists none for it), never the account's
plan in its place. When Zed answers 402 for the organization a sign-in was
saved under ("Organization does not have an active subscription"), the
plugin reads the account again, moves to an organization with a plan if
there is one, saves it with the sign-in, and asks once more. The plan card
re-checks the organization the same way.

The sign-in is kept as an `oauth` entry: `access` is the access token and
`refresh` is JSON holding the user id, machine id, organization and plan.
Zed has no refresh token, so the token lasts until Zed refuses it. When
Zed refuses it, you are asked to sign in again. `accountId` is the account's
GitHub login.

## Requests

- **Model token:** each request uses a short-lived model token, minted from
  the sign-in with `POST /client/llm_tokens` and kept in memory. If Zed calls
  the token stale (a 401, `x-zed-expired-token` or `x-zed-outdated-token`),
  it is minted again once.
- **API per model:** each model is declared on the AI SDK package of its
  own provider, so the request the SDK writes is already what Zed wants.
  The plugin's `fetch` wraps it as `{provider, model, provider_request}` and
  sends it to `POST /completions` with Zed's headers:

  | Zed provider | AI SDK package | API |
  |---|---|---|
  | anthropic | `@ai-sdk/anthropic` | Messages |
  | open_ai | `@ai-sdk/openai` | Responses |
  | x_ai | `@ai-sdk/openai-compatible` | chat completions |
  | google | `@ai-sdk/google` | generateContent |

- **Request changes, as magpie makes them:**
  - Anthropic: the request loses `stream`, since the cloud always streams.
    Every `tool_result` gets `is_error` (Zed's cloud refuses one without it).
  - xAI: `max_tokens` becomes `max_completion_tokens`.
  - Gemini: the model is named in the request as `models/<id>`.
- **Replies:**
  - Zed's newline-delimited lines are sent back as each API's own
    server-sent events.
  - A caller that didn't ask for a stream gets one whole reply.
  - A failure Zed reports mid-stream keeps its status (429, 402, 529 …).
    So does a reply without Zed's `stream_ended`, which comes back as an
    error rather than a shorter answer, except one case. A streamed xAI
    (chat) reply that has already said something, and has no tool call
    half written, ends cut short instead, with a note saying so, a `stop`
    and `[DONE]` (#91). magpie's gateway can go on with an Anthropic or
    Responses reply cut short, but not with a chat one.
  - A 402 means the plan doesn't include Zed's hosted models, or its
    allowance is used up.

## Models

- **After sign-in:** the account's own list comes from Zed's `GET /models`,
  read when you sign in and every 10 minutes after. Disabled models are
  left out. Each model's effort levels become its variants. When a read
  fails, the list read last is kept; when none was ever read (Zed down,
  or a list with nothing callable, as for an organization without an
  active subscription), the hook throws why, and magpie shows that reason
  with the account, the declared models marked as not its list (#229).
- **Before sign-in:** the config hook declares one model of each family
  (Claude Sonnet 5, Opus 5.5, Haiku 4.5, GPT-5.5, Gemini 3.5 Flash,
  Grok 4.7), which the account's list replaces.

## Usage

magpie's `auth.usage` hook shows what Zed tells its editor of the plan,
as magpie's built-in Zed account did. Zed doesn't report how much of the
allowance is spent, so there are no windows:

- the plan's name (Pro, Pro Trial, Business, Student, VIP). A free
  account's card says "No plan"; its account row still names it Free;
- when the billing period ends;
- an overdue invoice, as the card's error ("its models are paused");
- a sign-in Zed refuses (`/users/me` or the model list answering 401)
  marks the account as needing to sign in again. A plan that changed is
  saved with the sign-in.

## In magpie, not the plugin

These come from magpie itself, as they did for the built-in:

- Several Zed accounts at once. magpie keeps a sign-in per account and
  switches between them; plain OpenCode keeps one sign-in per provider.
- Web search. A model that can't search is given magpie's web-search
  stand-in, when a search API is set up in magpie.
