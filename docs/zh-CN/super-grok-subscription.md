# 通过 TokenHub 使用 Super Grok 订阅

Language: [English](../super-grok-subscription.md) | 简体中文 | [日本語](../ja/super-grok-subscription.md)

TokenHub 可以把 Super Grok / Grok CLI 账号接入为订阅 Provider。调用方继续使用 TokenHub 的项目 Key 和 OpenAI 兼容 `/v1` 接口，不需要 CCswitch 或 CLIProxyAPI。

这条路径走的是非公开的 Grok CLI chat-proxy。只应使用组织自有账号，并把它当作内网能力，而不是可转售中转。

## 前提

- 能够完成 xAI 设备码登录的 Super Grok（或 Grok CLI）账号。
- 有权限创建 Provider 和路由的管理员。
- 出站 HTTPS 可访问 `auth.x.ai` 与 `cli-chat-proxy.grok.com`。

## 接入账号

1. 打开 **Provider 渠道**，创建 Provider。
2. 选择 **账号资源池**，再选择 **Super Grok** 通道。
3. 确认聊天 Base URL 为 `https://cli-chat-proxy.grok.com/v1`。
4. 开始授权。TokenHub 会显示用户码并打开 xAI 设备页。
5. 在 xAI 页面确认后，TokenHub 加密保存 refresh token，并可以自动续租 access token。
6. 引入 `grok-4.5`、`grok-4.6`、`grok-composer-2.5-fast` 或 `grok-build-0.1` 等订阅模型，然后创建路由。

不要把 xAI API Key 填进这个 Provider。官方 `api.x.ai` Key 应使用 `openai_compatible` 通道。

## 从客户端调用 TokenHub

路由建好后，调用方使用 **Key 管理** 里的**项目 API Key**，不要使用 Provider 上保存的 Super Grok OAuth Token。控制台登录 Token 不能调用 `/v1`。

**Key 管理** 可以下载 Codex CLI 的 `config.toml` 和环境变量 `.env` 模板，**不会**下载 Grok CLI 的独立 home。Grok CLI 需要按下述方式手工配置。

### OpenAI 兼容客户端

网关根地址必须以 `/v1` 结尾，项目 Key 作为 Bearer Token。把主机换成部署里的真实 TokenHub 地址。

```bash
export TOKENHUB_BASE_URL="http://localhost:8080/v1"
read -r -s "TOKENHUB_API_KEY?TokenHub project API key: "
export TOKENHUB_API_KEY
echo

curl --fail-with-body \
  --url "${TOKENHUB_BASE_URL%/}/models" \
  --header "Authorization: Bearer ${TOKENHUB_API_KEY}"

curl --fail-with-body \
  --url "${TOKENHUB_BASE_URL%/}/chat/completions" \
  --header "Authorization: Bearer ${TOKENHUB_API_KEY}" \
  --header "Content-Type: application/json" \
  --data '{"model":"grok-4.5","messages":[{"role":"user","content":"Reply with pong."}]}'
```

`model` 必须出现在该 Key 的 `GET /v1/models` 结果中。`POST /v1/responses` 使用同一套 Base URL 和 Bearer Token。

### Grok CLI（独立 home）

不要把 TokenHub 配置写进 `~/.grok`。那个目录是官方 Grok 登录和 Super Grok 会话。`GROK_CONFIG` / `GROK_CONFIG_PATH` overlay **不能**修改推理 Base URL。

使用单独的 home，让默认的 `grok` 命令继续走 xAI：

```bash
GROK_HOME="${HOME}/.grok-tokenhub"
mkdir -p "$GROK_HOME"
chmod 700 "$GROK_HOME"
```

写入 `$GROK_HOME/config.toml`：

```toml
[models]
default = "grok-4.5"

[endpoints]
models_base_url = "http://localhost:8080/v1"

[model.grok-4.5]
model = "grok-4.5"
base_url = "http://localhost:8080/v1"
name = "TokenHub Super Grok"
api_backend = "chat_completions"
env_key = "TOKENHUB_API_KEY"
```

把 `http://localhost:8080/v1` 换成上面的网关根地址，把 `grok-4.5` 换成该 Key 允许调用的模型。然后在同一 shell 中：

```bash
export GROK_HOME="${HOME}/.grok-tokenhub"
export GROK_MODELS_BASE_URL="${TOKENHUB_BASE_URL}"
export XAI_API_KEY="${TOKENHUB_API_KEY}"
chmod 600 "$GROK_HOME/config.toml"

grok inspect
grok --model grok-4.5
grok -p "Reply with pong." --model grok-4.5 --yolo --max-turns 1
```

设置 `GROK_MODELS_BASE_URL` 后，Grok CLI 使用 `XAI_API_KEY` 发送 `Authorization: Bearer`，不再走 `grok login`。同时保留 `TOKENHUB_API_KEY`，以便 `config.toml` 里的 `env_key` 能解析。`grok inspect` 必须显示独立的 `GROK_HOME`，而不是 `~/.grok`。

取消 `GROK_HOME`（或新开一个 shell）即可回到官方 Grok 配置。不要把这些文件覆盖到 `~/.grok/config.toml` 或 `~/.grok/auth.json`。

不要把项目 Key 提交进仓库。优先用 `read -s`，或仓库外 `chmod 600` 的环境文件。

## 支持的接口

- `POST /v1/chat/completions`（含流式）
- `POST /v1/responses`（含流式）
- 演练场聊天走同一套 Responses 桥

本版本不包含：图片/视频、`/v1/responses/compact`、WebSocket、Gemini 原生 `v1beta`、Anthropic Messages，以及订阅额度面板。

## 运维说明

- TokenHub 访问 chat-proxy 时会带上当前 Grok CLI 客户端版本。xAI 可能拒绝过期版本。
- Composer 模型通过 `prompt_cache_key` / 会话头保持会话亲和。
- 定时续租与 Codex 一样使用五分钟提前量。若 xAI 作废 refresh token，账号会标记为需要重新授权。

适配器能力见 [整体架构](architecture.md) 中的 `xai_grok`。
