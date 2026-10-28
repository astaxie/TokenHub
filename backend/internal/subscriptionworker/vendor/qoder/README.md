# @magpie-community/opencode-qoder-auth

Your [Qoder](https://qoder.com) subscription in OpenCode and
[magpie](https://usemagpie.ai). The package serves Qoder's two sites, whose
accounts exist only on their own:

| Provider id | Site | Accounts | Plugin export |
|---|---|---|---|
| `qoder` | qoder.com (international) | Google, GitHub or email | `QoderAuthPlugin` |
| `qoder-cn` | qoder.cn ([Qoder CN](#qoder-cn)) | Alibaba Cloud or phone number | `QoderCNAuthPlugin` |
| `qwenwork` | gateway.qwenwork.cn ([QwenWork](#qwenwork-千问办公)) | phone number or email | `QwenWorkAuthPlugin` |
| `qwenwork-enterprise` | the enterprise's own domain ([QwenWork](#qwenwork-千问办公)) | email OTP or enterprise SSO | `QwenWorkEnterpriseAuthPlugin` |

What follows is about `qoder`. Qoder CN works the same way on its own
hosts; [its section](#qoder-cn) says what differs.

## Sign-in

**Sign in with Qoder** is the sign-in Qoder's desktop client uses:

1. A PKCE device flow opens qoder.com's account page.
2. openapi.qoder.sh is polled every 2 s until you authorize, for at most
   15 minutes.
3. The device token is traded for a job token, and the account's email and
   name are read.

The page is asked for no `redirect_uri`, as Qoder's CLI asks for none, so
the sign-in ends in the browser tab even when Qoder.app is installed
(before 0.2.25 it sent the desktop app's `qoder-app://`, which handed the
page to Qoder.app).

**Sign in with Qoder PAT** (and **Sign in with Qoder CN PAT**) skips the
device flow: a Personal Access Token from `<site>/account/integrations` is
posted to `/api/v1/jobToken/exchange` (`personal_token`), which answers a
job token, its refresh token and its expiry. The PAT is kept on the
credential, so a lapsed job refresh is exchanged again instead of asking
for the sign-in; PATs last up to a year. The job token signs the model list
and chat calls with the same COSY envelope either sign-in's does.

On Qoder CN this is the plugin's own reading of the protocol rather than
the CLI's path: qoder.cn's CLI signs requests with its device token and
never asks for a job token. Verified against a live qoder.cn account — the
exchange answers 200 with a `jt-` token, the signed model list answers 200
with that account's own models, and a chat completion returns its text.

A PAT account keeps the name its browser sign-in takes (`email || uid`): magpie
replaces an account on a new sign-in when both the accountId and the uid
match, so a renewed PAT sign-in names the same account rather than adding a
row beside the lapsed one.

A PAT account has no device token, so its usage read, campaigns pages and
daily check-in go out on the job token instead. The check-in works on it: a
live PAT account answered 200 to the campaigns list and `CLAIMED` to a claim,
so it earns the daily credits the browser sign-in does. That path has no
second token to rotate, so a refused call is the answer rather than a retry.

What is kept: the job token, its refresh token and expiry, the uid, the
device token and a machine id made for this sign-in. OpenCode keeps them in
`auth.json`; magpie keeps them in `plugin-auth.json`.

Refreshing:
- Qoder spends a refresh token once, so the plugin never sends one twice.
- magpie renews the job token 10 minutes before it runs out, through the
  plugin's `auth.refresh`, once for the account and before its requests,
  models and usage need it; magpie saves the new pair. OpenCode doesn't
  call that hook: there the job token is refreshed 5 minutes before it runs
  out, before a request, and the new pair is saved straight away.
- Only one refresh runs at a time. A request that reads the sign-in before
  magpie saved a renewal uses the renewed pair, and a renewal handed a pair
  a request already spent gives what that request got.
- The device token, which reads usage, has no end on record: it is rotated
  when Qoder refuses it. On a [Qoder CN](#qoder-cn) device-token account it
  is the chat token too, and is renewed as above.
- When Qoder refuses a refresh (401 or 403), the account needs signing in
  again. Any other failure is tried again later.

## Requests

Qoder serves its models on the API its client talks to:
`api3.qoder.sh/algo/api/v2/service/pro/sse/agent_chat_generation`. The
plugin's `fetch` takes the chat completion OpenCode sends and does the rest
itself.

**Writing the request**

- It writes the chat completion as Qoder's request:
  - Qoder's own system line comes before yours.
  - Messages become text and image blocks.
  - Tool calls and results become OpenAI tool turns. Images a tool returned
    follow in a user turn.
  - A result whose id the client lost answers its turn's call in order
    (since 0.2.22, #277): Qoder refuses a whole request with a result that
    answers no call before it. A result with no call left to answer goes
    as text in the user's turn.
  - Your tools become Qoder's native function tools.
  - A call the client sent without an id gets one made from its turn, its
    place and its arguments, so it goes out the same on every request.
- It marks the prompt for Qoder's cache as Qoder's own client does (since
  0.2.26, #290): the last block of the last user turn carries
  `cache_control: ephemeral` (nothing is marked when the last turn is a
  tool's result), user turns carry their blocks as `contents` too, and each
  request names its conversation in `session_id`. That id is a hash of
  the session the agent names (since 0.2.31, #330: DSH's
  `x-deepseek-harness-session-id` and the other agents' own session
  headers, which magpie hands the plugin's `chat.headers` hook, and
  OpenCode's session), else of Codex's thread (`prompt_cache_key`) when
  magpie sends one, else of the conversation's start, so every turn of one
  conversation names the same session; the key itself never goes out.
  Each request has its own `request_id` and `chat_record_id`.
- Every call that answers one user prompt goes out as one task, as Qoder's
  own client sends it (since 0.2.31, #330): one `request_set_id`, which is
  also `business.id`, and one `business.begin_at` for every call of the
  tool loop, `business.stage` "start" until a call is answered and
  "processing" after. A new user prompt in the session is a new task. The
  plugin keeps at most 512 tasks, each for 2 hours after its last call.
  Before 0.2.31 each call was a task of its own.
  - The model's own configuration from Qoder's list goes with it.
- It picks the reasoning effort from the model's own levels:
  - The nearest level to the one asked, the higher one on a tie.
  - The model's default when none is asked.
  - The lowest level for "none", when the model can't turn thinking off.
- It picks the context window (`context_length`) from the model's own
  windows (Qoder's Context setting: 200K, 400K, 1M): the default while the
  request fits it, else the smallest that holds it, else the largest. A
  larger window may cost more, so it is asked for only when needed.
- It encodes the body with the client's codec and signs the call with the
  client's COSY envelope. The envelope carries:
  - the account, AES-encrypted, with the key wrapped by Qoder's RSA key;
  - an MD5 signature;
  - the machine id and the client's headers.
  - `Cosy-Version`: the worker runtime version that Qoder's desktop app
    ships (1.1.69 in app 0.4.8, since 0.2.23, #281), because
    `Cosy-ClientType` says the desktop app.

**Reading the reply**

- Qoder's SSE comes back as a chat completion, streamed or not, with
  reasoning and usage.
- Tool calls Qoder writes as XML or JSON in its text become tool calls.
  Tool calls it sends the OpenAI way keep Qoder's id.
- A call written in the text that can't be read, or that the stream ends
  inside, is never sent on as the answer (since 0.2.16, #132): the turn
  ends as an error, a 502 or the stream's error frame with no finish, so
  the agent or magpie can try again. A call whose JSON is whole when the
  stream ends without its close is still the call; a tag in code or prose
  stays text.
- A call written in Anthropic's or Qwen's tags with no `<tool_call>`
  around it (since 0.2.36, #323), `<function_calls>` around one or more
  `<invoke name="…"><parameter name="k">v</parameter></invoke>`, or
  `<function=name><parameter=k>v</parameter></function>`, is a call too,
  but only for a tool the request declared; for any other, or in
  backticks or a code fence, it stays text. A parameter's value is JSON
  unless the tool's schema says string, in these and in `<tool_call>`.

**Errors**

| What Qoder says | What the request returns |
|---|---|
| A refused sign-in (no code, or code 105) | 401 |
| A quota (codes 110, 112–119, 122) | 429 |
| Busy or queued for the model (code 10605) | Since 0.2.28 the request waits in Qoder's queue as Qoder's client does (below), then is sent again; past the wait, 503 with `Retry-After` from Qoder's `retryAfterSeconds` (30s when it gives none). The sign-in is untouched |
| A refusal of the request itself (codes 406, 416, 430, 80411, 80412, 100400–100403) | 400, with Qoder's words |
| "Error in upstream response", the model's own vendor behind Qoder failing, which Qoder sends as a 400 (since 0.2.24) | 502, so magpie asks again or another account; the sign-in is untouched |
| A failure before the answer starts | Qoder's status |
| A failure after the answer has started | The stream ends with an error |

## Models

The `provider.models` hook reads the account's own list, as Qoder's client
asks for it (`/algo/api/v2/model/list`). It keeps the enabled models of the
listing's `app` scene, the list Qoder's app shows and the one a request
names (`session_type: "app"`), and adds any model only the `chat` scene
still lists after the model it follows there, so a model picked before
stays served (since 0.2.13, #97: the app listed Fast, 0.8×, where chat
lists Performance, 1.1×). It leaves out "auto" and "default", which route
inside Qoder. It takes
each model's reasoning levels from its `thinking_config`, and its context
from its `context_config`: the largest window it offers (Qoder's docs
give Qwen3.8-Flash 200K, 400K and 1M), not `max_input_tokens` (180K
there), which is only what a request naming no window gets. A model listed
with no windows keeps `max_input_tokens`.

The `config` hook declares the list as it was on 2026-09-30, with the
app's Fast from 2026-10-12:

- Ultimate, Fast, Performance, Efficient
- Sonus, Cantus
- Qwen3.8-Max, Qwen3.8-Flash, Qwen3.7-Max, Qwen3.7-Plus
- Kimi-K3, Kimi-K2.8-Preview
- GLM-5.3, GLM-5.3-Flash
- DeepSeek-V4-Pro, DeepSeek-Flash
- MiniMax-M3

Every model speaks chat completions (`@ai-sdk/openai-compatible`).

## Qoder CN

`qoder-cn` is for accounts on [qoder.cn](https://qoder.cn), the China site.
Those accounts sign in with Alibaba Cloud or a phone number and can't sign
in through `qoder`. Qoder CN speaks the same protocol as qoder.com:

- the same device flow, poll, job token and refresh paths;
- the same COSY envelope and body codec;
- the same model list, chat and usage paths.

Only the hosts and the sign-in's client id differ. The plugin uses the ones
Qoder CN's own CLI builds in for its `cn` build (`@qodercn-ai/qoderclicn`
1.1.65):

| | `qoder` | `qoder-cn` |
|---|---|---|
| Sign-in page | qoder.com | qoder.cn |
| Accounts: poll, tokens, user info, usage | openapi.qoder.sh | openapi.qoder.com.cn |
| Models and chat | api3.qoder.sh | gateway.qoder.com.cn |
| Device-flow client id | Qoder's desktop client's | Qoder CN's CLI's (production) |
| `redirect_uri` on the sign-in page | none, as Qoder's CLI sends (since 0.2.25) | none, as the CLI sends |

Qoder CN's CLI chats with the device token itself and never makes a job
token. The plugin asks qoder.cn for a job token first, as it does on
qoder.com. If qoder.cn refuses it (a 4xx), the account works the way the
CLI does:

- the device token signs the model calls;
- it is renewed with `/api/v1/deviceToken/refresh`, the chat and account
  tokens being one pair;
- a refused renewal (401 or 403) means signing in again.

Until you sign in, the `config` hook declares the tiers Qoder CN's CLI names
(Ultimate, Performance, Efficient, Lite). Once you are signed in, the
account's own list from gateway.qoder.com.cn replaces them.

Neither magpie's built-in Qoder CN nor this plugin has been checked against
every kind of qoder.cn account. If a sign-in or a chat fails, please open
an issue with the error.

### Enterprise VPC

A Qoder CN enterprise on its own VPC instance signs in with **Sign in with
Qoder CN Enterprise (VPC)** (since 0.2.8). It asks for the instance: its
name (`acme`) or one of its addresses (`acme.vpc.qoder.com.cn`,
`https://acme.vpc.qoder.com.cn`). Only https and hosts under
`vpc.qoder.com.cn` are taken.

The account then uses the instance's own hosts, as Qoder CN's CLI
(`@qodercn-ai/qoderclicn` 1.1.64) does once a VPC instance is set. The
paths, client id and protocol are the public ones:

| | Public Qoder CN | VPC instance `acme` |
|---|---|---|
| Sign-in page | qoder.cn | acme.vpc.qoder.com.cn |
| Accounts: poll, tokens, user info, usage | openapi.qoder.com.cn | acme-openapi.vpc.qoder.com.cn |
| Models and chat | gateway.qoder.com.cn | acme-gateway.vpc.qoder.com.cn |

- The instance is kept with the account, so its refreshes, models, usage
  and check-in go to the instance too. An account kept with an instance
  that isn't one is asked to sign in again; it is never sent to the public
  hosts.
- The account is named `<email> (<instance>)`, so it is listed apart from
  the same person's public account.
- An enterprise's usage shows as the Enterprise plan.
- **Sign in with Qoder CN** is unchanged and still the first way. An
  account signed in without an instance works as before.

The hosts and reply shapes come from a real VPC account's report
(yetone/magpie#312). This plugin has not been tried with a VPC account.

## QwenWork (千问办公)

`QwenWork` and `QwenWork 企业版` serve Alibaba's own QwenWork (qwenwork.cn)
on this same protocol, with the desktop client's client id, an RSA key the
Qoder one's equal, its own `qwork` scene, and the device token as the chat
token. The public cloud's sign-in is the desktop app's device flow, with
`redirect_uri=qwenwork-cn://`; usage reads the sign-in's account-context.
A personal account's quota there has `total` and `used` null, only
`remaining`, so the card shows what is left as its balance, with each pool
(daily, monthly, long-term) from `/api/v1/adapter/user/wallets`, as
QwenWork's own client reads them; no share used is made up. An account
with a total keeps its used / total window, and a biz account (or the
enterprise flagship) never asks for the wallets.
There is no PAT and no daily check-in on it: the `pat: false` and
`checkin: false` site fields turn both off, so Qoder's device identity is
never sent there.

The enterprise flagship (企业旗舰版, a VPC / private deployment) serves
web/openapi/api on the enterprise's **own single domain**, and its device
flow sends **no `redirect_uri`** (`/device/selectAccounts` refuses one).
Its sign-in asks for that domain — the host the admin console lives on,
like `your-company.vpc.qwenwork.cn`; `QWENWORK_ENT_DOMAIN` presets it so
the sign-in asks nothing. Every call — chat, models, refresh, usage —
routes to that host, and the account is named `<email|uid> (<domain>)`.
The login page expires in 5 minutes; a member signing in by email OTP
must have their email bound to their account in the admin console, or the
page answers 用户不存在.

The flagship deploys no model-catalog service (`/algo/api/v2/model/list`
answers 503 there), so its list falls back to the public cloud's qwork
models shipped in this package: flash, pro and qwen3.8-max-preview. A 503
falls back; any other error doesn't.

## Daily check-in

Qoder gives credits for a daily claim (the Qoder client's campaign
`CLAIM_BENEFIT`, 100 Credits a day as reported), on both sites. magpie can
claim it for each account when its Qoder check-in switch is on
(Settings), through `auth.checkin`: an already claimed campaign is `done`,
a campaign not available is `inactive`, and a claimable one is `claimed`
with its benefit amount. It asks these pages through this plugin's own
campaigns path (since 0.2.7),
which sends them as the account, on the device token (`Bearer`,
`Cosy-ClientType: 10`) as usage is read; a refused device token is
rotated once and the page asked again.

International campaigns also need Qoder's native device identity:
`Cosy-MachineToken` and `Cosy-MachineType`. Without them, the server can
return HTTP 200 but omit the daily claim. The plugin reads them from
Qoder's own `runtime-info` helper for the signed-in uid, sharing an
in-flight read and caching it in memory for an hour. The same identity
goes on the list, the claim and a device-token retry; it is not saved in
the account's credentials.

- Install Qoder desktop, or sign in to Qoder CLI once, to make its native
  runtime available. The plugin finds the desktop's `resources/umid`
  (`Qoder.app` on macOS, `%LOCALAPPDATA%\Programs\Qoder` or, since
  0.2.26, `%ProgramFiles%\Qoder` on Windows, `/opt/Qoder` on Linux) or the runtime the CLI caches in `~/.qoder/.bin`
  (`runtime-info-<platform>-<arch>-*` on macOS and Linux,
  `umid-win32-x64-*\runtime-info.exe` on Windows; `QODER_CONFIG_DIR`,
  `QODER_CLI_HOME` and `QODER_CONFIG_DIR_NAME` move it as they move the
  CLI's folder).
- For another installation location, set `QODER_RUNTIME_INFO` to the
  absolute path of Qoder's installed `runtime-info` executable
  (`runtime-info.exe` on Windows), with its accompanying SDK files.
- With no helper anywhere (a machine that never had Qoder desktop or
  Qoder CLI), the check-in is `ineligible` for the day, with a message
  saying to install one (or set `QODER_RUNTIME_INFO`); magpie asks again
  the next day rather than every 30 minutes (#304). The identity comes
  only from Qoder's own helper: the plugin never makes one up, so such an
  international account gets no daily claim until Qoder is installed.
- A helper that is there but can't be read, or gives an invalid identity,
  is `failed` and asked again later, rather than a false "no activity"
  result.
- With the identity read and sent, a list with no daily claim is
  `inactive`, with a message (since 0.2.32) saying Qoder offered this
  device's identity no daily credits campaign, that Qoder decides that per
  device, and that the credits can be claimed in Qoder desktop when it
  shows them (#304: a Linux CLI identity was offered none while the same
  account's desktop identity was). The plugin never sends another
  device's identity.

Qoder CN (since 0.2.30) sends its own device identity the same way when
Qoder CN is installed: its desktop 0.4.9 and CLI (`qoderclicn`) both ask
Qoder CN's `runtime-info` for environment `0` (the international one is
`3`) and send it on the campaigns pages (yetone/magpie#2911, #304).

- The plugin finds Qoder CN desktop's `resources/umid` (`Qoder CN.app` on
  macOS, `%LOCALAPPDATA%\Programs\Qoder CN` or `%ProgramFiles%\Qoder CN`
  on Windows, `/opt/Qoder-CN` on Linux) or the runtime its CLI caches in
  `~/.qoder-cn/.bin` (`QODERCN_CONFIG_DIR`, `QODERCN_CLI_HOME` and
  `QODERCN_CONFIG_DIR_NAME` move it). `QODERCN_RUNTIME_INFO` names
  another location; `QODER_RUNTIME_INFO` is only the international one's.
- Without it the requests go out as before, as Qoder CN's desktop does
  when it can't read the identity, and a claim the list still offers is
  made. When the list then offers no daily claim, the check-in is
  `ineligible` for the day with a message to install Qoder CN desktop (or
  sign in to its CLI); a helper that is there but can't be read makes it
  `failed`, asked again later.
- Not tried with a real Qoder CN account: whether Qoder CN now hides the
  daily claim from a request without the identity, as the international
  site does, is inferred from its clients, not seen.

- **List:** `GET <openapi>/sash/api/v1/me/campaigns`: `campaigns[]` with
  `campaignId`, `actionType`, `claimStatus` (`CLAIMABLE`, `CLAIMED`),
  `startAt`, `endAt`, `benefit.amount`.
- **Claim:** `POST <openapi>/sash/api/v1/me/campaigns/{id}/claim`, body
  `{}`: `status` `CLAIMED` (or under `data`).
- `<openapi>` is `openapi.qoder.sh` (Qoder) or `openapi.qoder.com.cn`
  (Qoder CN). Other pages on those hosts are still refused.

Taken from [wallechfox/qoder-checkin](https://github.com/wallechfox/qoder-checkin),
which a user reports works, and Qoder desktop 0.4.3's campaign requests.
International campaign discovery was checked with a real account on
2026-10-08: the native identity exposed the claimable 100 credits, and
omitting either header hid them. This verification only queried the
activity; a live claim was not performed.

## Credits available, but a request is refused

A positive credit balance does not guarantee a queue slot or access to
every model. Use the model ids listed for that particular account and
region. Do not infer another account's permission from its balance.

Qoder code **10605**, including `RESOURCE_LIMIT`, `serviceAvailable` or
`isQueued` details, is a resource/queue answer. Keep its suggested retry
delay; repeatedly signing in does not free that queue. Since **0.2.18**
([#185](https://github.com/magpie-community/plugins/pull/185)), a 10605 led
by a BOM or wrapped in an outer message is read as busy too; see
[#167](https://github.com/magpie-community/plugins/issues/167) for the broader report.
That parser fix does not remove the provider's limit or prove that every
live 401 had the same cause. A timeout likewise does not establish an
empty wallet or an expired login.

Since **0.2.28** a queued request waits its turn as Qoder's own client
(Qoder.app 0.4.3's agent worker) waits it: it asks
`/api/v2/service/ask/queue/status` with the request's `requestSetId`, the
model and the plan's `queueType`, at once and then every
`retryAfterSeconds` (held to 0.5–30s, 30s when unsaid), and once
`isQueued` is false it waits that state's `retryAfterSeconds` and sends the
same request again (up to 10 times). Qoder's client waits up to an hour;
the plugin waits at most 4 minutes from the request's start, since nothing
reaches the agent meanwhile (magpie keeps a stream alive with comments for
5 minutes of silence). Three failed status asks in a row, a 404, or a
cancelled request end the wait early. Qoder's client also reports the end
of its turn to `/api/v2/service/ask/finish`; the plugin doesn't, and
Qoder releases that lease on its own TTL. `queue.test.mjs` has the wait.

For a bounded offline reproduction, run `packages/qoder/busy.test.mjs`
from #185 in an isolated temporary HOME. It compares plain queue JSON,
BOM-prefixed JSON and known message wrappers, keeping explicit login and
credit failures distinct. Extracting a JSON body from a translated log
cannot recover the original wire bytes; synthetic wrappers demonstrate
parser behavior, not which wrapper a historical request actually used.
After selecting a local fix with magpie's `plugin use`, confirm which spec
serves `qoder` in `plugin list`; a patched checkout alone does not change
the running provider.


For native check-in, an existing `CLAIMED` result is not a new credit grant.
The host's recheck/cache behavior is tracked in
[magpie#2696](https://github.com/yetone/magpie/issues/2696). Verify a new
claim against the vendor record; a successful activity query alone is not
proof that credits arrived.

## protocol

`export const protocol = { makePlugin, deviceSignIn, openapi, siteOf }` is
the plugin factory and its pieces, for another service on this same
protocol (QwenWork ships here; a future one can too). Its shape is not a
stable API — it changes as the packages on it need.

## Not here

- Qoder's usage and quota display.
- Several accounts at once.
- magpie's web-search stand-in.

## Credits

The protocol (endpoints, COSY envelope, body codec and device flow) comes
from [CLIProxyAPI](https://github.com/ufec/CLIProxyAPI)'s Qoder support, by
way of magpie. Its MIT license is in `LICENSE-CLIProxyAPI`.
