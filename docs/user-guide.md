# User LLM API Guide

Language: English | [简体中文](zh-CN/user-guide.md) | [日本語](ja/user-guide.md)

This guide is for employees and application developers who call approved large language models through TokenHub.

## What You Need

| Item | Purpose |
| --- | --- |
| Base URL | OpenAI-compatible endpoint root, for example `http://localhost:8080/v1` |
| Project API Key | Sent as `Authorization: Bearer YOUR_TOKENHUB_API_KEY` |
| Model ID | Returned by `GET /v1/models` and used as the `model` field |
| Request ID | Used in Request Logs when troubleshooting failures |

Console login tokens cannot call model APIs. Use a project API key from **Key Management**.

## Call Sequence

1. Open **Key Management** and create or copy an API key. New keys are shown only once.
2. TokenHub automatically attributes a personal key to an assigned project, or to the platform default project when none is assigned.
3. Call `GET /v1/models` to see the model list available to that key.
4. Use one model ID in `POST /v1/chat/completions`, `POST /v1/responses`, or `POST /v1/embeddings`.
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
| `description` | Model description |
| `context_size` | Maximum context window |

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
