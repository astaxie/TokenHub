# Use Super Grok Subscription from TokenHub

Language: English | [简体中文](zh-CN/super-grok-subscription.md) | [日本語](ja/super-grok-subscription.md)

TokenHub can attach a Super Grok / Grok CLI account as a subscription Provider. Clients keep using TokenHub project keys and the OpenAI-compatible `/v1` surface. CCswitch and CLIProxyAPI are not required.

This path uses the unofficial Grok CLI chat proxy. Use only accounts your organization owns, and treat it as an internal capability rather than a resale product.

## Prerequisites

- A Super Grok (or Grok CLI) account that can complete xAI device-code login.
- An administrator who can create Providers and routes.
- Outbound HTTPS to `auth.x.ai` and `cli-chat-proxy.grok.com`.

## Connect an account

1. Open **Provider Channels** and create a Provider.
2. Choose **Account pool**, then select the **Super Grok** channel.
3. Confirm the chat Base URL `https://cli-chat-proxy.grok.com/v1`.
4. Start authorization. TokenHub shows a user code and opens the xAI device page.
5. Approve the device. TokenHub stores the refresh token encrypted and can renew access tokens automatically.
6. Import subscription models such as `grok-4.5`, `grok-4.6`, `grok-composer-2.5-fast`, or `grok-build-0.1`, then create routes.

Do not paste an xAI API key into this Provider. Official `api.x.ai` keys belong on an `openai_compatible` channel.

## Call TokenHub from a client

After routes exist, callers use a **project API key** from **Key Management**, not the Super Grok OAuth tokens stored on the Provider. The console login token cannot call `/v1`.

**Key Management** can download a Codex CLI `config.toml` and a `.env` template. It does not download a Grok CLI home. Configure Grok CLI by hand as shown below.

### OpenAI-compatible clients

Set the gateway root so it ends with `/v1`, then use the project key as a bearer token. Replace the host with the real TokenHub URL from the deployment.

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

The `model` value must appear in `GET /v1/models` for that key. `POST /v1/responses` uses the same base URL and bearer token.

### Grok CLI (isolated home)

Do not write TokenHub settings into `~/.grok`. That directory holds the official Grok login and Super Grok session. The `GROK_CONFIG` / `GROK_CONFIG_PATH` overlay cannot change the inference base URL.

Use a separate home directory so the default `grok` command stays on xAI:

```bash
GROK_HOME="${HOME}/.grok-tokenhub"
mkdir -p "$GROK_HOME"
chmod 700 "$GROK_HOME"
```

Write `$GROK_HOME/config.toml`:

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

Replace `http://localhost:8080/v1` with the same gateway root used above, and `grok-4.5` with a model the key is allowed to call. Then, in the same shell:

```bash
export GROK_HOME="${HOME}/.grok-tokenhub"
export GROK_MODELS_BASE_URL="${TOKENHUB_BASE_URL}"
export XAI_API_KEY="${TOKENHUB_API_KEY}"
chmod 600 "$GROK_HOME/config.toml"

grok inspect
grok --model grok-4.5
grok -p "Reply with pong." --model grok-4.5 --yolo --max-turns 1
```

When `GROK_MODELS_BASE_URL` is set, Grok CLI sends `Authorization: Bearer` from `XAI_API_KEY` and does not use `grok login`. Keep `TOKENHUB_API_KEY` set as well so the `env_key` in `config.toml` resolves. `grok inspect` must show the isolated `GROK_HOME`, not `~/.grok`.

Leave `GROK_HOME` unset (or open a new shell) to return to the official Grok profile. Do not copy these files over `~/.grok/config.toml` or `~/.grok/auth.json`.

Never commit the project key. Prefer `read -s` or a `chmod 600` env file outside the repository.

## Supported surface

- `POST /v1/chat/completions` (including streaming)
- `POST /v1/responses` (including streaming)
- Playground chat through the same Responses bridge

Not in this release: image/video, `/v1/responses/compact`, WebSocket, native Gemini `v1beta`, Anthropic Messages, and subscription quota dashboards.

## Operational notes

- TokenHub impersonates the current Grok CLI client version when talking to chat-proxy. xAI can reject an outdated version.
- Composer models keep session affinity through `prompt_cache_key` / conversation headers.
- Scheduled token renewal follows the same five-minute lead as Codex. If xAI invalidates the refresh token, the account is marked as requiring reauthorization.

See [Architecture](architecture.md) for the `xai_grok` adapter capabilities.
