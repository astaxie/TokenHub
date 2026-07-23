# 普通用户大模型 API 指南

Language: [English](../user-guide.md) | 简体中文 | [日本語](../ja/user-guide.md)

本指南面向通过 TokenHub 调用企业已批准大语言模型的员工和应用开发者。

## 你需要什么

| 项目 | 用途 |
| --- | --- |
| Base URL | OpenAI 兼容接口根地址，例如 `http://localhost:8080/v1` |
| 项目 API Key | 通过 `Authorization: Bearer YOUR_TOKENHUB_API_KEY` 发送 |
| 模型 ID | 由 `GET /v1/models` 返回，并填写到 `model` 字段 |
| request_id | 调用失败时用于在请求日志中排查 |

控制台登录 Token 不能调用模型 API。请在 **Key 管理** 中使用项目 API Key。

## 调用顺序

1. 打开 **Key 管理**，创建或复制 API Key。新 Key 只展示一次。
2. TokenHub 会自动把个人 Key 归入已分配项目；尚未分配项目时归入平台默认项目。
3. 调用 `GET /v1/models` 查看这个 Key 可用的模型列表。
4. 选择一个模型 ID，调用 `POST /v1/chat/completions`、`POST /v1/responses` 或 `POST /v1/embeddings`。
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
| `description` | 模型描述 |
| `context_size` | 模型最大上下文长度 |

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
