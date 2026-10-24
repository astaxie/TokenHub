# @magpie-community/opencode-commandcode-auth

Signs in to a [Command Code](https://commandcode.ai) plan (Pro, GOAT, Max,
Ultra, Go, Teams Pro) and makes its requests, in OpenCode and in magpie.
Provider id: `commandcode-plan`.

## Signing in

An account is a Command Code API key. There are three ways to get one:

- **Command Code (browser)**: the CLI's own sign-in. Studio asks you to
  approve a key for this machine, then posts it to a callback on
  `127.0.0.1`. The plugin checks the key with `/alpha/whoami` and reads the
  plan.
- **Command Code CLI's sign-in**: takes the account `commandcode login`
  signed in to, from `~/.commandcode/auth.json`. The file is only read,
  never changed.
- **API key**: paste a key from
  [commandcode.ai/settings/keys](https://commandcode.ai/settings/keys).

The key is kept where OpenCode keeps sign-ins (`auth.json`; in magpie,
`plugin-auth.json`) as `{ "type": "api", "key", "metadata": { "email": <user>, "plan" } }`.
Keys don't expire, so there is nothing to refresh.

## Requests

- **Every plan but Go** uses the Provider API at
  `https://api.commandcode.ai/provider/v1`. Every request carries the key
  as both `Authorization: Bearer` and `x-api-key`.
  - Claude models speak Anthropic's Messages API.
  - The other models speak chat completions.
- **Go** has no Provider API access. Its key is only accepted where the CLI
  asks, `POST /alpha/generate`, in the CLI's own format (command-code
  1.72.2 and its headers). The plugin's `fetch` handles this:
  - It turns OpenCode's chat completion into that format.
  - It turns the line-by-line reply back into a chat completion, streamed
    or not.
  - Failures keep their statuses: a model the plan lacks is a 403, running
    out of credits is a 402, and a usage-window limit is a 429.
  - A reply that stops before its finish line is a 502, unless it was
    streamed and had already said something. Then it ends cut short, with
    a note saying so, and `[DONE]` (#91).

The plan is read from `/alpha/billing/subscriptions`. It is cached for 10
minutes; after a failed read, the plugin tries again after 1 minute.
When the plan isn't known yet and the Provider API refuses the key with
"Your Go plan doesn't include API access", the key is taken as Go's from
then on (and saved so with it), and a chat completion is sent again to
`/alpha/generate`.

## Zero Data Retention

[Zero Data Retention](https://commandcode.ai/docs/resources/zdr) sends
requests only to upstreams that keep no prompts or replies. It is off
until you turn it on, either way:

- **In magpie**: turn on **Zero Data Retention** in the Command Code
  provider's editor (magpie v0.1.1231 or later). The switch is read at
  each request, so it needs no restart.
- **Anywhere** (OpenCode too): set `CMD_ZDR=1` in the environment of the
  process the plugin runs in, as for Command Code's CLI. Only `1` turns it
  on.

While it is on, every request for a reply carries `x-cmd-zdr: 1`, on the
Provider API and on Go's `/alpha/generate` alike, as the CLI sends it with `CMD_ZDR=1`.
ZDR requests usually cost more. A model with no ZDR upstream is refused,
and the refusal reaches the agent as Command Code said it: on the Provider
API a 422 with code `cmd_zdr_no_providers`, on Go a 400 whose type is
`cmd_zdr_no_providers`. Turn the switch off, or pick another model.

## Models

The `config` hook declares the default list:

| Model | Context window |
|---|---|
| Claude Sonnet 5 | 1M |
| Claude Opus 5.5 | 1M |
| GPT-6 Sol | 1.05M |
| DeepSeek V4 Pro | 1M |
| DeepSeek V4 Flash | 1M |
| Kimi K3 | 1M |
| GLM-5.3 | 1M |
| MiniMax M3 | 1M |

Once signed in, the `provider.models` hook replaces the default list with
the account's own:

Command Code's `/models` list is the same for every plan. A plan's list is
that list less the models the plan may not use, as the CLI's own table
(command-code 1.79.2) has it:

- **Go** and **GOAT** may not use the premium models (Claude Sonnet 5 and
  Opus, GPT-6 Sol, GPT-5.6 Terra, …). Go is also blocked from GOAT's own
  (Claude Sonnet 5.5 and Haiku 5.5, GPT-5.6 Sol, Grok 4.6 and 4.7, …).
- **Pro** may not use Claude Opus and Fable, GPT-6 Astra, GPT-6.1 Sol and
  Fugu Ultra; **Provider** may not use GPT-6.1 Sol.
- **Max**, **Ultra** and **Teams Pro** may use every model.

An account with bought or free credits may use every model, and so may one
whose plan can't be read: then the whole list is shown. A model the plan
doesn't have is still refused by Command Code (403 `MODEL_NOT_IN_PLAN`), and
that refusal reaches the agent.

- **Every plan but Go**: the Provider API's `/models` list. Each model's
  `supported_endpoints` picks the API it speaks.
- **Go**: the same `/models` list, asked without the key (it answers
  without one, and Go's key has no Provider API). That is 56 models as of
  2026-10-13. When the list can't be had, the CLI's own Go table (57
  models) stands in.

  The Go models carry their reasoning levels, and a requested level is
  fitted to the nearest one the model has.

Thinking can be turned off on the Provider API for DeepSeek V4 Pro, V4
Flash, V4 Flash Vision (exp), V4.1 Flash and V4.1 Flash Fast. These models
offer an off level (`none`), and the plugin sends it as Command Code's
`reasoning_effort: "off"`. A client's own `"off"` is sent the same way. The
other models, DeepSeek V4 Flash Fast and Kimi K3 among them, refuse `"off"`,
so turning thinking off sends them no level. Go's `/alpha/generate` hasn't
been tried with `"off"`, so it is sent no level there either.

The `/models` list doesn't say which models take images. A model is marked
as taking them when it is a Claude model, or when the default list or the
CLI's table says so. For any other model the plugin says nothing either
way, and magpie answers from models.dev, as its built-in did.

## Not included

- Usage and quota (`/alpha/billing/credits`, the 5-hour and weekly windows).
- Switching between several accounts. OpenCode keeps one sign-in per
  provider.
