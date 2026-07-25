# User LLM API Guide

Language: English | [简体中文](zh-CN/user-guide.md) | [日本語](ja/user-guide.md)

This guide is for employees and application developers who call approved large language models through TokenHub.

## What You Need

| Item | Purpose |
| --- | --- |
| Base URL | OpenAI-compatible root `http://localhost:8080/v1`; Claude Code host `http://localhost:8080` |
| Project API Key | Sent as `Authorization: Bearer YOUR_TOKENHUB_API_KEY` |
| Model ID | Returned by `GET /v1/models` and used as the `model` field |
| Request ID | Used in Request Logs when troubleshooting failures |

Console login tokens cannot call model APIs. Use a project API key from **Key Management**.

## Call Sequence

1. Open **Key Management** and create or copy an API key. New keys are shown only once.
2. TokenHub automatically attributes a personal key to an assigned project, or to the platform default project when none is assigned.
3. Call `GET /v1/models` to see the model list available to that key.
4. Use one model ID in `POST /v1/chat/completions`, `POST /v1/messages`, `POST /v1/responses`, or `POST /v1/embeddings`.
5. Review **Usage Analytics** and **Request Logs** for requests, tokens, cost, and errors.

## List Models

```bash
curl --request GET \
  --url "http://localhost:8080/v1/models" \
  --header "Authorization: Bearer YOUR_TOKENHUB_API_KEY" \
  --header "Content-Type: application/json"
```

Typical model fields:

| Field | Meaning |
| --- | --- |
| `id` | Model identifier used in API calls |
| `object` | Object type, usually `model` |
| `created` | Model creation Unix timestamp |
| `input_token_price_per_m` | JieKou-compatible integer input price per million tokens |
| `output_token_price_per_m` | JieKou-compatible integer output price per million tokens |
| `title` | Model title |
| `display_name` | Anthropic-compatible display name |
| `description` | Model description |
| `context_size` | Maximum context window |
| `created_at` | Anthropic-compatible RFC 3339 creation timestamp |
| `max_input_tokens` | Anthropic-compatible maximum input context |
| `max_tokens` | Configured maximum output tokens, or `0` when unspecified |

## Retrieve Model

```bash
curl --request GET \
  --url "http://localhost:8080/v1/models/gpt-4.1-mini" \
  --header "Authorization: Bearer YOUR_TOKENHUB_API_KEY" \
  --header "Content-Type: application/json"
```

This returns one model object using the same fields as `GET /v1/models`.

## Chat Completions

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

Common request fields:

| Field | Required | Notes |
| --- | --- | --- |
| `model` | Yes | Must be available in `GET /v1/models` |
| `messages` | Yes | `system`, `user`, and `assistant` message list |
| `max_tokens` | No | Maximum generated tokens |
| `temperature` | No | Sampling temperature |
| `reasoning_effort` | No | Reasoning effort for models and routes that support it |
| `stream` | No | `true` returns Server-Sent Events |
| `tools` | No | Function tools when supported by the upstream model |
| `response_format` | No | JSON object or JSON schema when supported |

### Reasoning effort

Chat Completions accepts the OpenAI-compatible `reasoning_effort` field:

```json
{
  "model": "REASONING_MODEL_ID",
  "messages": [{"role": "user", "content": "Analyze the trade-offs."}],
  "reasoning_effort": "high"
}
```

Responses accepts the nested OpenAI-compatible form:

```json
{
  "model": "REASONING_MODEL_ID",
  "input": "Analyze the trade-offs.",
  "reasoning": {"effort": "high"}
}
```

TokenHub treats reasoning effort as a best-effort hint and does not change route ordering. OpenAI-compatible providers receive the value unchanged. Native Anthropic routes convert supported values to `output_config.effort`. Native Gemini routes convert supported values according to the model-specific `thinkingLevel` matrix for Gemini 3 and later models or the documented `thinkingBudget` for Gemini 2.5 models. Unsupported or blank values are omitted so the upstream model default remains in effect. If an upstream provider returns a `400` or `422` parameter error that explicitly identifies the effort field, TokenHub retries the same route once without that field before applying the existing failover behavior. Each physical retry counts toward Provider Resource RPM and appears as a route attempt.

Responses reasoning effort is supported on OpenAI-compatible, Anthropic, and Gemini routes. Azure OpenAI Responses and streaming Responses are not implemented; those requests return `501 provider_capability_not_supported`.

## Tool calling and multimodal content on Anthropic and Gemini routes

Chat Completions requests routed to a native Anthropic or Gemini provider translate the whole request and response, not only plain text.

| Capability | Anthropic | Gemini |
| --- | --- | --- |
| `tools` and `tool_choice` | Supported | Supported |
| Assistant `tool_calls` and `role: "tool"` results | Supported | Supported |
| `parallel_tool_calls: false` | Supported | `501 provider_capability_not_supported` |
| Image content parts | `http(s)` URLs and base64 data URIs | Base64 data URIs only |
| Streaming | Incremental relay | Incremental relay |

Streaming forwards upstream events as they arrive, so time to first token reflects the provider rather than the full response. Content types these routes cannot represent, such as audio parts, return `400 unsupported_content_block` instead of being dropped from the request.

### Reasoning continuation

Anthropic and Gemini require the opaque signature attached to a reasoning step to be echoed back verbatim on the next turn of a multi-step tool exchange. The OpenAI Chat Completions schema has no field for it, so TokenHub returns the data in extension fields:

| Field | Provider data |
| --- | --- |
| `message.reasoning_content` | Anthropic `thinking` text, Gemini thought parts |
| `message.reasoning_signature` | Anthropic `thinking.signature` |
| `message.redacted_reasoning_content` | Anthropic `redacted_thinking.data` |
| `message.tool_calls[].thought_signature` | Gemini `thoughtSignature` |

Echo these fields on the assistant message of the following request to preserve reasoning continuity. Clients that ignore them still work: TokenHub omits the reasoning block rather than replaying a signature the provider would reject. Signatures are tagged with the provider that issued them and are never replayed to a different provider.

## Anthropic Messages and Claude Code

TokenHub exposes `POST /v1/messages` and `POST /v1/messages/count_tokens` for Claude Code and Anthropic-compatible clients. Use a project key as a bearer token:

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

Native Anthropic routes preserve Anthropic content blocks and beta headers. OpenAI-compatible routes translate text, images, client tools, tool results, parallel tool calls, and streaming events. Anthropic server tools that cannot be represented by an OpenAI-compatible provider return `400 unsupported_tool`.

Configure local Claude Code with the TokenHub host URL, without the `/v1` suffix:

```bash
export ANTHROPIC_BASE_URL="http://localhost:8080"
export ANTHROPIC_AUTH_TOKEN="YOUR_TOKENHUB_API_KEY"
export ANTHROPIC_MODEL="CLAUDE_COMPATIBLE_MODEL_ID"

claude
```

`ANTHROPIC_AUTH_TOKEN` sends the TokenHub key in `Authorization: Bearer`. `ANTHROPIC_API_KEY` also works through `x-api-key` when no Authorization header is present. Token counting verifies key and model access but does not create a billed inference record.

## SDK Setup

```ts
import OpenAI from "openai";

const client = new OpenAI({
  apiKey: process.env.TOKENHUB_API_KEY,
  baseURL: "http://localhost:8080/v1",
});
```

## Troubleshooting

| Status | Likely cause | What to do |
| --- | --- | --- |
| 401 | Missing, malformed, disabled, or expired API key | Check `Authorization` and key status |
| 403 | Project, key, or model permission does not allow the request | Ask your team leader to check project membership and model access |
| 404/503 | No enabled healthy route can serve the model | Ask an administrator to check routes and provider health |
| 429 | Quota, concurrency, or provider resource limit reached | Wait for reset or request a quota increase |
| 500 | Upstream provider or routing error | Search `request_id` in Request Logs |

## Screenshot

![Gateway documentation](assets/screenshots/gateway-en.png)
