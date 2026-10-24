# @magpie-community/opencode-kiro-auth

Signs in to a [Kiro](https://kiro.dev) subscription (Free, Pro, Pro+,
Power) and makes its requests, in OpenCode and in magpie. Provider id:
`kiro`.

## Signing in

- **Kiro (Google, GitHub, AWS Builder ID, IAM Identity Center)**: the Kiro
  IDE's own sign-in.
  - Kiro's page (`app.kiro.dev/signin`) sends the browser back to one of
    the ports the IDE listens on (3128, 4649, 6588, 8008, 9091, 49153,
    50153–53153) on `127.0.0.1`. While the IDE is signing in, those ports
    may be busy.
  - Google and GitHub come back with a code. Kiro's auth service trades it
    for tokens.
  - Builder ID and Identity Center come back with an AWS sign-in URL and
    region. The plugin registers a client with AWS's OIDC service, sends
    the browser to AWS, and trades the code AWS returns.
  - The account is named by the email Kiro has for it (`Get-Usage-Limits`)
    and the way it was signed in to: `email (Google)`, `email (GitHub)`,
    `email (Builder ID)`, `email (IAM Identity Center)`, `email
    (Internal)`. Its plan is read from the same call, and its usage names
    it the same way.
  - Kiro keeps a Google, a GitHub, a Builder ID and an Identity Center
    sign-in of one email as different accounts. The sign-in carries a `uid`
    (`google`, `github`, `builderid`, `enterprise`), so magpie keeps each one
    beside the others, and a sign-in again the same way replaces only its
    own account. An account signed in before 0.1.13 is given its `uid`,
    and one named by its bare email before 0.1.14 its name, on its first
    request, model listing, usage read or renewal. Until then, a sign-in
    again the same way goes beside it rather than replacing it: magpie
    matches a sign-in by its name, and the plugin can't see the other
    accounts to match it by `uid`.
  - A company's own identity provider (`external_idp`) is not supported
    here. Sign in with `kiro-cli login` and use the next method instead.
- **Kiro CLI's or Kiro IDE's sign-in**: uses the account kiro-cli is signed
  in to, or else the Kiro IDE's. Nothing is copied. It is read each time
  from where they keep it:
  - kiro-cli: its SQLite database's `auth_kv` table.
    - macOS: `~/Library/Application Support/kiro-cli/data.sqlite3`
    - Linux: `~/.local/share/kiro-cli/data.sqlite3`
    - Windows: `%APPDATA%\kiro-cli\data.sqlite3`
  - The IDE: `~/.aws/sso/cache/kiro-auth-token.json`.

  kiro-cli refreshes its own token first (`kiro-cli debug refresh-auth-token`).
  A token the plugin refreshes itself is written back, so kiro-cli and the
  IDE go on with it.
- **Kiro API key (`ksk_…`)**: sent with `tokentype: API_KEY`, as kiro-cli
  sends it. `GetProfile` is asked in us-east-1, then eu-central-1, for the
  region the key's profile is in; a key it finds none for is used in
  us-east-1, as kiro-cli uses it. Its models are listed with the control
  plane's `ListAvailableModels` and its credits with `GetUsageLimits`, and
  no profile is named in its calls, as kiro-cli names none.

A browser sign-in is kept where OpenCode keeps sign-ins (`auth.json`; in
magpie, `plugin-auth.json`) as an `oauth` entry. The entry holds the
tokens, the profile ARN, the region and, for AWS, the registered client.
Refreshing such a sign-in:

- magpie renews the token 10 minutes before it expires, through the
  plugin's `auth.refresh`, once for the account and before its requests,
  models and usage need it. magpie saves the new token. A failed renewal,
  even one Kiro refused, doesn't mark the account: it is tried again.
- OpenCode doesn't call that hook: there the token is refreshed 2 minutes
  before it expires, before a request, and saved.
- Either way, a token Kiro turns down (a 403) is refreshed and the request
  tried once more.
- Only one refresh runs at a time, magpie's renewal included, and a token
  refreshed but not yet saved is used rather than spending the old refresh
  token again.
- kiro-cli's and the IDE's sign-ins aren't renewed by magpie: they are
  refreshed before a request, as above, and written back.

Tokens are refreshed through:

- Google/GitHub: through `prod.<region>.auth.desktop.kiro.dev/refreshToken`.
- Builder ID/Identity Center: through AWS's `oidc.<region>.amazonaws.com/token`.

## Requests

Kiro has no API OpenCode speaks. The models are declared on Anthropic's
Messages (`@ai-sdk/anthropic`), and the plugin's `fetch` handles each
request:

- It sends the request to `runtime.<region>.kiro.dev/generateAssistantResponse`,
  the API kiro-cli uses, with kiro-cli's headers. The region is the one the
  profile ARN names.
- It turns the request into a Kiro conversation:
  - The system prompt goes at the head of the first message.
  - Tool calls and results are paired: an unanswered call gets an error
    result, and a result longer than 250,000 characters is cut.
  - Tool ids Kiro wouldn't take are rewritten. A tool name over 64
    characters (an MCP plugin's, in Claude Code) is sent shortened with a
    hash and its calls come back under the full name.
  - A tool's images (a screenshot, Codex's view_image) go with the
    message that answers the call, as Kiro's tool results hold text alone.
  - Only the latest images are sent.
  - Tools the history used but the request doesn't offer are declared.
- It turns the AWS event stream Kiro answers with back into Messages'
  events (streamed) or one message. The reply includes text, thinking,
  tool calls and usage. When Kiro reports only how full the context is,
  input tokens are estimated from that.
- Thinking (Claude models and Auto) is asked for the way kiro-cli asks, in
  the prompt, with a budget: low 10k, medium 20k, high 30k, xhigh and max
  50k (as magpie's built-in Kiro: #120). The
  `<thinking>` block of the reply comes back as thinking.
- Kiro's failures keep their meaning:
  - input too long → 400
  - no capacity → 503
  - usage limit → 429
  - throttling → "rate limited"

## Models

The `config` hook declares `auto` (Kiro picks the model). Once signed in,
the `provider.models` hook replaces it with the account's own list from
`List-Available-Models`, with the default first. Each model carries its
context window, output limit and whether it takes images. Claude models
and Auto have the thinking variants.

## Not included

- Usage and quota (credits, the monthly window).
- Switching between several accounts. OpenCode keeps one sign-in per
  provider.
- Signing in with a company's own identity provider in the browser (use
  kiro-cli's sign-in).
- magpie's web-search stand-in for Kiro.
