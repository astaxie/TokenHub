# @magpie-community/opencode-devin-auth

Signs in to a [Devin](https://devin.ai) subscription, the account the
`devin` CLI uses, and makes its requests, in OpenCode and in magpie.
Provider id: `devin`.

## Signing in

An account is the CLI's session token (`devin-session-token$…`). There are
two ways to get one:

- **Devin (browser)**: the CLI's own `devin auth login`, run by the
  plugin. It is a PKCE sign-in through `app.devin.ai/auth/cli/continue`
  back to a callback on `127.0.0.1`. The code is exchanged for the token
  at `server.codeium.com` (`ExchangeDevinCLIPKCECode`).
- **Devin CLI's sign-in**: takes the account the CLI is signed in to, from
  its `credentials.toml` (`$XDG_DATA_HOME/devin/`, by default
  `~/.local/share/devin/`; `%APPDATA%\devin\` on Windows). The file is only
  read, never changed.

The plugin names the account as Devin does when asked with the token
(`GetUserStatus`: its email, organization when named, and tier). When Devin can't tell, and the CLI
is installed, it asks `devin auth status`, run with a data folder of the
plugin's own, `~/.cache/opencode-devin-auth/<hash of the token>/`, so the
CLI's own sign-in is never touched. A CLI that reads another
`credentials.toml` than that folder's (on Windows it reads
`%APPDATA%\devin\` whatever it is run with) isn't asked: it would name
its own account. Otherwise the account is named "Devin".

The token is kept where OpenCode keeps sign-ins (`auth.json`; in magpie,
`plugin-auth.json`) as `{ "type": "api", "key", "metadata": { "email", "plan", "orgId", "teamId" } }`.
Devin doesn't expire it on a schedule, so there is nothing to refresh;
when it is refused, requests fail with a 401 saying to sign in again.

## Requests

Devin serves chat through Windsurf's API server, as the CLI does:
`POST https://server.codeium.com/exa.api_server_pb.ApiServerService/GetChatMessage`,
a Connect RPC in protobuf, with `Authorization: Basic <token>`.
`WINDSURF_API_SERVER_URL` moves the server, as it does for the CLI.

The plugin's `fetch` handles this:

- It turns OpenCode's chat completion into the CLI's request (devin-cli
  3000.11.3).
  - The system prompt goes at the head of the first user message.
  - Each tool's description goes there too, in a `<tool_descriptions>`
    block, and the tool is sent with a pointer to it. Devin answers "an
    internal error occurred" to some agents' tool descriptions when they
    are sent as the tools' own.
  - A tool call the conversation never answered is answered with "Tool use
    was interrupted and did not produce a result."
  - Images must be inline (`data:` URLs). The images a tool returned go
    with its result, as magpie's built-in sends them: a tool message's own
    `image_url` parts, and the ones magpie's gateway puts in the user
    message after the results ("[From the result of …]").
- A request past 8 KiB goes gzipped, as the CLI sends it (since 0.1.21,
  #275): Devin turns a body past 10 MiB away with 413.
- It turns the streamed reply back into a chat completion, streamed or
  not: text, reasoning, tool calls and usage.
- Failures keep their statuses: a refused token is a 401, a usage limit a
  429, a prompt too long for the model a 400.
- A reply with only metadata and usage is a 502, even when output tokens
  were counted. The plugin waits for actual content before opening a
  successful stream, so the gateway can try another channel.

## Models

Devin's models are families: `swe-2`, `claude-opus-5-5`, `gpt-6-sol`, …
Each family has variants at each effort (`swe-2-high`,
`claude-opus-5-5-xhigh`), and some have a fast or priority run
(`claude-opus-5-5-high-fast`). The plugin lists each family once, with
the efforts its variants are at, plus its fast or priority run
(`claude-opus-5-5-fast`). The variant asked for is the one at the effort
picked, or the nearest the family has; with none picked, the family's
default. Variants at no effort (`glm-5-2-1m`) are listed as they are.
Adaptive and Fusion aren't listed; they route between models inside
Devin's own agent.

The `config` hook declares Devin's list as of 2026-10-14 (89 models).
Once signed in, the `provider.models` hook replaces it with the account's
own: what Devin tells the CLI's model picker for the account's token
(`GetCliModelConfigs`, the list `devin models list` prints), with or
without the CLI and on every system. When Devin can't be asked, the list
is the CLI's `devin models list`, if the CLI names the account Devin names
the token's (on Windows it reads its own sign-in whatever folder it is
given, so it answers only for that account); else the declared list. The
list is cached for 5 minutes; after a failed read, the plugin tries again
after 1 minute.

Which models take images is what Devin tells its CLI's model picker
(`GetCliModelConfigs`, each model's `supports_images`), asked once signed
in, with or without the CLI, and cached as the list is: `swe-2`, `swe-1.7`
and most others do; GLM-5.2, GLM-5.3, Inkling, Nemotron, DeepSeek V4 Flash
and V4 Pro don't. Before Devin is asked, or when it can't be, the declared
list says what Devin said on 2026-10-05. A model Devin says nothing of
takes models.dev's word.

## Not included

magpie's built-in Devin account does more than this plugin:

- **Usage**: the plan's quota windows.
- **Several accounts** at once.
- **Web search**: its stand-in for Anthropic's server-side web search tool.
