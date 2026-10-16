# TokenHub AI SDK Smoke Test

This directory contains a small Vercel AI SDK test client for TokenHub's OpenAI-Compatible gateway.

## Setup

```bash
cd sdk
npm install
cp .env.example .env
```

Edit `.env` and set:

```bash
TOKENHUB_API_KEY=thk_xxx
TOKENHUB_MODEL=deepseek-chat
```

Then run:

```bash
npm run test:deepseek
```

To verify the Anthropic Messages and Claude Code gateway:

```bash
TOKENHUB_ANTHROPIC_MODEL=claude-compatible-model \
  npm run test:anthropic-messages
```

To verify the Admin API path for security policies:

```bash
npm run test:security-policy
```

To verify audio transcription and document reranking, publish suitable models and
allow both names on the project API key, then run:

```bash
TOKENHUB_TRANSCRIPTION_MODEL=gpt-4o-transcribe npm run test:transcriptions
TOKENHUB_RERANK_MODEL=public-reranker npm run test:rerank
```

These clients use Node's standard HTTP/FormData APIs (Node 20 or later) and the
documented gateway contracts. Transcription uploads a synthetic one-second silent
WAV and checks the JSON response, authentication and invalid uploads. Reranking
checks string, ID-bearing and mixed documents, Top-N, ordering, returned documents,
and invalid requests. They do not print submitted content or provider error bodies.
Successful calls against a real provider may incur its normal charges.

The backend also runs these exact clients against an isolated in-process TokenHub
server and fake OpenAI/compatible/local upstreams, without real credentials:

```bash
cd ../backend
go test ./internal/server -run '^TestAudioAndRerankSDKSmoke$' -count=1
```

This test requires `node` on PATH; it skips explicitly when Node is unavailable
locally and fails in CI instead of silently skipping the clients.
The model environment variables above configure clients only; they are not server
or deployment settings.

## TokenHub Prerequisites

Before running the test, make sure TokenHub has:

- A Provider configured for DeepSeek or another OpenAI-compatible upstream.
- A route from unified model `deepseek-chat` to a JieKou upstream model such as `deepseek/deepseek-v3-0324`.
- An internal API Key whose allowed models include `deepseek-chat`.

The script calls `GET /v1/models` first, then sends a `generateText` request through AI SDK.

The Anthropic Messages script checks model discovery, token counting, and an
Anthropic SSE request with a client tool. `TOKENHUB_ANTHROPIC_BASE_URL` is the
TokenHub host URL without the `/v1` suffix.

The security policy script calls the Admin API with `TOKENHUB_ADMIN_TOKEN`, then creates or updates the policy identified by `TOKENHUB_SECURITY_POLICY_ID`. It is idempotent, so you can run it repeatedly without creating duplicate policies.

For JieKou, keep TokenHub's external/unified model as `deepseek-chat` if you want, but set the route's upstream `上游模型` to the exact JieKou model ID. Their docs show model IDs like `deepseek/deepseek-r1-0528`; `deepseek/deepseek-v3-0324` is a cheaper chat option.

## Troubleshooting

If `GET /v1/models` succeeds but `generateText` returns `provider_model_not_found` or `provider_invalid_request`, TokenHub has accepted the internal API Key and selected a route, but the upstream Provider rejected the route's `provider_model`.

Fix it in TokenHub Admin:

- Open `路由策略`.
- Find the row whose `统一模型` matches `TOKENHUB_MODEL`.
- Set `上游模型` to the exact model/deployment name accepted by that Provider.

For OpenAI-compatible providers, TokenHub sends the route's `上游模型` as the request body's `model`.
