# 普通用户大模型 API 指南

Language: [English](../user-guide.md) | 简体中文 | [日本語](../ja/user-guide.md)

本指南面向通过 TokenHub 调用企业已批准大语言模型的员工和应用开发者。

## 所需信息

| 项目 | 用途 |
| --- | --- |
| Base URL | OpenAI 兼容根地址为 `http://localhost:8080/v1`；Claude Code Host URL 为 `http://localhost:8080` |
| 项目 API Key | 通过 `Authorization: Bearer YOUR_TOKENHUB_API_KEY` 发送 |
| 模型 ID | 由 `GET /v1/models` 返回，并填写到 `model` 字段 |
| request_id | 调用失败时用于在请求日志中排查 |

控制台登录 Token 不能调用模型 API。请在 **Key 管理** 中使用项目 API Key。

## 调用顺序

1. 打开 **Key 管理**，创建或复制 API Key。新 Key 只展示一次。
2. TokenHub 会自动把个人 Key 归入已分配项目；尚未分配项目时归入平台默认项目。
3. 调用 `GET /v1/models` 查看这个 Key 可用的模型列表。
4. 选择一个模型 ID，调用 `POST /v1/chat/completions`、`POST /v1/messages`、`POST /v1/responses` 或 `POST /v1/embeddings`。
5. 在 **用量统计** 和 **请求日志** 中查看请求、Token、成本和错误。

## 获取模型列表

```bash
curl --request GET \
  --url "http://localhost:8080/v1/models" \
  --header "Authorization: Bearer YOUR_TOKENHUB_API_KEY" \
  --header "Content-Type: application/json"
```

常见模型字段：

| 字段 | 含义 |
| --- | --- |
| `id` | 后续 API 调用使用的模型标识符 |
| `object` | 对象类型，通常为 `model` |
| `created` | 模型创建 Unix 时间戳 |
| `input_token_price_per_m` | 兼容 jiekou 的每百万输入 tokens 整数价格 |
| `output_token_price_per_m` | 兼容 jiekou 的每百万输出 tokens 整数价格 |
| `title` | 模型标题 |
| `display_name` | Anthropic 兼容的模型显示名称 |
| `description` | 模型描述 |
| `context_size` | 模型最大上下文长度 |
| `created_at` | Anthropic 兼容的 RFC 3339 创建时间 |
| `max_input_tokens` | Anthropic 兼容的最大输入上下文 |
| `max_tokens` | 已配置的最大输出 Token 数；未配置时为 `0` |

## 获取指定模型信息

```bash
curl --request GET \
  --url "http://localhost:8080/v1/models/gpt-4.1-mini" \
  --header "Authorization: Bearer YOUR_TOKENHUB_API_KEY" \
  --header "Content-Type: application/json"
```

该接口返回单个模型对象，字段与 `GET /v1/models` 中的模型项一致。

## 创建聊天对话

```bash
curl --request POST \
  --url "http://localhost:8080/v1/chat/completions" \
  --header "Authorization: Bearer YOUR_TOKENHUB_API_KEY" \
  --header "Content-Type: application/json" \
  --data '{
    "model": "gpt-4.1-mini",
    "messages": [
      {"role": "system", "content": "You are an internal enterprise AI assistant."},
      {"role": "user", "content": "Summarize today'\''s support tickets."}
    ],
    "temperature": 0.7,
    "stream": false
  }'
```

常见请求字段：

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `model` | 是 | 必须来自 `GET /v1/models` |
| `messages` | 是 | `system`、`user`、`assistant` 消息数组 |
| `max_tokens` | 否 | 最大生成 tokens |
| `temperature` | 否 | 采样温度 |
| `reasoning_effort` | 否 | 支持该参数的模型和路由所使用的推理强度 |
| `stream` | 否 | `true` 时返回 SSE 流 |
| `tools` | 否 | 上游模型支持时可传函数工具 |
| `response_format` | 否 | 上游模型支持时可传 JSON object 或 JSON schema |

### 推理强度

Chat Completions 接受 OpenAI 兼容的 `reasoning_effort` 字段：

```json
{
  "model": "REASONING_MODEL_ID",
  "messages": [{"role": "user", "content": "Analyze the trade-offs."}],
  "reasoning_effort": "high"
}
```

Responses 接受 OpenAI 兼容的嵌套格式：

```json
{
  "model": "REASONING_MODEL_ID",
  "input": "Analyze the trade-offs.",
  "reasoning": {"effort": "high"}
}
```

TokenHub 将推理强度作为尽力应用的提示字段，不改变路由顺序。OpenAI 兼容 Provider 接收原值；原生 Anthropic 路由将支持的值转换为 `output_config.effort`；原生 Gemini 路由根据具体模型的支持矩阵转换为 Gemini 3 及以上模型的 `thinkingLevel`，或 Gemini 2.5 模型的官方 `thinkingBudget`。不支持的值或空值会被省略，继续使用上游模型的默认行为。如果上游返回 `400` 或 `422` 参数错误，并且错误信息明确指向推理强度字段，TokenHub 会在同一路由内移除该字段并重试一次，之后再按原有故障转移逻辑处理。每次物理重试均计入 Provider Resource RPM，并记录为一次路由尝试。

Responses 推理强度支持 OpenAI 兼容、Anthropic 和 Gemini 路由。Azure OpenAI Responses 与 Responses 流式响应尚未实现，此类请求返回 `501 provider_capability_not_supported`。

## Anthropic 与 Gemini 路由的工具调用与多模态内容

Chat Completions 请求路由到原生 Anthropic 或 Gemini 供应商时，会完整转换请求与响应，而不仅是纯文本。

| 能力 | Anthropic | Gemini |
| --- | --- | --- |
| `tools` 与 `tool_choice` | 支持 | 支持 |
| Assistant `tool_calls` 与 `role: "tool"` 结果 | 支持 | 支持 |
| `parallel_tool_calls: false` | 支持 | `501 provider_capability_not_supported` |
| 图片内容块 | `http(s)` URL 与 base64 data URI | 仅 base64 data URI |
| 流式 | 增量转发 | 增量转发 |

流式按上游事件到达顺序逐条转发，因此首字延迟取决于供应商而非完整响应。这些路由无法表达的内容类型（例如音频块）返回 `400 unsupported_content_block`，不会被静默丢弃。

### 推理连续性

Anthropic 与 Gemini 要求在多轮工具调用的下一轮中，原样回传推理步骤附带的 opaque 签名。OpenAI Chat Completions 规范没有对应字段，因此 TokenHub 通过扩展字段返回：

| 字段 | 对应供应商数据 |
| --- | --- |
| `message.reasoning_content` | Anthropic `thinking` 文本、Gemini thought 片段 |
| `message.reasoning_signature` | Anthropic `thinking.signature` |
| `message.redacted_reasoning_content` | Anthropic `redacted_thinking.data` |
| `message.tool_calls[].thought_signature` | Gemini `thoughtSignature` |

在后续请求的 assistant 消息中回传这些字段即可保持推理连续性。忽略这些字段的客户端同样可用：TokenHub 会省略推理块，而不会回传供应商将拒绝的签名。签名带有签发供应商的标记，绝不会被回传给其他供应商。

## Anthropic Messages 与 Claude Code

TokenHub 提供 `POST /v1/messages` 和 `POST /v1/messages/count_tokens`，供 Claude Code 与 Anthropic 兼容客户端调用。项目 Key 通过 Bearer Token 发送：

```bash
curl --request POST \
  --url "http://localhost:8080/v1/messages" \
  --header "Authorization: Bearer YOUR_TOKENHUB_API_KEY" \
  --header "anthropic-version: 2023-06-01" \
  --header "Content-Type: application/json" \
  --data '{
    "model": "CLAUDE_COMPATIBLE_MODEL_ID",
    "max_tokens": 2048,
    "messages": [
      {"role": "user", "content": "Understand this repository and summarize its architecture."}
    ]
  }'
```

原生 Anthropic 路由保留 Anthropic 内容块与 beta Header。OpenAI 兼容路由转换文本、图片、客户端工具、工具结果、并行工具调用和流式事件。Anthropic 服务端工具无法转换到 OpenAI 兼容 Provider 时，接口返回 `400 unsupported_tool`。

启用 `mid-conversation-system-2026-04-07` 的 Claude Code 请求可以在 `messages` 中包含 `system` 条目。TokenHub 会在原生 Anthropic 路由中保留这些条目，并在 OpenAI 兼容路由中将其转换为保持原顺序的系统消息。未启用该 beta 时，`messages` 仍只接受 `user` 和 `assistant` role。

本地 Claude Code 使用 TokenHub Host URL，不添加 `/v1` 后缀：

```bash
export ANTHROPIC_BASE_URL="http://localhost:8080"
export ANTHROPIC_AUTH_TOKEN="YOUR_TOKENHUB_API_KEY"
export ANTHROPIC_MODEL="CLAUDE_COMPATIBLE_MODEL_ID"

claude
```

`ANTHROPIC_AUTH_TOKEN` 通过 `Authorization: Bearer` 发送 TokenHub Key。没有 Authorization Header 时，也可通过 `ANTHROPIC_API_KEY` 使用 `x-api-key`。Token 估算会检查 Key 和模型权限，但不生成计费推理记录。

## Codex 订阅生图

`POST /v1/images/generations` 接受 OpenAI 兼容的 `model`、`prompt`、`quality`、`size`、`n` 和 `response_format` 字段。请使用对外虚拟模型 `model: "codex-gpt-image-2"` 与 `n: 1`。`gpt-image-2` 是独立的标准 API 模型，绝不会路由到 Codex 订阅能力。添加 `Prefer: respond-async` 可先获得图片任务，再轮询 `GET /v1/image-jobs/{id}`。

`POST /v1/images/edits` 通过 multipart 的 `image` 或 `image[]` 接收参考图。`gpt-image-2` 可把单个 `mask` 转发给 OpenAI API；Codex 订阅账号暂不支持遮罩编辑。TokenHub 不安装或启动 Codex CLI，而是直接请求 Codex 订阅 Images 接口；提示词在数据库中加密保存，输入图与输出图保留在服务器上，下载 URL 签名有效期为 24 小时。URL 过期后文件仍会保留，再次查询任务即可获得新 URL。被选中的 Codex 账号必须具备生图权限。

生图任务默认最多执行 5 分钟，可通过 `TOKENHUB_IMAGE_JOB_TIMEOUT_SECONDS` 调整。

TokenHub 根据账号的真实调用结果记录生图能力。已确认支持的账号会被优先选择；返回 `403` 的账号会被临时跳过；尚未检测的账号仍可在首次使用时完成检测。经过 `TOKENHUB_IMAGE_CAPABILITY_RETRY_SECONDS`（默认 24 小时）后，不支持的账号会重新进入模型发现和路由范围，由下一次真实请求低频复测。TokenHub 不会为了探测恢复而在后台自动生成图片。

至少一个健康的 Codex 接入账号已确认支持生图或进入低频复测窗口时，`codex-gpt-image-2` 会出现在 `GET /v1/models` 中。它是订阅制虚拟模型，不需要配置普通 Provider 模型路由。独立的 `gpt-image-2` 模型使用 OpenAI API Provider，绝不会消耗 Codex 订阅额度。

完整的 curl、异步轮询、参考图、Node.js 和 Python 测试流程见 [Codex 生图 API 调用与测试指南](codex-image-generation-api.md)。

## SDK 配置

```ts
import OpenAI from "openai";

const client = new OpenAI({
  apiKey: process.env.TOKENHUB_API_KEY,
  baseURL: "http://localhost:8080/v1",
});
```

## 错误排查

| 状态 | 常见原因 | 处理方式 |
| --- | --- | --- |
| 401 | API Key 缺失、格式错误、已停用或已过期 | 检查 `Authorization` 和 Key 状态 |
| 403 | 项目、Key 或模型权限不允许当前请求 | 联系团队负责人检查项目成员和模型权限 |
| 404/503 | 该模型没有可用健康路由 | 请管理员检查路由和 Provider 健康状态 |
| 429 | 额度、并发或 Provider 资源限制触发 | 等待恢复或申请提升额度 |
| 500 | 上游 Provider 或路由错误 | 在请求日志中搜索 `request_id` |

## 截图

![Gateway documentation](../assets/screenshots/gateway-en.png)
