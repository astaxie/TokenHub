# @magpie-community/opencode-codearts-auth

Signs in to **Huawei Cloud CodeArts** (华为云码道, the CodeArts Agent coding plans: Trial,
personal and enterprise credit plans) and serves its models — GLM, DeepSeek, openPangu and
more — in OpenCode and magpie. Provider id: `codearts`.

> **Unofficial.** This package is not affiliated with, authorized or endorsed by Huawei,
> Huawei Cloud or CodeArts; their names and marks belong to their owners. It talks to the
> non-public APIs the CodeArts client uses and identifies itself the way that client does
> (including claiming the daily free benefit and check-in), which may not be allowed by Huawei
> Cloud's terms of service, and those APIs may change at any time. Use it at your own risk,
> including the risk of restrictions on your account.

## Install

```sh
magpie plugin add @magpie-community/opencode-codearts-auth
magpie plugin login codearts
```

or install it from magpie's Plugins tab and sign in there.

## Sign-in

**Sign in with Huawei Cloud** opens Huawei Cloud's consent page. The plugin listens on a
loopback port (`127.0.0.1`, ≥ 10000) for the redirect and exchanges the code at Huawei Cloud
STS — a PKCE flow whose token requests carry an ES256 DPoP proof, as the CodeArts client does.
STS answers with temporary AK/SK/security-token credentials and a refresh token. The callback
server takes one GET to `/oauth/callback`, exactly; one that comes back without a code (the
portal's error) fails the sign-in at once.

**Where it is kept:** the whole credential (AK/SK/security token, expiry, refresh token, the
PKCE verifier and the DPoP private key) is a JSON string in the account's `access` field, in
magpie's (or OpenCode's) own account store. `auth.refresh` renews it 30 minutes before it
expires, reusing the same verifier and DPoP key; a stale credential met on a request
(`401`, `APIG.0602`) is renewed once and the request sent again. A refresh token is spent once
per account, however many loaders ask; a renewal that only fails for now (STS unreachable)
answers `503` and keeps the sign-in, and only one STS turns away for good marks it. The
account id is the signer named in the refresh token, so signing in again replaces the account
instead of adding one.

## Models

The account's live list: CodeArts' builtin models, plus the free models of its daily benefit
once today's benefit has been claimed (`POST /api/v1/benefit/claim`, once a Beijing day, as the
client does; a `benefit not found` on a day not yet claimed claims it too). Each model's credit
rate (the client's 「消耗系数」) is reported as magpie's `rate`,
with its reasoning flag and limits, from the account's main agent (`build`) model list. Before
signing in, a short built-in catalog stands in.

## Requests

- Every request is signed with Huawei's SDK-HMAC-SHA256 using the temporary credentials, and
  only for CodeArts' own two hosts, `snap-access.cn-north-4.myhuaweicloud.com` and
  `opengw.developer.huaweicloud.com`: a request anywhere else (an overridden `api`) is refused
  before the credentials leave, and no signed or token request follows a redirect.
- The gateway allows 3 concurrent sessions per account and tells them apart by
  `x-ot-session-id`; the plugin sends one stable session id per account (and a fresh trace per
  request) like the client, keeps at most 3 chats in flight per account, retries
  `TM.00001041` with backoff, and waits in the gateway's own queue (`429` +
  `queue-status: queued`) the way the client does. Only then does magpie get a `429`.
- Chats always go upstream as streams — CodeArts meters only streamed answers — and are folded
  back into one `chat.completion` when the caller asked for JSON.
- `thinking: { type: "enabled" }` is sent unless the caller set it, so the reasoning lands in
  `reasoning_content` and `content` is only the reply.
- Errors the gateway puts inside a `200` stream (spent quota, unknown model) come back as
  `429` / `400` / `502` with the vendor's words. The `codearts/` prefix is stripped and output
  budgets are clamped to 65536.

## Usage and check-in

- Usage: the credit buckets of `snap-manager/v1/statistics/plugin` (only the package total
  pauses the account; the basic / on-demand / bonus buckets are shown aside) and the free daily
  token pool.
- When each credit batch expires is read from the CodeArts console's own calls
  (`snap-manager/v1/package/credit/page`, `v1/ops/credit/*`); when those can't be read, the
  plugin falls back to a small ledger of the check-ins it claimed,
  `~/.config/magpie/codearts-checkins.json` (or `MAGPIE_CODEARTS_LEDGER`). This part is still
  being checked against more accounts — trust the CodeArts console where they differ.
- `auth.checkin`: the client's 「限时福利 → 每日领取」 — `GET /v1/ops/delivery?channel=IDE`,
  `POST /v1/ops/claim`, then `POST /v1/ops/confirm` when the claim answers an id. A claim whose
  confirm failed is kept in that ledger for the day and confirmed at the next check-in; one the
  client leaves unconfirmed (`id: null`) never is. Turn on
  *Auto check-in* for CodeArts under **Settings → Usage → Plugins**.

## Options

| Option | Default | What it does |
|---|---|---|
| `maxInflight` | `3` | chats in flight per account; it can only lower the vendor's cap of 3 sessions |
| `retryBudgetMs` | `150000` | how long to retry `TM.00001041` before answering `429` |
| `retryDelays` | `[2000,4000,6000,8000,10000]` | retry waits in ms (the last repeats) |
| `queueBudgetMs` | `300000` | how long to wait in the gateway's queue |
| `thinking` | `true` | `false` stops adding `thinking: { type: "enabled" }` |

## Credits

First written by [yuweihao17](https://github.com/yuweihao17/codearts); this version carries the
fixes of [yuweihao17/codearts#8](https://github.com/yuweihao17/codearts/pull/8). It builds on the
measurements and code of [HuHong-Tao](https://github.com/yuweihao17/codearts/pull/6) and
[ReachForStar](https://github.com/ReachForStar/codearts), and on the CodeArts channel of
[Ebony-Vinyl/dsh-our-free-model](https://github.com/Ebony-Vinyl/dsh-our-free-model).

## 中文

接入**华为云码道（CodeArts Agent）**的编码套餐（体验版、个人与企业积分套餐），在 OpenCode 和
magpie 里使用其 GLM、DeepSeek、openPangu 等模型。provider id：`codearts`。

> **非官方插件**：与华为、华为云及 CodeArts（码道）没有任何关联，也未获其授权或认可。插件调用的是
> 码道客户端使用的非公开接口，并以与官方客户端相同的方式标识自己（含自动领取每日免费权益与签到），
> 这可能不符合华为云的服务条款，接口也可能随时变更。使用风险（包括账号受限）由使用者自行承担。

- **登录**：华为云 OAuth（PKCE + DPoP），本机 `127.0.0.1`（端口 ≥ 10000）接收回调，向 STS 换取临时
  AK/SK/security-token；回调只接受精确的 `/oauth/callback` GET，回调不带 code（portal 报错）时立即判定登录失败。完整凭据（含 refresh_token、PKCE verifier、DPoP 私钥）以 JSON 存在账号的
  `access` 字段里，保存在 magpie / OpenCode 自己的账号存储中；过期前 30 分钟自动续期，请求遇到临时凭据
  过期时就地续期并重发一次。同一账号的 refresh_token 只用一次；续期只是暂时失败（如 STS 连不上）时返回
  `503` 并保留登录，只有被 STS 明确拒绝才标记需要重新登录。账号标识取自 refresh_token，重复登录不会多出账号。
- **模型**：账号实时的内置模型列表；领取当日免费权益成功后，再加上免费模型（每个北京自然日领一次，与
  客户端一致；当天还没领取时遇到 `benefit not found` 也会先领取再重发）。每个模型的「消耗系数」作为 magpie 的倍率上报，来自账号主智能体（`build`）的模型列表。
- **请求**：SDK-HMAC-SHA256 签名，只签给码道自己的两个主机（`snap-access.cn-north-4.myhuaweicloud.com`、
  `opengw.developer.huaweicloud.com`），发往其他地址（如改过的 `api`）的请求在凭据发出前就被拒绝，签名请求和令牌请求都不跟随重定向；像客户端一样发送 `x-ot-session-id` 会话头（每账号一个会话），账号内
  在途请求不超过 3 个，`TM.00001041` 退避重试，网关排队时按客户端的方式等待；总是以流式请求上游（码道
  只对流式回答计量积分），调用方要非流式时折叠回 JSON；默认注入 `thinking: { type: "enabled" }`，思考与
  正文分开；200 里内嵌的错误换算为 `429` / `400` / `502` 并带厂商原话。
- **用量与签到**：总积分决定账号是否可用，其余积分桶只做明细；另有每日免费额度窗口。各批积分的到期
  时间读自码道控制台同一批接口，读不到时退回插件的签到账本 `~/.config/magpie/codearts-checkins.json`
  （这部分仍在用更多账号验证，请以控制台为准）。`auth.checkin` 与客户端「限时福利 → 每日领取」一致
  （插件领取后确认失败的，当天记在签到账本里，下次签到时补确认；客户端本就不确认的 `id: null` 领取不会被确认），
  在 **设置 → 用量 → Plugins** 打开「每日自动签到」即可。
- **致谢**：原作者 [yuweihao17](https://github.com/yuweihao17/codearts)；HuHong-Tao、ReachForStar 的
  实测与实现；[Ebony-Vinyl/dsh-our-free-model](https://github.com/Ebony-Vinyl/dsh-our-free-model)。

## License

MIT — see [LICENSE](./LICENSE).
