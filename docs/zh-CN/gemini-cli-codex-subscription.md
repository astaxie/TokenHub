# Gemini CLI 通过 TokenHub 使用 Codex 订阅 GPT

TokenHub 提供 Gemini 原生 `v1beta` 兼容接口。官方 Gemini CLI 可以直接把请求发送到 TokenHub，再由 TokenHub 路由到 OpenAI Codex Subscription 账号；不需要 CCswitch 或其他本地协议代理。

## 前置条件

- TokenHub 中已经添加可用的 OpenAI Codex Subscription 账号。
- 已启用一个 GPT 模型，并把该模型路由到 Codex Subscription Provider，例如 `gpt-5.5`。
- 项目 Key 允许访问该模型。
- 本机已安装支持 `GOOGLE_GEMINI_BASE_URL` 的 Gemini CLI。可运行 `gemini --version` 检查。

TokenHub 地址必须使用 HTTPS；只有 `localhost`、`127.0.0.1` 和 `[::1]` 可以使用 HTTP。Base URL 不要添加 `/v1beta`。

## 不修改现有配置的启动方式

推荐先使用仅对当前命令生效的环境变量。以下命令不会修改 `~/.gemini/settings.json`：

```bash
export TOKENHUB_GEMINI_KEY='你的 TokenHub 项目 Key'

GEMINI_API_KEY="$TOKENHUB_GEMINI_KEY" \
GOOGLE_GEMINI_BASE_URL='https://tokenhub.example.com' \
GEMINI_MODEL='gpt-5.5' \
gemini -m gpt-5.5

unset TOKENHUB_GEMINI_KEY
```

本地开发实例示例：

```bash
export TOKENHUB_GEMINI_KEY='你的 TokenHub 项目 Key'

GEMINI_API_KEY="$TOKENHUB_GEMINI_KEY" \
GOOGLE_GEMINI_BASE_URL='http://127.0.0.1:8080' \
GEMINI_MODEL='gpt-5.5' \
gemini -m gpt-5.5
```

Gemini CLI 使用 `x-goog-api-key` 发送 `GEMINI_API_KEY`，TokenHub 会把它当作项目 Key 验证。不要在这里填写 OpenAI OAuth Access Token。

## 仅对一个项目持久化

如果只希望某个代码项目通过 TokenHub，可以在该项目下创建 `.gemini/.env`：

```dotenv
GEMINI_API_KEY=你的 TokenHub 项目 Key
GOOGLE_GEMINI_BASE_URL=https://tokenhub.example.com
GEMINI_MODEL=gpt-5.5
```

把 `.gemini/.env` 加入该项目的 `.gitignore`，禁止提交项目 Key。该方式不会修改用户级 `~/.gemini/settings.json`，离开项目后也不会影响其他 Gemini CLI 会话。

## 验证

先验证普通对话：

```bash
GEMINI_API_KEY="$TOKENHUB_GEMINI_KEY" \
GOOGLE_GEMINI_BASE_URL='https://tokenhub.example.com' \
gemini -m gpt-5.5 -p 'Reply with exactly TOKENHUB_GEMINI_OK.'
```

再在一个不含敏感数据的测试目录验证客户端工具：

```bash
mkdir -p /tmp/tokenhub-gemini-test
cd /tmp/tokenhub-gemini-test
printf 'TOKENHUB_TOOL_FIXTURE\n' > fixture.txt

GEMINI_API_KEY="$TOKENHUB_GEMINI_KEY" \
GOOGLE_GEMINI_BASE_URL='https://tokenhub.example.com' \
gemini -m gpt-5.5 -p 'Use read_file to read fixture.txt, then return its marker.'
```

管理后台的请求审计应显示：

- 请求模型为 `gpt-5.5`；
- Provider 为 OpenAI Codex Subscription；
- 工具调用前后的请求使用相同 Provider Resource；
- 状态码为 200。

## 已支持的 Gemini 协议能力

- `GET /v1beta/models`
- `GET /v1beta/models/{model}`
- `POST /v1beta/models/{model}:generateContent`
- `POST /v1beta/models/{model}:streamGenerateContent?alt=sse`
- `POST /v1beta/models/{model}:countTokens`
- 文本、内联图片、Gemini CLI 客户端工具、函数结果、多轮工具调用
- Codex reasoning continuation 在 Gemini `thoughtSignature` 中回传
- 同一 Gemini 会话的 Codex 订阅账号亲和性

当前不支持 Gemini 服务端 `googleSearch`、`codeExecution` 和 `cachedContent`。Gemini CLI 在本机执行的 `read_file`、Shell、文件编辑等客户端工具不受影响。

## 常见问题

| 现象 | 检查项 |
| --- | --- |
| `invalid_api_key` | `GEMINI_API_KEY` 必须是 TokenHub 项目 Key，不是 Codex OAuth Token |
| `model_not_allowed` | 项目 Key 的模型白名单必须包含所选 GPT 模型 |
| `provider_unavailable` | 检查模型路由、Codex 账号健康状态和额度 |
| CLI 仍访问 Google | 确认设置了 `GOOGLE_GEMINI_BASE_URL`，且地址末尾没有 `/v1beta` |
| 工具调用失败 | 查看请求审计中的错误；确认使用当前版本 Gemini CLI 和启用工具能力的 GPT 模型 |

Gemini CLI 自定义地址的官方说明见 [Gemini CLI configuration](https://github.com/google-gemini/gemini-cli/blob/main/docs/reference/configuration.md)。
