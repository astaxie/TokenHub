# @magpie-community/opencode-grok-auth

Grok with a SuperGrok or X Premium+ subscription, through the sign-in of
xAI's [Grok Build CLI](https://x.ai/cli). Provider id: `grok`.

It needs Grok Build installed:

```sh
curl -fsSL https://x.ai/cli/install.sh | bash          # macOS, Linux
irm https://x.ai/cli/install.ps1 | iex                 # Windows
```

It is found in the installer's folder (`GROK_BIN_DIR`, `GROK_HOME/bin`,
`~/.grok/bin`), or on `PATH` when it lives under a `.grok` folder, so that
another program named `grok` isn't taken for it. Where the installer can't
run, such as a container without curl, put the binary in a folder of your
own and set `GROK_BIN_DIR` to it. magpie installs Grok Build itself, as
the installer does, before a sign-in that needs it.

## Sign in

- **Sign in with Grok Build (device code)** runs `grok login --device-auth`
  and opens the page it prints. Approve the sign-in there, and the CLI
  finishes it.
  - If the CLI isn't signed in yet, the sign-in goes to the CLI's own home
    (`GROK_HOME`, else `~/.grok`), as if you had run `grok login`.
  - If it is signed in, the sign-in goes to a home of this plugin's,
    `$XDG_CONFIG_HOME/opencode-grok-auth/<random>` (else under
    `~/.config`). Grok Build's own sign-in stays as it is.
  - The account is told apart by its email, else its xAI user id (an
    account signed in through X can have no email). `GROK_AUTH_PATH` is
    left out of the CLI's run, so the sign-in lands in that home.
  - When the CLI finishes but leaves no sign-in there, the failure says
    which file it read and what it found.
- **Use Grok Build's current sign-in** takes the account the CLI is
  signed in to, with nothing to approve.
- **Import CPA Grok account JSON** copies one enabled xAI OAuth account from
  a CLIProxyAPI (CPA) account JSON file (CPA keeps it as
  `~/.cli-proxy-api/xai-<email>.json`) into a private home for this plugin.
  CPA and Grok Build rotate the same refresh token, so stop using that
  account in CPA before importing it. The file is read, never rewritten.

## Where the sign-in is kept

The CLI keeps the sign-in in its home, as `auth.json`. OpenCode (or
magpie) keeps where that home is, the token and its expiry. Importing a CPA
account writes only its translated xAI OAuth fields to a private home owned
by this plugin; the selected CPA file is left unchanged.

The plugin reads `auth.json`; Grok Build refreshes the token, which changes
each time. The plugin runs `grok models` in that home, so the CLI renews
it, then reads it again:

- magpie renews the token 5 minutes before it ends, through the plugin's
  `auth.refresh`, once for the account and before its requests, models
  and usage need it. magpie saves what the CLI gave.
- OpenCode doesn't call that hook: there the token is renewed when it is
  5 minutes from expiring, before a request.
- The CLI runs once for a home at a time: a request or renewal that
  comes while it runs waits for it and reads what it gave. A token the
  CLI already renewed is taken as it is.
- Grok can turn a token away before its `expires_at`. A request, model
  list or usage read that gets 401 has the CLI renew the token and asks
  once more with the new one.
- When the CLI's run leaves the token as it was, the 401 or error says
  Grok Build couldn't renew the sign-in, with what the CLI said. For Grok
  Build's own home ("Use Grok Build's current sign-in") opening Grok Build
  and sending it a message renews it. For a home of the plugin's (a second
  account, an imported one), opening Grok Build doesn't touch it: add the
  account in magpie again.
- When the CLI no longer holds a sign-in in that home (`grok logout`),
  the account has to be signed in again. When it couldn't renew a token
  that has ended, magpie tries again later.
- An `auth.json` that is empty or not JSON is most likely one the CLI is
  writing: it is read again for about half a second, then the last sign-in
  read whole there stands, as the CLI itself keeps its previous one. It is
  never taken as signed out. The plugin takes no `auth.json.lock`.

## Requests

Requests go to `https://cli-chat-proxy.grok.com/v1`, OpenAI's Responses
API (`@ai-sdk/openai`), signed as the CLI signs them:

- `Authorization: Bearer <token>`
- `x-xai-token-auth: xai-grok-cli`
- `x-grok-client-identifier: grok-shell`
- `x-grok-client-mode: headless`
- `x-grok-client-version` and `User-Agent: grok-shell/<version> (<os>; <arch>)`.
  The version is 1.0.41, or the installed CLI's if it is newer.
- `x-grok-model-override`: the request's model. A window suffix
  (`grok-4.7@500k`) is the plain model (`grok-4.7`) here and in the body.
- `x-grok-context-window`: the token count a suffixed model names
  (`500000` for `@500k`). The plain id sends no such header and stays on
  the default window.
- `x-grok-conv-id`: the request's `prompt_cache_key`.

The backend turns away some of what a request may carry, so the plugin
leaves these out:

- tools of types it doesn't take (a freeform `apply_patch`, namespaces)
- `external_web_access` on web search
- a `tool_choice` naming a dropped type
- a `tool_choice` with no tools beside it (magpie sends `"none"` so when a
  client turned tools off)
- a reasoning item's `"content": null`

Plain Codex collaboration items (`agent_message`) reach Grok as user
messages, with their sender, recipient and content preserved. Sealed or
unrecognized content stays unchanged; the plugin does not decrypt tasks.

### Native X search (optional)

In magpie, open the Grok provider, turn on **Native X search** under
**Plugin options**, and save. It is off by default; changes apply to the
next request without restarting or signing in again.

When enabled, the plugin offers Grok its native `x_search` tool. Grok
decides whether to search; enabling it does not force a search on every
request. Existing function tools and X search filters are preserved, and
an existing `x_search` is not duplicated. Explicit choices such as `none`,
`required`, a named tool, or `allowed_tools` never gain another tool.

The option applies to Responses requests, including Chat Completions,
Anthropic Messages and Gemini requests that magpie converts to Responses,
in streaming and non-streaming mode. Use magpie with the fix for
[yetone/magpie#3110](https://github.com/yetone/magpie/issues/3110) so that
those clients' tool-disable choices reach the plugin.

For compatibility with older gateways, a request that reaches the plugin
with neither tools nor a `tool_choice` is left unchanged. This can happen
when magpie translates a client's automatic choice on an empty tool list.
To use X search through a translated API, send at least one function tool;
the plugin adds X search alongside it. An explicit tool-disable choice
always takes precedence.

This uses the existing `grok` provider's login, models and quota reporting;
no extra plugin is needed. It applies to that provider's models when the
upstream supports X search, not to Grok models served through Cursor or
another provider. Report problems in
[this repository](https://github.com/magpie-community/plugins/issues).

## Models

Grok 4.7 until signed in. Signed in, the models are the ones the account
lists at `/v1/models` (Responses models only), each with its reasoning
efforts as variants.

`context_window` is the default window and keeps the plain id, so a saved
`grok-4.7` stays a 256k model. Each other size in `context_windows` is
offered beside it, named `Grok 4.7 @ 500k` with id `grok-4.7@500k` and
that many tokens as its window. A request for it sends
`x-grok-context-window: 500000`. The proxy serves up to its largest
window either way and refuses past it; the header names the window the
caller asked to compact against. Pick `grok-4.7@500k` where a client
compacts at the advertised window, such as Claude Code.

## Usage

magpie's card shows the credits of the current period
(`/v1/billing?format=credits`): weekly for SuperGrok, with on-demand
spending beside it when it has a cap.

Grok's billing says nothing of a plan's rate limit, so a free account can
read 0% used while its requests come back 429. After a 429 the card shows
a spent "Rate limit" window until the reset it named (`Retry-After`, or a
`*ratelimit*reset*` header). When it named none, the window stays until a
request goes through, or for 5 minutes.

Grok Bot's separate allowance is shown by the [Cursor plugin](../cursor/README.md#usage).
Sign in there with the Cursor account linked in Grok Bot, including for
access through SuperGrok.
