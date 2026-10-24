# @magpie-community/opencode-raccoon-auth

OpenCode & Magpie provider plugin for **SenseTime Raccoon (商汤小浣熊, `xiaohuanxiong.com`)**.

## Features

- **Sign-in methods**:
  1. Browser OAuth with automatic loopback return: confirm authorization in the browser; no desktop client or pasted code is needed.
  2. Manual callback URL / token fallback (paste `office-raccoon://auth/callback?code=...`, or raw JWT `access_token` / JSON token pair).
  3. Raccoon desktop live session reuse (strictly read-only, reading `~/.box-agent/config/auth.json` without writing or spending desktop refresh tokens). A desktop seat keeps the token it was signed in with on its own record, so it is never answered with, and never reports, another account's credentials or credits; once its own token is refused or runs out it asks to be signed in again. The app's own refresh token is still never spent.
- **Token renewal**: single-flight proactive refresh 5 minutes before expiration for browser logins.
- **Dynamic model catalog & credit rates**: real-time synchronization from `/api/web/llm/v2/model_catalog` with context window, output limit, vision/reasoning tags, credit multipliers, and limited-free markers.
- **Usage & allowances**: reports available credits, unexpired grant bills, daily expiration timestamps, and Pro/Free subscription plans.
- **Daily check-in & desktop onboarding grant**: supports Magpie automatic daily check-in (`+300` credits/day) and one-time desktop onboarding grant (`+3000` credits).
- **SSE model rewrite**: transparently rewrites internal upstream model names back to requested downstream models for client compatibility.

## 中文

商汤小浣熊（SenseTime Raccoon / `xiaohuanxiong.com`）OpenCode & Magpie 提供商插件。

### 功能特性

- **多种登录方式**：
  1. 浏览器 OAuth 授权登录：确认授权后自动返回，无需安装官方桌面客户端，也无需输入短信验证码或粘贴网页地址。
  2. 手动回调 / Token 备用入口：粘贴 `office-raccoon://auth/callback?code=...` 完整回调链接，或 `access_token` / JSON；普通授权网页地址和单独授权码不能代替完整回调。
  3. 直接只读复用本机商汤小浣熊客户端实时登录态（`~/.box-agent/config/auth.json`）。桌面端席位会把登录时读到的 token 保存在自己的账号记录里，因此它不会拿到、也不会**上报**另一个账号的凭据或积分；一旦自己的 token 被拒或用尽，就提示重新登录（客户端自身的 refresh token 依旧不会被动用）。
- **Token 自动续期**：浏览器登录账号过期前 5 分钟自动单飞刷新。
- **动态模型目录与积分倍率**：实时同步 `/api/web/llm/v2/model_catalog`，展示模型上下文、最大输出、视觉/思考能力、积分倍率与限免（`Free`）标识。
- **积分余额与套餐查询**：展示可用积分、未过期发放额度、每日积分过期时间与 Pro/Free 套餐状态。
- **每日签到与桌面端登录奖励**：支持 Magpie 每日自动签到（`+300` 积分/天）及首次桌面端 `+3000` 积分登录奖励领取。
- **SSE 模型名重写**：将上游响应中的内部模型名重写为下游请求的 `model`，确保 Claude Code / Codex / OpenCode 等客户端兼容。

### Browser callback lifecycle

The browser flow uses the authorize page's `desktop_callback=loopback` contract, a random 64-character hex state and `http://127.0.0.1:<port>/auth/callback`. The listener accepts only the matching host, GET path and state. Invalid callbacks leave the pending login intact; success, authorization refusal, exchange failure or the ten-minute deadline close it. A browser page says success only after the code exchange succeeds; response pages contain no code or token and disable caching. Closing/cancelling the host UI does not notify this plugin through the OpenCode callback contract, so an abandoned listener expires at that deadline. When a remote browser cannot reach that machine's loopback, Magpie can forward a pasted final callback URL; the separate manual method remains available as well.

### Desktop seats

The desktop app is signed in to one account at a time and keeps only that account's JWT in `auth.json`. A desktop seat therefore saves the token it was signed in with on its own record and uses it for every request, reading the app only to pick up a newer token for that same account. A seat whose account the app no longer holds is never answered with the app's current token: it keeps using its own until that token is refused, and then asks to be signed in again, rather than being billed to an account it does not name. Adding a seat takes only the account the app holds at that moment.

Note that signing in to a second account in the desktop app **revokes the first account's session on the vendor's side**, so the first seat's token stops being accepted then — its `exp` notwithstanding — and is answered `401 {"code":200003}` by the credits and check-in endpoints, with every refresh endpoint answering 404 for it ([#166](https://github.com/magpie-community/plugins/pull/166), measured on a real pair of accounts). Two desktop seats are therefore one live account plus one seat waiting to be signed in again, never two working seats. For two accounts that both work, sign in twice through the browser method.
