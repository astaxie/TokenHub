# Use Codex Subscription GPT from Gemini CLI

TokenHub exposes a native Gemini `v1beta` compatibility surface. The official Gemini CLI can connect directly to TokenHub, which then routes the request to an OpenAI Codex Subscription account. CCswitch and other local protocol proxies are not required.

## Prerequisites

- A healthy OpenAI Codex Subscription account is configured in TokenHub.
- A GPT model such as `gpt-5.5` is enabled and routed to that provider.
- The TokenHub project key allows that model.
- The installed Gemini CLI supports `GOOGLE_GEMINI_BASE_URL`.

Use HTTPS except for `localhost`, `127.0.0.1`, or `[::1]`. Do not append `/v1beta` to the base URL.

## Start without changing existing settings

The following environment variables apply only to this command and do not edit `~/.gemini/settings.json`:

```bash
export TOKENHUB_GEMINI_KEY='your TokenHub project key'

GEMINI_API_KEY="$TOKENHUB_GEMINI_KEY" \
GOOGLE_GEMINI_BASE_URL='https://tokenhub.example.com' \
GEMINI_MODEL='gpt-5.5' \
gemini -m gpt-5.5

unset TOKENHUB_GEMINI_KEY
```

For a local TokenHub instance:

```bash
GEMINI_API_KEY="$TOKENHUB_GEMINI_KEY" \
GOOGLE_GEMINI_BASE_URL='http://127.0.0.1:8080' \
GEMINI_MODEL='gpt-5.5' \
gemini -m gpt-5.5
```

Gemini CLI sends `GEMINI_API_KEY` in `x-goog-api-key`; use a TokenHub project key, not an OpenAI OAuth access token.

## Persist configuration for one project

Create `.gemini/.env` inside that project:

```dotenv
GEMINI_API_KEY=your TokenHub project key
GOOGLE_GEMINI_BASE_URL=https://tokenhub.example.com
GEMINI_MODEL=gpt-5.5
```

Add `.gemini/.env` to that project's `.gitignore`. This leaves the user-level Gemini configuration unchanged.

## Supported surface

- `GET /v1beta/models` and `GET /v1beta/models/{model}`
- `generateContent`, SSE `streamGenerateContent`, and `countTokens`
- Text, inline images, client-side tools, function results, and multi-turn tool calls
- Codex reasoning continuation carried in Gemini `thoughtSignature`
- Codex subscription account affinity across one Gemini conversation

Gemini server-side `googleSearch`, `codeExecution`, and `cachedContent` are not supported. Local Gemini CLI tools such as file reads, shell commands, and edits remain available.

See the official [Gemini CLI configuration](https://github.com/google-gemini/gemini-cli/blob/main/docs/reference/configuration.md) reference for `GOOGLE_GEMINI_BASE_URL`.
