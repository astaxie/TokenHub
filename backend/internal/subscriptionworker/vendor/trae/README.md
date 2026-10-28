# @magpie-community/opencode-trae-auth

Two providers, one package: **Trae CN** (trae.cn, ByteDance's AI IDE, provider
id `trae-cn`) and **Trae Global** (trae.ai, the international deployment,
provider id `trae-global`). Each signs in with its own Trae account, the way
the IDE does, and makes its model requests. The free tier works as well.

The two are separate services — different domains, a different client's
catalog, barely overlapping model lists — so they are two providers rather
than a setting. They share one implementation: the request, stream and
tool-call machinery is the same code, and each realm is a table (hosts,
client, versions, usage page, fallback models, error codes) in `index.mjs`.
A fix to that machinery reaches both.

| Provider id | Signs in to | Endpoints |
|---|---|---|
| `trae-cn` | Trae CN (trae.cn) | `api.trae.cn`, `trae-api-cn.mchost.guru` |
| `trae-global` | Trae international (trae.ai) | `growsg-normal.trae.ai`, `coresg-normal.trae.ai` (SG) / `coreva-normal.trae.ai` (US) |

> **Experimental (both).** The CN protocol was worked out from two
> open-source relays, [wangqi233/trae2api](https://github.com/wangqi233/trae2api)
> and [autumnsentiment/Trae2api-cn](https://github.com/autumnsentiment/Trae2api-cn),
> and the international one from the same relays' realm table. Neither has
> been run against a real account here: Trae CN has not been tried at all, and
> Trae Global has been tried on one Free account in the SG region only — no
> Pro and no US account. Paid-account behaviour (which models a plan lists,
> what its allowance shows) is implemented from the interface's shape, not
> confirmed on a live one. If something fails, open an issue and include the
> error magpie shows.

## Sign-in

One way per provider, **Trae CN account (browser)** or **Trae Global account
(browser)**. The plugin opens that realm's authorization page
(`www.trae.cn/authorization` or `www.trae.ai/authorization`, the IDE's client
`ono9krqynydwx5`). Sign in there and allow the sign-in; the page then sends the
browser back to a callback on `http://127.0.0.1:<port>/authorize` — it accepts
no other kind of callback. The callback carries the account's Cloud-IDE-JWT,
refresh token and account. Nothing needs pasting back.

An account of one realm does not sign in to the other: trae.cn's accounts are
not trae.ai's.

At sign-in the plugin creates the account's device and names it to the
authorization page: `device_id` and `machine_id` (32 hex). Every
request then sends that same device. The relays saw requests dropped when the
device was new each time. A Trae CN `device_id` is 16 digits since 0.2.16, the
shape of the ids Trae's device service gives its clients (15 or 16 digits);
Trae Global's stays 19.

### Enterprise (SaaS) accounts

Since 0.2.10 the authorization page also offers Trae's enterprise (SaaS / SSO)
sign-in; before, the plugin asked the page to hide it (`hide_saas_login`), so
an enterprise subscription could only sign in as a personal account. A host
the sign-in names for the account is kept when it is an https host on Trae's
own domains (`trae.cn`, `trae.ai`, `mchost.guru`): a model host other than the
consumer site's own (`www`, `api`, `grow…`) takes the account's model
requests, and a token host the callback names (`authHost`) takes its
renewals. Any other host is ignored and the consumer hosts are used, as for a
personal account, which is unchanged. The account's tenant and login-type
fields are kept with the sign-in; nothing sends them.

**Not tried with a real enterprise account.** Which hosts and fields an
enterprise sign-in sends back is not known yet; if yours doesn't work, open an
issue with what the sign-in page showed.

### The international realm's region

Trae Global has two deployments. The sign-in's `AIRegion` (or a US auth host
such as `api-us-east.trae.ai`) selects the US one; anything else uses the SG
one. The US account's model requests go to `coreva-normal.trae.ai` and its
usage page to `api-us-east.trae.ai`; the SG account uses
`coresg-normal.trae.ai` and `api-sg-central.trae.ai`. Nothing has to be
configured: the region rides in the saved sign-in.

## Requests

Chat completions (`@ai-sdk/openai-compatible`) are translated for the IDE
agent's `POST https://trae-api-cn.mchost.guru/api/agent/v3/llm_utils_chat`:

- **Request:**
  - The messages go as `{role, content: [{type: "text", text}]}`.
  - The model goes as `config_name` and `model`, and as `model_name` the
    `__dev` model the function's list names for it, when it names one.
  - The function is the one whose model list has the model, the first that
    names a `__dev` model for it (in the order `chat_v3`, the classic IDE's;
    `solo_work_lite`, SOLO's Work mode; `solo_agent`, the TRAE agent's;
    `solo_agent_lite`; `solo_coder`, SOLO's coder roster). If Trae answers
    4001, 4023 or 1005, the plugin tries the others once and remembers which
    one worked.
  - `session_id` is the conversation's, as the IDE keeps one per conversation:
    the session magpie (or OpenCode) names the request by, else the chat's
    first user message, which every later turn repeats, together with the
    account the request is made as. Trae counts a conversation by it, so a
    fresh one per request made one user send that takes several steps (a tool
    result sent back, a retry, another function answering) count as a
    conversation of its own — several rows in the usage detail, each under no
    title. One conversation answered by two accounts (magpie failing over, or
    rotating, inside it) is named twice then, as two conversations, so Trae
    never reads one account's conversation as another's; that costs the two
    halves of such a conversation their single row. `request_id` is the
    request's own, fresh on every one.
- **Headers:** TRAE SOLO CN 0.1.69's (`x-ide-version`/`x-app-version`
  0.1.69, version code 20260917; Trae offers a model only to clients new
  enough for it): `Authorization: Cloud-IDE-JWT …`, `X-Cloudide-Token`,
  `x-app-id`, the device headers and `x-uid`.
- **Answer:** Trae always answers in its own SSE events:
  - `output` gives `response`/`content` and `reasoning_content`/`reasoning`.
    Text under `message`, `delta` or an event with no name is the answer's
    too.
  - `token_usage`, `done` and `error` mark usage, the end and failures.
    What `token_usage` says the prompt cache read and wrote
    (`cache_read_input_tokens`, `cache_creation_input_tokens`) goes on under
    those names, and `reasoning_tokens` as
    `completion_tokens_details.reasoning_tokens`.
    `done`'s `finish_reason` `length`/`max_tokens` is OpenAI's `length`.
  - Queue events are ignored.
  - A `{"reasoning_content": …}` object written into the text is reasoning,
    and reasoning Trae sends again is sent once.

  The plugin turns these into an OpenAI stream or a single body. A reply
  that fails (an `error` event, or Trae's stream breaking off) ends with the
  error and `[DONE]`, with no finish.
- **Tools:**
  - The tools go natively in `tools`, with `parameters` as a JSON string.
    A past call goes back as the turn Trae made it: an assistant
    `tool_calls` message carrying it (each call
    `{id, type: "function", function_call: {name, arguments}}`) and a
    `tool` message after it answering it. Trae answers such a history with
    native calls; asked for its calls in text instead — a system prompt
    describing a `<tool_call>` block, earlier calls written out as one —
    models write them as blocks, and a block the stream cuts short is an
    error (plugins#38). Trae takes no two messages of one role in a row,
    so an assistant turn's text is folded into the calls turn after it
    and consecutive calls turns join one `tool_calls` list.
  - A call with no name is dropped, and the `tool` result answering it
    with it: Trae requires the name, and an empty `tool_calls` list with a
    result naming no call is not a turn it takes.
  - A result with no text goes as `(no output)` (`(the result's image
    follows)` when it had only an image): Trae refuses the whole request
    with 4027 when a result's text is empty, reading the calls turn as
    unanswered (measured on Trae CN in #251). A result answering no call
    the history sends goes back as text in a `user` turn.
  - A request that names no tools to Trae (none sent, or `tool_choice:
    "none"`, such as a summary) sends its past calls and results as text,
    as before: every live run of the native turns carried the tools.
  - GLM also writes its own template, `<tool_call>name<arg_key>k</arg_key>
    <arg_value>v</arg_value></tool_call>` (a string kept as written, other
    types read as JSON), and sometimes a whole call into a native call's
    name; both are read as the call. A name that holds the wrapper with
    GLM's tags where its service stopped mid-call — `{"name":"bash",
    "arguments":{"command</arg_key><arg_value>ls</arg_value>` — is read too:
    the tags are the service's own handoff, so what stands between them is
    the model's text (a quote left unescaped read as the quote it is, a
    comma inside a value kept, a field's closing tag ending the value before
    it), and the fields the native call did carry are kept beside the ones
    the name's own text fills in. A name whose tool the request doesn't
    have, or one no field can be read from, goes on as written, as any other
    unknown tool does.
  - A block is read as models get it wrong too: every quote escaped
    (`{\"name\":…}`), quotes or backslashes in a string left unescaped, an
    opener with the name as an attribute (`<tool_call name="bash">` with
    `<parameter name="k">v</parameter>`), or JSON that is whole when the
    stream ends without `</tool_call>`. A block that still can't be read,
    or is cut short, is never sent on as the answer: the reply ends with an
    error (a 502 without a stream), so the gateway can try again
    (plugins#38). A `<tool_call>` in backticks or a code fence is text.
  - Kimi and MiMo sometimes write a call in the tags they were trained on:
    Qwen's `<function=name><parameter=k>v</parameter></function>` (bare or
    inside `<tool_call>`), or Anthropic's `<function_calls>` around one or
    more `<invoke name="…"><parameter name="k">v</parameter></invoke>`.
    Each is read as a call, a parameter's value as JSON unless the tool's
    schema says string, and one cut short is an error as above. These
    tags are read only for a tool the request declared; for any other, or
    in backticks or a code fence, they stay text.
  - Calls from either source become OpenAI `tool_calls`. A name matching a
    requested tool but for case or punctuation (`Read`) goes on as the
    request's (`read`); any other goes on as written, so the agent can say
    the tool doesn't exist.
  - Trae requires the message answering a call to follow the call: the
    results of parallel calls go out as consecutive `tool` messages, with
    nothing between them, or the later ones lose their answer (11148).
  - Images: Trae's lists mark each model `multimodal` true or false (the
    Doubao-Seed models, Kimi K3 and K2.6, Qwen 3.7 Plus and MiniMax M3 take
    images; GLM-5 and DeepSeek V4 don't). A model marked true is listed
    as one that takes images, and an image sent to it goes in the
    message's content as an OpenAI `image_url` part beside the text, as
    TRAE's own agent sends one (plugins#161). A model marked false gets
    "[an image, which this model can't see]" in its place. A model whose
    list hasn't been read is sent its images, unless the fallback list
    says it takes none.
    A tool result's images go in a `user` message after the whole run of
    results, not in the `tool` turn: measured on the live endpoint, an
    `image_url` part in a `tool` turn reaches Kimi K2.6 but not
    Doubao-Seed (it answered "Blue"/"Black" to a red image), while the
    user-turn placement is read by both. The results of one turn stay
    consecutive — nothing may go between them (11148) — so the images
    wait for the last of them.

The model list is the account's own, from
`/api/ide/v1/batch_get_detail_param`, every function's list in one ask, as
TRAE SOLO CN asks it (`/api/ide/v1/get_detail_param`, one function at a
time, when the batch gives none). The lists are put together: SOLO and the
TRAE agent list models `chat_v3` doesn't (deepseek-v4.1-flash is the TRAE
agent's, `solo_agent`), and SOLO's coder roster lists models no other chat
function does (`glm-5v-turbo`, `kimi-k2.5`, `minimax-m2.7`,
`qwen-3.6-plus`, each answering through `solo_coder`). Left out are the
IDE's helpers (`usage` other than
`chat_completion`: summary, fast_apply…), configs switched off and the
custom-model slots. Context is the list's `context_window_tokens.dev`,
output the `__dev` model's `max_tokens`. A model with a `__max` model and
a bigger `context_window_tokens.max` is listed again as its Max,
`<id>-max` ("… (Max)"), with that window and the `__max` model's
`max_tokens`; the two can come from different functions' lists (chat_v3
can name the `__max` model, a SOLO list the windows). It asks the
`__max` model through the function that names it, with `max_tokens` set
and `user_message_context.model_info.prompt_max_tokens` the window less
it, as Max mode does. The lists needn't agree on a model's *name*: chat_v3
calls deepseek-v4.1-flash "DeepSeek-V4-Flash 正式版", as it once called
DeepSeek-V4-Flash-Official, where the SOLO lists call it
"DeepSeek-V4.1-Flash" (yetone/magpie#681). Trae's usage page books a
request under the name of the list it was served through, so the plugin
takes the name most of a model's lists give — the one Trae's own client is
served under — and asks that model, and its Max, through a list carrying
it. Trae renamed its official entries on 2026-10-09, leaving the name its
lists mostly agree on the only thing that tells a misnamed entry apart.

Reasoning: the models reason, and each entry of the account's list says
which levels it takes — its `reasoning_effort_config`, with the options
(`light`, `high`, `extra_high`) and the default. The request takes the same
names as `reasoning_effort`, so a level an agent picks is sent as Trae
spells it: the plugin offers each model's own levels as `variants`
(`low`/`high`/`xhigh` for `light`/`high`/`extra_high`), and sends nothing
for a level the model doesn't name, or for a model whose entry names none —
Trae then serves it at its own default. An entry naming levels this map
can't spell offers none, rather than being sent one Trae would reject. A
request that wants levels for a model the list hasn't named reads the list,
and that read is remembered for five minutes whether it produced an entry or
not: a list that fails, or one that doesn't name the model, costs one read
rather than one per request. (A read costs up to two 20s timeouts when Trae's
list endpoint is slow, while the chat itself needs no list to answer.) A read
in flight is shared, not skipped: requests that arrive together — a title
request beside a session's first turn, subagents — ride the one read and all
see what it named, so a Max asked that way is still sent under its base
model's name rather than the agent's own `-max` id. The host's own catalog
refresh rides the same read, so a refresh and a request never ask twice.
A request's read swallows an expiry (the chat can still answer); the
catalog's refresh raises it, so a lapsed account is still marked — including
when the refresh merely rides a read a request started, which is why the lapse
travels on the read's record instead of being thrown from it. The host's
refresh is strict, so it reads again rather than serving a cached lapse: a
re-sign-in is asked afresh instead of being told again that the account lapsed.

The entry kept for a model is the one the request is asked through, and so
the one whose levels the request is held to, so the lists' disagreement
about a model's levels matters: `computer_use_subagent`, for one, is named
with `light`/`high` by `solo_agent` and `solo_agent_lite` and with none by
`solo_work_lite`. The three signals that pick the entry — its `__dev`
model, its not carrying another model's name, its carrying the name the
lists agree on — decide first; of entries that tie on all three, one that
names levels is kept, so a model Trae serves with levels is not left
without them by a sibling entry that names none.

Every model it lists also says it reasons in its `capabilities`
(`reasoning: true`, with `toolcall`, `temperature`, `attachment` and the
input and output modalities), the fallback list's own capabilities kept
where it has an entry: magpie reads `capabilities.reasoning` alone for a
model a plugin lists, and a model without it is shown as one that doesn't
reason (0.2.4).

When no
list can be read, the plugin uses the realm's own fallback list. Trae CN's
`chat_v3` is known to serve GLM-5.2, GLM-5, Kimi K2.6, Qwen 3.7 Plus,
DeepSeek V4 Pro and DeepSeek V4 Flash; Trae Global's serves GPT-5.4,
Gemini 3 Flash, Kimi K2.5, MiniMax M2 and DeepSeek V3.2. The live list
replaces either.

## Renewal

Trae CN (since 0.2.3) renews the JWT as its clients do, bound to the
account's device:

- **Where:** `POST api.trae.cn/trae/api/v3/oauth/ExchangeToken`, with the
  refresh token, the device (`DeviceInfo`: its id, machine id and public
  key) and a `DeviceProof`.
- **The device key:** an ECDSA P-256 key pair made for each account at
  sign-in (or at the first renewal of one signed in before 0.2.3), kept
  with the sign-in.
- **The proof:** the key's ECDSA-SHA256 signature (base64) of
  `POST\n/trae/api/v3/oauth/ExchangeToken\n{ClientID}\n{RefreshToken}\n{Timestamp}\n{Nonce}`.
- **As whom:** TRAE SOLO CN (`en1oxy7wnw8j9n`, `SOLO_PC`) first, then
  Trae CN's IDE (`ono9krqynydwx5`, `IDE_PC`).
- **The refresh token:** kept; this renewal doesn't spend it.

A token the sign-in page gives is renewed this way at once. When Trae
turns the device-bound renewal away, the plugin renews as before 0.2.3.

Trae Global, and Trae CN as a fallback, renew at the realm's auth host's
`/cloudide/api/v3/trae/oauth/ExchangeToken` (`api.trae.cn`,
`growsg-normal.trae.ai`). There Trae issues a new refresh token each time
and spends the old one, so only one renewal runs at a time.

magpie renews the JWT ten minutes before it ends (`refreshLead`) through
`auth.refresh`. Each request also checks it, two minutes before the end, for
OpenCode.

The account is marked for a new sign-in in these cases:

- the refresh token is turned away;
- a request is answered 401, or with a code that means the sign-in lapsed:
  1001 for both realms, and 20101 for Trae Global.

Trae signs other clients out of an account when its token is renewed. The
IDE may therefore ask you to sign in again after magpie renews.

## Errors

| Trae's answer | What the agent gets |
| --- | --- |
| 401 / code 1001 (both), 20101 (Global) | 401, and the account is marked for a new sign-in |
| code 4008 / 1005 (quota, plan; both), 4011 (Global) | 429 |
| Anything else | Trae's message and code, as a 502 or Trae's own status |

## Usage

magpie's usage card shows the account's plan and allowance, from whichever
page the realm bills on:

**Trae CN** — the credits page:

- **Source:** `api.trae.cn/trae/api/v2/pay/ide_user_ent_usage`.
- **What it adds up:** each entitlement pack's `credits_limit` (-1 means
  unlimited) and what the pack has used.
- **What it shows:** one Credits window: the credits used of the packs'
  total, with the share, which magpie shows as used or left like
  WorkBuddy's.

**Trae Global** — the dollar billing's entitlements:

- **Source:** `POST api-sg-central.trae.ai/trae/api/v1/pay/user_current_entitlement_list`
  (a US account asks `api-us-east.trae.ai`).
- **What it adds up:** the pack's allowance in dollars
  (`basic_usage_limit` plus `bonus_usage_limit`) and what was used of each
  (`basic_usage_amount`, `bonus_usage_amount`).
- **What it shows:** one Dollar Usage window, the dollars used of the
  allowance, resetting when the pack ends.

A 200 carrying an error code is shown as Trae's own message rather than as
an empty answer.

## Daily check-in

Trae CN gives credits for a daily check-in (每日签到); Trae Global has no
such page, so this is CN only. magpie can press it
once a day for each account (Settings, or the switch on the usage card),
through `auth.checkin`: `claimed` when the status says today's is in after
the claim, `done` when it said so before it, `inactive` when the page is
disabled, `ineligible` when the claim answers 9095 (this device has
checked in today) or 9074 (Trae did not take it for this account's
device) or answers code 0 while the read-back still says the day is out,
and `failed` for a turned-away sign-in or any answer that got nowhere,
which is tried again later that day. An `ineligible` is the day's answer:
magpie asks that account no more until tomorrow, and only reads its
status.
It asks Trae CN's own pages through
this plugin's fetch, which sends them as the account, as Trae CN's IDE
(3.3.104) sends them: its Cloud-IDE-JWT (renewed first when near its
end), its device (`x-device-id`, `x-device-brand`, `x-device-type`,
`x-os-version`, `x-app-version`) and a client's User-Agent (TRAE SOLO
CN's Electron shell). Since 0.2.1: Bun's own User-Agent and the chat's
headers got 9074 「当前参与用户太多，请稍后再试」 every time
(yetone/magpie#808). Since 0.2.3 the JWT is also bound to the device (see
Renewal): a JWT renewed at `/cloudide/…` carries no device, and the claim
answered it 9074 even with the client's headers.

- **Status:** `POST api.trae.cn/trae/api/v2/ug/checkin_credits/status`,
  body `{"req_source":1}` (magpie's `{}` is sent as that): `enable`,
  `checked_in`, `credits`.
- **Claim:** `POST api.trae.cn/trae/api/v2/ug/checkin_credits/claim`,
  the same, only while it is on and today's isn't in.
- **The device, when Trae CN's own client is here (0.2.14):** the
  account this machine's Trae CN desktop client is signed in as checks in
  on the client's own device, the one Trae's device service registered:
  `x-device-id` is the id in the client's
  `globalStorage/storage.json` key `iCubeAuthInfo://icube-dc:<id>`,
  `x-device-type` and `x-os-version` are this machine's, and no
  `x-device-brand` is sent. The account is matched by the `userId` in the
  client's sign-in (`iCubeAuthInfo://icube.cloudide`, plain JSON or the
  client's fixed codec, as dingminhua/dsh-connect-trae reads it). The file
  is only read: nothing in it is written, and the client's token is never
  used. On one account the client's claim went through while the
  plugin's, on its own device, got 9074 (yetone/magpie#808, #1920); that
  this header is what changes it hasn't been tried on a real account
  yet. Every other account keeps its own device, and a file that can't be
  read or made out, or holds no single device, changes nothing.
- **Every profile is read (0.2.21):** a machine can hold more than one
  Trae CN client profile — the IDE's and TRAE SOLO CN's each write their
  own `storage.json`, and each names the account that profile is signed in
  as, with that account's device. So every profile is read and the one
  signed in as the account answers with its device, which is how two
  accounts can each check in on a real client's device. Before, only the
  first profile was read and it decided for all of them: a second
  account's own profile was never seen, and one profile signed in as
  someone else (or one file that didn't parse) hid every profile after it.
  A profile that isn't signed in as the account, or that names no single
  device, is passed over rather than ending the search. Files: macOS
  `~/Library/Application Support/Trae CN/…` and `…/TRAE SOLO CN/…`, Windows
  `%APPDATA%\Trae CN\…`, `…\TRAE SOLO CN\…` (or `trae-cn`), Linux
  `~/.config/trae-cn/…`, `…/Trae CN/…`, `…/TRAE SOLO CN/…`. The IDE's
  folder has been seen in a real install (macOS and Windows); TRAE SOLO
  CN's name and its `iCubeAuthInfo://` keys are read as the same client
  of the same realm writes them, which hasn't been checked against real
  files — harmless if they differ, since a folder that isn't there, or a
  key that isn't named, is simply not read. A client started with its own
  `--user-data-dir` is not found: there is no list of those to read.
- **Every other account's device (0.2.16):** its own device id, in the
  client's shape. Trae CN answered a claim on the plugin's 19-digit id
  9074, while the same account and token on a 16-digit id were taken
  (magpie-community/plugins#194). An account signed in since 0.2.16 has a
  16-digit id and checks in on it. One signed in before checks in on a
  16-digit id made from its own device id and uid: the same id every day,
  never rotated, and no other account's. Its chat requests and renewals
  keep the 19-digit device it signed in with.
- **What comes back:** Trae's answer as it is. A 401 or code 1001 marks
  the account for a new sign-in. Code 9095 means this device has checked
  in today, and that holds for the day. Code 9074 means Trae did not take
  the claim for this account's device: its own words say the pool is busy,
  which two measurements do not bear out — the same account, token and
  headers were taken on a 16-digit id where a 19-digit one got 9074
  (plugins#194), and on 2026-10-23 the same account and headers were taken
  on a device id Trae had never seen while this account's own id answered
  9074. The message says so, and does not send people to Trae CN's app,
  which is not where the problem is. Signing the account in to a Trae CN
  client on this machine is the one thing seen to change a 9074, but it
  hasn't been tried on a real account, so it isn't named in the message;
  nor is it named when the claim already rode a client's device, where it
  would only tell the user to do what they did. No device id is ever
  changed or rotated to get round a 9095 or a 9074, and no account is
  given another account's device or its own client's device unless a
  client is signed in as that account.
- **A claim's code 0 is not the answer (0.2.22):** Trae answers a
  claim code 0 for one that registered nothing, so the status is read back
  after every claim and only a day that is in is reported as `claimed`.
  Since 0.2.22 one that isn't is an `ineligible` — the day's answer, not a
  `failed` to be tried again: a `failed` is retried with backoff for the
  rest of the day and each retry is another claim, which for an account
  Trae keeps answering code 0 would turn one claim a day into many, and a
  status that lags a claim that did land would read as not in once and
  claim again. Nothing is claimed again that day; the status alone is
  read, so a lag that catches up is still reported as `done`. The credit a
  claim granted is its `credits`, or its `extra_credits` when it names
  only that; Trae CN's claim usually names neither
  (`{"code":0,"message":"success"}`), and then the read-back's `credits`
  is the credit. The two are never added: the status carries
  `extra_credits` beside `credits` (100 and 100 on a real account) while
  the day gives 100.

Only `api.trae.cn`'s `/trae/api/` pages are sent this way; any other URL
that isn't a chat request is still refused (400). Needs 0.1.4 or later.
