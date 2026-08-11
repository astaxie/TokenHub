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

## Test a Model in the Playground

Open **Model Playground** in the console to test an available chat model without creating an API script. Each response shows streaming or buffered delivery, TTFT when it can be measured, output throughput, total duration, full-context input tokens, output tokens, estimated cost, local completion time, and a request ID. Expand the response for the actual response details. Provider and route internals appear only when your role has routing-read permission.

The session is temporary and remains only on the current page unless you choose **Export Playground**. **Stop** keeps partial output. **Rerun** creates another candidate from that turn and removes later turns. Changing models starts a new session unless you explicitly choose to keep the existing context. For an upstream that does not support streaming, the page uses buffered mode and marks TTFT as not applicable.

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

TokenHub treats reasoning effort as a best-effort hint and does not change route ordering. OpenAI-compatible providers receive the value unchanged. Native Anthropic routes convert supported values to `output_config.effort`. Native Gemini routes convert supported values according to the model-specific `thinkingLevel` matrix for Gemini 3 and later models or the documented `thinkingBudget` for Gemini 2.5 models. Unsupported or blank values are omitted so the upstream model default remains in effect. If an upstream provider returns a `400` or `422` parameter error that explicitly identifies the effort field, TokenHub retries the same route once without that field. A `400` or `422` that is not an effort rejection is reported to you rather than retried elsewhere: a request the upstream considers malformed is malformed at every provider. Each physical retry counts toward Provider Resource RPM and appears as a route attempt.

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

Chat Completions routed to a Codex Subscription account use the Responses protocol internally and provide the same text, image, function-tool, parallel-tool, reasoning-continuation, and streaming behavior.

The Codex subscription upstream does not accept client sampling, output-token-limit, or stop-sequence fields. TokenHub accepts those fields at the compatibility endpoint but omits them from the subscription request, so `max_tokens`, `max_completion_tokens`, `temperature`, `top_p`, and stop conditions are not enforced on Codex-backed routes. Use a standard API Provider when those controls are contractual.

### Reasoning continuation

Anthropic and Gemini require the opaque signature attached to a reasoning step to be echoed back verbatim on the next turn of a multi-step tool exchange. The OpenAI Chat Completions schema has no field for it, so TokenHub returns the data in extension fields:

| Field | Provider data |
| --- | --- |
| `message.reasoning_content` | Anthropic `thinking` text, Gemini thought parts, Codex reasoning summaries |
| `message.reasoning_signature` | Anthropic `thinking.signature`, Codex encrypted reasoning |
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

For OpenAI-compatible routes, Provider and Provider Resource `options` can adapt Claude reasoning controls to the upstream vocabulary. `reasoning_effort_map` is a JSON object such as `{"minimal":"low","xhigh":"max"}`; `reasoning_effort_values` is a comma-separated allowlist; `reasoning_effort_unsupported` is `omit` (default), `reject`, or the explicitly opted-in `passthrough`; and `reasoning_budget_map` maps maximum token counts plus optional `*` fallback to effort values, for example `{"2048":"low","8192":"medium","*":"max"}`. Resource options override Provider options. TokenHub translates `thinking.type=disabled` to `none`, leaves `adaptive` at the upstream default unless an explicit effort exists, and maps `enabled` with `budget_tokens`. An explicit `output_config.effort` overrides top-level `effort` and the budget-derived value. Set `preserve_reasoning_content=true` only when the upstream accepts its own `reasoning_content` on later assistant messages. OpenAI-compatible `reasoning_content` is returned to Claude clients as ordered `thinking` / `thinking_delta` blocks with a TokenHub replay signature.

Models routed to an OpenAI Codex Subscription account work through the same Messages endpoint: TokenHub translates Messages directly to the Responses protocol and converts the result back to Anthropic events. Claude Code therefore connects to TokenHub directly; CC-Switch or another local protocol proxy is not required. Codex-issued reasoning signatures are carried across tool turns, and a Claude Code session remains affined to one healthy subscription account.

On Codex-backed Messages routes, `max_tokens`, `temperature`, `top_p`, `stop_sequences`, and Anthropic structured-output formatting cannot be enforced because the subscription upstream does not support their equivalent request fields.

Claude Code requests that enable `mid-conversation-system-2026-04-07` may include `system` entries inside `messages`. TokenHub preserves those entries on native Anthropic routes and translates them into ordered system messages on OpenAI-compatible routes. Without that beta, `messages` continues to accept only `user` and `assistant` roles.

Configure local Claude Code with the TokenHub host URL, without the `/v1` suffix:

```bash
export ANTHROPIC_BASE_URL="http://localhost:8080"
export ANTHROPIC_AUTH_TOKEN="YOUR_TOKENHUB_API_KEY"
export ANTHROPIC_MODEL="CLAUDE_COMPATIBLE_MODEL_ID"

claude
```

`ANTHROPIC_AUTH_TOKEN` sends the TokenHub key in `Authorization: Bearer`. `ANTHROPIC_API_KEY` also works through `x-api-key` when no Authorization header is present. Token counting verifies key and model access but does not create a billed inference record.

## Gemini CLI with Codex subscription GPT

Gemini CLI can connect directly to TokenHub's native Gemini `v1beta` surface and use a GPT model routed to an OpenAI Codex Subscription account. Set `GEMINI_API_KEY` to a TokenHub project key, `GOOGLE_GEMINI_BASE_URL` to the TokenHub host without `/v1beta`, and select the routed GPT model. CCswitch is not required. See [Use Codex Subscription GPT from Gemini CLI](gemini-cli-codex-subscription.md) for isolated and project-local configuration, supported endpoints, verification, and limitations.

## Codex subscription image generation

`POST /v1/images/generations` accepts the OpenAI-compatible `model`, `prompt`, `quality`, `size`, `n`, and `response_format` fields. Use the public virtual model `model: "codex-gpt-image-2"` and `n: 1`. `gpt-image-2` normally remains a separate standard API model. As a narrow compatibility exception, TokenHub maps a generation request marked by a Codex `originator` or `x-codex-image-turn-id` header to `codex-gpt-image-2` and returns `b64_json`; the API key must allow `codex-gpt-image-2`. Add `Prefer: respond-async` to receive an image job, then poll `GET /v1/image-jobs/{id}`.

`POST /v1/images/edits` accepts multipart reference images in `image` or `image[]`. `gpt-image-2` forwards one `mask` to the OpenAI API; mask edits are not available through Codex subscription accounts. TokenHub sends image requests directly to the Codex subscription Images endpoint without installing or starting Codex CLI, keeps prompts encrypted in the database, retains input and output images on the server, and returns signed download URLs valid for 24 hours. The files remain stored after a URL expires; polling the job creates a new URL. The selected Codex account must have image-generation entitlement.

Image jobs have a five-minute default execution timeout controlled by `TOKENHUB_IMAGE_JOB_TIMEOUT_SECONDS`.

TokenHub records image-generation capability from real account results. Accounts confirmed as supported are preferred, accounts returning `403` are temporarily skipped, and accounts that have not been checked remain eligible for first-use detection. After `TOKENHUB_IMAGE_CAPABILITY_RETRY_SECONDS` (24 hours by default), an unsupported account becomes discoverable and routable again so the next real request can retry it. TokenHub does not generate a background image merely to probe recovery.

`codex-gpt-image-2` appears in `GET /v1/models` when a healthy connected Codex account is confirmed as supported or has reached its low-frequency retry window. It is a subscription-backed virtual model and does not require a conventional Provider model route. Except for the Codex-client compatibility mapping above, the separate `gpt-image-2` catalog model uses an OpenAI API provider and does not consume Codex subscription quota.

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
