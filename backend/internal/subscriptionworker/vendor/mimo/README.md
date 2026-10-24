# @magpie-community/opencode-mimo-auth

Signs in to **Xiaomi MiMo** with a Xiaomi account, the way the MiMo desktop
app does, and makes its model requests. Provider id: `mimo-app`.

## Sign-in

One way, **Xiaomi account**. The plugin opens Xiaomi's sign-in page
(account.xiaomi.com's long-poll ticket). Sign in there, or scan its QR code
with the Xiaomi phone app. The plugin waits on the ticket until Xiaomi says
the account is in, so there's nothing to paste back.

Xiaomi's passToken then signs the account on at the MiMo server. The server
sends the browser through account.xiaomi.com and back through its `/sts`,
which sets the session cookies (`serviceToken`, `userId`, `cUserId`). An
account from Russia or India is moved to that region's server
(`mimo-server-ru` / `mimo-server-in`). Everyone else stays on
`mimo-server-sgp`.

## Requests

Chat completions go to `https://mimo-server-<region>.xiaomimimo.com/api/route/chat/completions`
(`@ai-sdk/openai-compatible`). Each request carries:

- the session cookies, with no `Authorization` header
- `X-Mimo-Source: mimocode-cli-free`
- `X-Client-Version: 26.929.292248`
- the app's User-Agent

`mimo-auto`, the app's default model, is requested as `mimo-pro`.

The session is renewed with the passToken a day after it was issued.
magpie (0.1.684 and later) does it ahead of time through `auth.refresh`, ten
minutes before that day is out (`refreshLead`), once for the account and
before its requests, models and usage ask for it; a passToken Xiaomi no
longer takes marks the account for a new sign-in. The check before each
request stays for OpenCode, which doesn't call `auth.refresh`, and one
sign-on at a time serves both. A model
request the server turns away (a 401) is answered as the server answered it,
as magpie's built-in MiMo account did; reading usage renews the session once
when the server turns it away. If the passToken no longer works, sign in
again.

### An account with a Token Plan and no app membership

The app's server answers such an account's requests, every model, with
403 `membership_required` ("未开通会员或会员已到期，请订阅后使用"). The
plugin then asks the account's Token Plan instead. The passToken signs on
at the open platform (platform.xiaomimimo.com, sid `api-platform`), which
gives the plan's `tp-` key (`/tokenPlan/apiKey/raw`) and its endpoint
(`openaiBaseUrl` of `/tokenPlan/apiKey`, e.g.
`https://token-plan-cn.xiaomimimo.com/v1`). The request goes there with
`Authorization: Bearer <tp- key>` and no cookies, with `mimo-pro` asked as
`mimo-v2.6-pro` and `mimo-flash` as `mimo-v2.6-flash`. For an hour after,
the account's requests go straight to the plan; then the app is asked
first again, in case a membership was bought. A key reset at the platform
(the plan's 401) is read again on the next request. An account with no
Token Plan, or a plan with no key yet (the platform's Token Plan page
makes one), gets the app's 403 as it is.

## Usage

magpie's Usage card shows the MiMo app's plan and its week's allowance.
A Token Plan bought at the open platform (platform.xiaomimimo.com) is a
plan of its own, spent by its `tp-` API key at
`token-plan-<region>.xiaomimimo.com`. The same passToken signs on at the
platform (sid `api-platform`), so the card shows the Token Plan too. With
an app plan, its credits are an aside beside the week's allowance: this
account's requests go to the app. With no app plan, the Token Plan is the
card's plan and its credits are the account's allowance, since its
requests go to the plan (above).

## Where the sign-in is kept

In OpenCode's `auth.json`, or magpie's `plugin-auth.json`, as an OAuth sign-in:

- `refresh`: the Xiaomi account as JSON (`userId`, `cUserId`, `passToken`, `deviceId`, `region`, `base`)
- `access`: the session cookies as JSON
- `expires`: when the session is renewed
- `accountId`: the Xiaomi user id

## Models

| Model | Name | Context | Output | Input |
|---|---|---|---|---|
| `mimo-pro` | MiMo Pro | 1,000,000 | 128,000 | text, image |
| `mimo-flash` | MiMo Flash | 1,000,000 | 128,000 | text, image |
