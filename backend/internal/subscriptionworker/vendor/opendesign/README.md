# @magpie-community/opencode-opendesign-auth

Open Design (open-design.ai) Cloud as a magpie / OpenCode provider: sign in
with the Open Design account, use the models its **Design Plan** entitles the
account to, and watch the plan's **5-hour and 7-day allowance** and wallet
balance where every other subscription's usage shows.

Works in [magpie](https://usemagpie.ai) (which runs OpenCode provider plugins)
and in OpenCode.

```
magpie plugin add @magpie-community/opencode-opendesign-auth
magpie plugin login opendesign
```

In OpenCode, `opencode.json`:

```json
{ "plugin": ["@magpie-community/opencode-opendesign-auth"] }
```

then `opencode auth login`.

## What it signs in to

Open Design's cloud is two planes:

| Plane | Host | Key | Used for |
| --- | --- | --- | --- |
| Control | `amr-api.open-design.ai` | `vela_ctrl_…` | plan, wallet balance, the Design Plan's allowance windows, the workspace |
| Model | `amr-link.open-design.ai/v1` | `sk-amr-…` | `/models`, `/responses` (the OpenAI Responses API) |

A sign-in is the same device authorization the Open Design CLI
(`vela login`) runs:

1. the plugin POSTs `/api/v1/cli/device-authorizations` and gets a code
   (`4HYFNA`) and an activation page (`open-design.ai/cloud/cli/activate?…`);
2. magpie opens that page; approving it says the CLI is signing in;
3. the plugin polls it and receives both keys for that account, with the
   account's email and id.

Nothing else is needed, and nothing of the desktop app's own sign-in
(`~/.amr/config.json`, a different machine-issued pair of keys) is read or
written.

## Sign-in methods

| Method | What it stores | Allowance |
| --- | --- | --- |
| **Open Design account** | the account's control + runtime keys | 5-hour and 7-day windows, wallet balance, plan |
| **Open Design API key** | a pasted `sk-amr-…` key | not readable: `amr-api` doesn't take API keys |

The account sign-in is the one that shows the allowance; the API key one
is there for a machine that can't open the approval page.

## Models

Nothing is listed per account at request time from anywhere but the vendor:
magpie calls the plugin's `provider.models`, which reads `amr-link`'s live
`/v1/models` at that account's key on every listing, so what a signed-in
account is offered is what its plan entitles it to right now. Limits, input
modalities and the vendor's own cost words (`rate`, e.g. `x0.5` for "low")
come from that same catalog.

Reasoning levels are not in the catalog, so `models.mjs` records the ones
measured against `amr-link` (#137): every model took `reasoning_effort`
`low` and `high`, and all but GPT-6 Luna thought at them, the ones the
catalog calls `standard` too. Those models reason at `low` and `high`;
GPT-6 Luna has no levels. A model the seed doesn't know reasons when its
vendor tier is `advanced`, with no levels until it is measured.

`models.mjs` is the seed the provider has *before* anyone signs in. Refresh
it from the live catalog with:

```bash
magpie plugin login opendesign            # once, so the key is on hand
bun refresh-models.mjs                    # or OPENDESIGN_KEY=sk-amr-… bun …
```

The script keeps the names and measured levels already there — the catalog
carries neither — and reads nothing else: no secret is written to the file or
printed.

Requests go to `https://amr-link.open-design.ai/v1`, each model on the API
amr-link answers its thinking on (measured, plugins#141): chat completions
(`@ai-sdk/openai-compatible`) for most, the Responses API (`@ai-sdk/openai`)
for `claude-haiku-5.5` and `deepseek-v4-pro`, and Anthropic's messages
(`@ai-sdk/anthropic`) for `glm-5.3-flash`. Every model reasons at `low`,
`high` and `max`.

## Where the sign-in is kept

magpie's `plugin-auth.json` (mode 600), as an oauth account:
`{ type: "oauth", access: <runtime key>, control: <control key>, accountId:
<email>, uid: <account id> }`. Neither key expires, so there is nothing to
renew and `refresh` is empty.

## Use

```
magpie plugin                         # opendesign (Open Design, 11 models) signed in
magpie provider test opendesign
magpie quota                          # opendesign · Design Plan Go · 5h 32% ↻3h
```

### Replacing a custom provider

The plugin signs in to the provider id `opendesign`. A **custom provider**
of that same id (Providers → the host and key by hand) takes the id first,
so the plugin's provider is then listed as `opendesign-plugin`, with no
models of its own. Delete the custom provider — `magpie provider rm
opendesign` — and the plugin's provider takes the plain id, so agent
configs pointing at `opendesign/<model>` keep working, now with the
allowance card.

## The allowance in routing

The windows are gating windows (magpie's `aside` is left off), so magpie's
own routing reads them like any subscription's:

* a window magpie reads as used up makes the account count as used up, so
  routing groups skip it and a direct call to `opendesign/<model>` is refused
  with a 429 until that window renews;
* `magpie provider account-cap opendesign <account> [percent|off]` holds the
  account at a share of each window, and `--window "5 hours" 70` caps one
  window alone, which is how to leave headroom instead of spending the plan
  to the last credit.

Since Open Design falls back to the wallet balance once the Design Plan
allowance is out, an exhausted window holds the account rather than spending
the wallet. Set the account to spend its credits, or cap the windows, to
choose which of the two magpie does.
