# @magpie-community/opencode-minimax-auth

Signs in to MiniMax Code (mcode) accounts so their credits and M Plan can be
used. That covers the free credits and the daily check-in's credits. There is
one provider for each of MiniMax's two sites:

| Provider id | Site | API |
|---|---|---|
| `minimax-code` | MiniMax Code, China (minimax.cn / minimaxi.com) | `https://agent.minimax.cn/mavis/api/v1/llm/v1` |
| `minimax-code-global` | MiniMax Code, international (minimax.io) | `https://agent.minimax.io/mavis/api/v1/llm/v1` |

The package exports `MiniMaxCodeAuthPlugin` (`minimax-code`) and
`MiniMaxCodeGlobalAuthPlugin` (`minimax-code-global`).

## Risk

Read this before you use the package:
- **The credits are for MiniMax's services.** MiniMax gives them for use in
  MiniMax Code, and its terms decide where else they may be used. This
  package uses them from other agents. MiniMax hasn't approved that.
- **The risk to the account is yours.** MiniMax may limit, suspend or close
  an account that uses them this way.
- **This is a community package.** It has nothing to do with MiniMax.

## Signing in

You sign in with MiniMax Code's own device sign-in: the same OAuth device
flow, PKCE S256 and client `mcode-public` that `mcode /login` uses.
- magpie opens MiniMax's page and shows the code to confirm there.
- The plugin then waits for the sign-in and finishes by itself.

MiniMax Code's own sign-in is never read or changed. That is
`~/.minimax/auth/…`, so signing in here doesn't sign `mcode` out.

The sign-in is kept as an OpenCode `oauth` auth:
- what it holds: the access and refresh tokens, their expiry, and the
  MiniMax user id and email;
- OpenCode keeps it in `auth.json`;
- magpie keeps it in `plugin-auth.json`.

Each account signs in on its own, so magpie can hold several and switch
between them.

Refreshing:
- MiniMax spends a refresh token once. Sent a second time it is
  `invalid_grant`, which signs the account out, so the plugin never sends
  one twice.
- magpie renews the token ten minutes before it ends, through the plugin's
  `auth.refresh`, once for the account and before its requests, models and
  usage need it. OpenCode doesn't call that hook: there the access token is
  refreshed two minutes before it ends, before a request. Either way the
  new token is saved.
- When MiniMax turns the token away (HTTP 401), the request goes again with
  a newer token if one was saved meanwhile, else after a refresh.
- Only one refresh runs at a time for an account. Requests that arrive
  meanwhile wait for it and use its token. When saving the new token
  fails, the plugin keeps it in memory and goes on with it, rather than
  with the spent one on disk.
- A refresh token someone else spent first (another magpie process) isn't a
  sign-out when the new one they saved is there: that one is used.
- The account counts as signed out only when MiniMax answers the refresh
  with `invalid_grant` (HTTP 400). Any other failure keeps the sign-in, and
  the current token is used while it lasts.

## Requests

Chats are Anthropic messages (`@ai-sdk/anthropic`) sent to
`<API>/messages`. Each one carries MiniMax Code's headers:
- `Authorization: Bearer <access token>`;
- `x-api-key: sk-xxx`, the placeholder key MiniMax Code gives its SDK;
- `User-Agent: MiniMaxAgent`;
- `X-Mavis-Agent-Id: main`;
- `X-Mavis-Timezone-Offset`;
- `X-Mavis-Session-Id`. This is the conversation's id when magpie or
  OpenCode passes one, and a fixed id for the plugin's run otherwise.

A refusal that says the credits or quota ran out goes on as a 429. The 402,
403 or 429 statuses count, when the body says so. That way magpie can move
the conversation to another account.

## Models

When you are signed in, the models come from MiniMax Code's live model
config (`GET /mavis/api/v1/models`). The list includes each model's context,
image input and thinking options. If the live list doesn't answer, this
built-in list is used instead:

| Model | Context | Output | Images | Reasoning levels (variants) |
|---|---|---|---|---|
| MiniMax-M3.1-Flash-Preview | 1M | 512K | yes | low, medium, high, xhigh, max |
| MiniMax-M3 | 1M | 512K | yes | none, high |
| MiniMax-M2.7 | 200K | 128K | no | — (always thinks) |
| MiniMax-M2.7-highspeed | 200K | 128K | no | — (always thinks) |

MiniMax Code's own list reports 512K/128K for the M3 family, but the
endpoint accepts 1,048,576 input tokens and 524,288 output tokens for
every one of them, so those are the limits given here. An agent that
believed the advertised ones would compact its history about half way too
early.

Variants map to what MiniMax Code sends:
- a level is adaptive thinking at that effort, which becomes
  `output_config.effort`;
- `none` turns thinking off.

## Usage

The usage card shows:
- the credits left;
- the plan: Free, or the M Plan's tier and when it ends;
- for an M Plan, its windows (5 hours, 7 days, and any model's own) from
  `coding_plan/remains`.

These are read the way MiniMax Code reads them, with its signed account API.

## Check-in

The account's daily check-in (每日签到) is claimed through the same sign-in:
the plugin reads the panel (`/minimax-cloud/api/v1/signin/status`) and
claims the day's credits (`/minimax-cloud/api/v1/signin/claim`) with the
same signed account API, asked as MiniMax Code's own `/checkin` asks them:
as the account (its MiniMax user id, looked up first when the sign-in
didn't keep it), from the desktop client, on MiniMax Code's own hosts
(agent.minimax.io, agent.minimaxi.com). A check-in turned away with 401 is
renewed and asked once more, with the user id read again, as MiniMax Code
does. magpie can run it once a day per account, when its
MiniMax check-in switch is on; Settings → Usage → Plugins has that switch
and a *Check in now* button, and the outcome — claimed, done, inactive,
ineligible, with the streak — lands on the account's Usage card.
OpenCode doesn't call this hook: there, check in from MiniMax Code or
MiniMax's site.

## Not here

- **No sign-out.** Signing out doesn't revoke the token on MiniMax's side.
