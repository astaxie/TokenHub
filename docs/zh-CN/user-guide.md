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

## 在演练场测试模型

打开控制台中的「模型演练场」，无需编写 API 脚本即可测试可用的聊天模型。每次响应都会展示流式或缓冲模式、可测量时的 TTFT、输出吞吐、总耗时、完整上下文输入 Tokens、输出 Tokens、估算成本、本地完成时间和 Request ID。展开响应可查看实际响应详情；只有拥有路由读取权限的角色才能看到 Provider 和路由内部信息。

只有所选模型的 `input_modalities` 包含 `image` 时，演练场才会开放图片上传；多模态模型需要在模型目录中配置该字段。演练场支持 JPEG、PNG 和 WebP 图片，每条消息最多上传 4 张、单张最大 5 MiB，当前会话中的图片总大小最多为 12 MiB。导出的会话会保留图片名称、媒体类型和大小等上下文信息，但不会包含图片内容。

会话默认是临时的，只保留在当前页面；需要留档时请使用「导出演练」。点击「停止」会保留部分输出；「重跑」会从该轮生成新候选并移除后续轮次。切换模型默认新建会话，只有显式选择后才会沿用原上下文。上游不支持流式时，页面会使用缓冲模式，并把 TTFT 标为不适用。

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

TokenHub 将推理强度作为尽力应用的提示字段，不改变路由顺序。OpenAI 兼容 Provider 接收原值；原生 Anthropic 路由将支持的值转换为 `output_config.effort`；原生 Gemini 路由根据具体模型的支持矩阵转换为 Gemini 3 及以上模型的 `thinkingLevel`，或 Gemini 2.5 模型的官方 `thinkingBudget`。不支持的值或空值会被省略，继续使用上游模型的默认行为。如果上游返回 `400` 或 `422` 参数错误，并且错误信息明确指向推理强度字段，TokenHub 会在同一路由内移除该字段并重试一次。不属于推理强度拒绝的 `400` / `422` 会直接返回给你，而不会切换到其它 Provider 重试：上游认定格式错误的请求，换任何 Provider 都是错的。每次物理重试均计入 Provider Resource RPM，并记录为一次路由尝试。

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

Chat Completions 路由到 Codex Subscription 账号时，内部使用 Responses 协议，并提供相同的文本、图片、函数工具、并行工具、推理连续性和流式能力。

Codex 订阅上游不接受客户端的采样、输出 Token 上限和停止条件字段。TokenHub 会在兼容端点接收这些字段，但不会将它们转发给订阅上游，因此 Codex 路由无法保证执行 `max_tokens`、`max_completion_tokens`、`temperature`、`top_p` 和停止条件。业务必须严格执行这些控制项时，请使用标准 API Provider。

### 推理连续性

Anthropic 与 Gemini 要求在多轮工具调用的下一轮中，原样回传推理步骤附带的 opaque 签名。OpenAI Chat Completions 规范没有对应字段，因此 TokenHub 通过扩展字段返回：

| 字段 | 对应供应商数据 |
| --- | --- |
| `message.reasoning_content` | Anthropic `thinking` 文本、Gemini thought 片段、Codex 推理摘要 |
| `message.reasoning_signature` | Anthropic `thinking.signature`、Codex 加密推理内容 |
| `message.reasoning_details` | 与工具调用 ID 绑定的 Codex 加密推理内容 |
| `message.redacted_reasoning_content` | Anthropic `redacted_thinking.data` |
| `message.tool_calls[].thought_signature` | Gemini `thoughtSignature` |

在后续请求的 assistant 消息中回传这些字段即可保持推理连续性。对于 `reasoning_details`，必须完整保留条目及其 `id`；只有该 ID 与同一 assistant 消息中的工具调用匹配时，TokenHub 才会接受。忽略这些字段的客户端同样可用：TokenHub 会省略推理块，而不会回传供应商将拒绝的签名。签名带有签发供应商的标记，绝不会被回传给其他供应商。

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

OpenAI 兼容路由可通过 Provider 和 Provider Resource 的 `options` 适配 Claude 推理参数。`reasoning_effort_map` 是类似 `{"minimal":"low","xhigh":"max"}` 的 JSON 对象；`reasoning_effort_values` 是逗号分隔的允许值；`reasoning_effort_unsupported` 可设为 `omit`（默认）、`reject`，或显式启用的 `passthrough`；`reasoning_budget_map` 按最大 Token 数及可选的 `*` 兜底值映射推理等级，例如 `{"2048":"low","8192":"medium","*":"max"}`。Provider Resource 配置覆盖 Provider 配置。TokenHub 将 `thinking.type=disabled` 转为 `none`；`adaptive` 在没有显式 effort 时使用上游默认值；`enabled` 根据 `budget_tokens` 映射。显式 `output_config.effort` 的优先级高于顶层 `effort` 和预算推导值。仅当上游支持在后续 assistant 消息中接收自身的 `reasoning_content` 时，才设置 `preserve_reasoning_content=true`。OpenAI 兼容上游返回的 `reasoning_content` 会按合法顺序转换为 Claude 的 `thinking` / `thinking_delta` 块，并附带 TokenHub 回放签名。

路由到 OpenAI Codex Subscription 账号的模型也使用同一个 Messages 接口：TokenHub 将 Messages 直接转换为 Responses 协议，再把结果转换回 Anthropic 事件。因此 Claude Code 可以直接连接 TokenHub，不需要 CC-Switch 或其他本地协议代理。Codex 签发的推理签名会跨工具调用轮次传递，同一个 Claude Code 会话会保持绑定到同一个健康订阅账号。

在 Codex 路由的 Messages 请求中，由于订阅上游不支持对应请求字段，`max_tokens`、`temperature`、`top_p`、`stop_sequences` 和 Anthropic 结构化输出格式无法被强制执行。

启用 `mid-conversation-system-2026-04-07` 的 Claude Code 请求可以在 `messages` 中包含 `system` 条目。TokenHub 会在原生 Anthropic 路由中保留这些条目，并在 OpenAI 兼容路由中将其转换为保持原顺序的系统消息。未启用该 beta 时，`messages` 仍只接受 `user` 和 `assistant` role。

本地 Claude Code 使用 TokenHub Host URL，不添加 `/v1` 后缀：

```bash
export ANTHROPIC_BASE_URL="http://localhost:8080"
export ANTHROPIC_AUTH_TOKEN="YOUR_TOKENHUB_API_KEY"
export ANTHROPIC_MODEL="CLAUDE_COMPATIBLE_MODEL_ID"

claude
```

`ANTHROPIC_AUTH_TOKEN` 通过 `Authorization: Bearer` 发送 TokenHub Key。没有 Authorization Header 时，也可通过 `ANTHROPIC_API_KEY` 使用 `x-api-key`。Token 估算会检查 Key 和模型权限，但不生成计费推理记录。

## 持久化后台 Responses

在 `POST /v1/responses` 中设置 `background: true`，即可持久化 Responses 请求并立即获得由网关生成的稳定 Response ID：

```bash
curl http://localhost:8080/v1/responses \
  -H "Authorization: Bearer $TOKENHUB_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-5.4","input":"Summarize the report","background":true}'
```

通过 `GET /v1/responses/{id}` 查询状态或最终结果，通过 `POST /v1/responses/{id}/cancel` 请求取消。查询与取消必须使用原始任务对应的项目、API Key、归属用户，并且该 Key 当前仍有模型访问权限；任一条件不匹配均返回 `404`。暂不支持可恢复的后台 SSE，因此同时设置 `background: true` 与 `stream: true` 会被拒绝。

对外状态包括 `queued`、`in_progress`、`completed`、`failed` 和 `cancelled`。Worker 会在上游调用前后应用配额、预算、并发限制、路由、Guardrail、缓存亲和、成本核算、请求日志和链路追踪。取消与完成竞态只有一个持久化结果；如果上游已经产生用量，仍会且只会结算一次。

排队中的任务可跨服务重启继续执行。租约在准入前丢失的任务会安全地重新排队；准入后丢失 Worker 的任务不会盲目重放，而是以 `response_execution_lost` 明确失败，因为上游可能已经收到请求。PostgreSQL 多实例通过带隔离代次的租约和行锁协调领取。SQLite 支持重启恢复，但仍限定为单后端部署，不得让多个后端实例共享同一个 SQLite 文件。

请求信封与结果使用 `TOKENHUB_SECRET_KEY` 静态加密；认证 Header 不会落库，只保留有长度限制的协议 Header 白名单。后台请求与响应正文不会复制到明文请求载荷审计记录或链路追踪导出中，路由尝试记录也会移除上游错误文本。加解密失败时流程会关闭并返回错误，不会回退到明文。终态载荷保留 `TOKENHUB_RESPONSE_RESULT_TTL_SECONDS`，到期后会擦除请求与结果密文，后续查询返回 `404`。返回的 ID 是 TokenHub 查询 ID，不会转换为上游 `previous_response_id`。

启用指标后，Prometheus 会提供 `tokenhub_gateway_response_jobs_queued`、`tokenhub_gateway_response_job_queue_wait_seconds`、`tokenhub_gateway_response_job_execution_seconds`、`tokenhub_gateway_response_jobs_total` 和 `tokenhub_gateway_response_job_recoveries_total`。Worker 并发数、轮询间隔、超时、租约、保留时间与队列上限见[部署文档](deployment.md#后端环境变量)。

## Gemini CLI 使用 Codex 订阅 GPT

Gemini CLI 可以直接连接 TokenHub 的 Gemini 原生 `v1beta` 接口，并使用路由到 OpenAI Codex Subscription 账号的 GPT 模型。将 `GEMINI_API_KEY` 设置为 TokenHub 项目 Key，将 `GOOGLE_GEMINI_BASE_URL` 设置为不含 `/v1beta` 的 TokenHub Host，并选择对应 GPT 模型即可，不需要 CCswitch。隔离启动、项目级配置、支持端点、验证步骤和限制见 [Gemini CLI 通过 TokenHub 使用 Codex 订阅 GPT](gemini-cli-codex-subscription.md)。

## Codex 订阅生图

`POST /v1/images/generations` 接受 OpenAI 兼容的 `model`、`prompt`、`quality`、`size`、`n` 和 `response_format` 字段。请使用对外虚拟模型 `model: "codex-gpt-image-2"` 与 `n: 1`。`gpt-image-2` 通常仍是独立的标准 API 模型；作为一个窄兼容例外，TokenHub 会把带 Codex `originator` 或 `x-codex-image-turn-id` 请求头的生图请求映射为 `codex-gpt-image-2` 并返回 `b64_json`，API Key 必须允许 `codex-gpt-image-2`。添加 `Prefer: respond-async` 可先获得图片任务，再轮询 `GET /v1/image-jobs/{id}`。

`POST /v1/images/edits` 通过 multipart 的 `image` 或 `image[]` 接收参考图。`gpt-image-2` 可把单个 `mask` 转发给 OpenAI API；Codex 订阅账号暂不支持遮罩编辑。TokenHub 不安装或启动 Codex CLI，而是直接请求 Codex 订阅 Images 接口；提示词在数据库中加密保存，输入图与输出图保留在服务器上，下载 URL 签名有效期为 24 小时。URL 过期后文件仍会保留，再次查询任务即可获得新 URL。被选中的 Codex 账号必须具备生图权限。

生图任务默认最多执行 5 分钟，可通过 `TOKENHUB_IMAGE_JOB_TIMEOUT_SECONDS` 调整。

管理员在 **Provider 渠道** 中配置这项能力：打开 OpenAI Codex Provider，在 **模型** 页签勾选 **Codex 订阅生图**。选择一个已启用账号后，TokenHub 会先提示额度消耗，再向该真实账号发送一次低质量 `gpt-image-2` 请求。只有收到非空且有效的图片，系统才会把账号记录为“支持生图”，并创建或重新启用 Provider 线路。返回 `403` 会记录为“不支持生图”；凭据过期时需要重新授权；限流、超时和上游临时故障不会覆盖之前的能力结果，可在弹窗中重试。测试会消耗少量订阅额度，TokenHub 不会在后台自动执行这项测试。

这个勾选项会以幂等方式（重复操作不会创建重复数据）管理一条从 `codex-gpt-image-2` 到 OpenAI Codex Provider、上游模型为 `gpt-image-2` 的启用线路。升级时，系统会为之前已经确认支持生图的启用账号做一次性线路补齐。取消勾选会停用匹配线路，但保留账号能力测试结果；服务启动时不会重新启用管理员明确停用的线路，也不会在迁移标记完成后重新创建被管理员删除的线路，管理员仍可主动重新测试并启用。优先级、权重、项目范围、指定资源和资源分组等高级控制仍可在路由策略中编辑。有启用线路后，已确认支持的账号会被优先选择，返回 `403` 的账号会被临时跳过；经过 `TOKENHUB_IMAGE_CAPABILITY_RETRY_SECONDS`（默认 24 小时）后，该账号可由下一次真实请求低频复测。首次测试失败且没有创建线路时，需要管理员手动重试。只有存在可用线路和账号时，`codex-gpt-image-2` 才会出现在 `GET /v1/models` 中。除上述 Codex 客户端兼容映射外，独立的 `gpt-image-2` 模型使用 OpenAI API Provider，不会消耗 Codex 订阅额度。

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
