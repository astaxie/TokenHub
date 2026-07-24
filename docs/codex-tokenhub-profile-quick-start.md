# Connect Codex to TokenHub: Profile Quick Setup

Language: English | [简体中文](zh-CN/codex-tokenhub-profile-quick-start.md) | [日本語](ja/codex-tokenhub-profile-quick-start.md)

> This guide is for users who only need to connect Codex to TokenHub through an isolated `tokenhub` profile. It covers profile creation, API key setup, validation, and recovery.
>
> To compare profile, process-local, global CLI, and desktop configuration, see [Connect Codex to TokenHub: Four Configuration Methods and Recovery](codex-tokenhub-configuration.md).

Before users follow this guide, an administrator must configure an OpenAI Codex provider, subscription account resources, and model routes in TokenHub, and then create an API key for the project.

## 1. How the profile works

The `tokenhub` profile is loaded only when Codex starts with `--profile tokenhub`.

| Command | Primary configuration | Request path |
| --- | --- | --- |
| `codex` | `~/.codex/config.toml` | Default Codex provider |
| `codex --profile tokenhub` | `~/.codex/tokenhub.config.toml` | TokenHub |

The profile does not overwrite the default `config.toml`, affects only explicitly selected sessions, and can be bypassed by omitting the profile argument.

Screenshots must come from real configuration or real requests. Fully redact API keys, login tokens, OAuth tokens, account identifiers, session IDs, private hosts, usernames, absolute paths, project names, and request IDs.

---

## 2. Use an existing profile

Before starting, confirm that TokenHub is available, the project API key is valid, and the configured model has a healthy route.

For a profile that uses `env_key`, enter the key without terminal echo and start Codex:

```bash
read -r -s "TOKENHUB_API_KEY?TokenHub project API key: "
export TOKENHUB_API_KEY
echo

codex --profile tokenhub
```

If the profile uses `experimental_bearer_token`, skip `read` and `export` and run `codex --profile tokenhub` directly.

After startup, run `/status`. `Model provider` must show TokenHub and `Model` must match the profile.

---

## 3. Create the profile

### 3.1 Check Codex CLI

```bash
codex --version
```

Install and sign in to Codex CLI first if the command is unavailable.

### 3.2 Check TokenHub

This guide uses the local endpoint:

```text
http://127.0.0.1:8080
```

Run the health check:

```bash
curl --fail-with-body http://127.0.0.1:8080/healthz
```

Expected response:

```json
{"service":"tokenhub-backend","status":"ok"}
```

If the service is unavailable, start TokenHub from the repository:

```bash
cd "/enter/the/absolute/path/to/TokenHub"
./start.sh
```

Keep that process running and continue in a new terminal.

### 3.3 Back up an existing profile

```bash
mkdir -p "$HOME/.codex"

if [ -f "$HOME/.codex/tokenhub.config.toml" ]; then
  cp -p "$HOME/.codex/tokenhub.config.toml" \
    "$HOME/.codex/tokenhub.config.toml.before-edit.$(date +%Y%m%d-%H%M%S)"
fi
```

### 3.4 Write the profile

Open the file:

```bash
nano "$HOME/.codex/tokenhub.config.toml"
```

Recommended environment-variable configuration:

```toml
model_provider = "tokenhub"
model = "gpt-5.6-luna"

[model_providers.tokenhub]
name = "TokenHub Local"
base_url = "http://127.0.0.1:8080/v1"
env_key = "TOKENHUB_API_KEY"
env_key_instructions = "Set TOKENHUB_API_KEY before starting Codex"
wire_api = "responses"
```

In `nano`, press `Control-O`, press Enter to confirm the filename, and press `Control-X`.

Requirements:

- `base_url` must include `/v1`.
- Do not declare `model_provider`, `model`, or `[model_providers.tokenhub]` more than once.
- Codex 0.134.0 and later use a separate `<profile>.config.toml` file instead of the legacy `[profiles.tokenhub]` table.

For a controlled personal development machine, you may store the project API key directly. Remove `env_key` and `env_key_instructions` and use:

```toml
[model_providers.tokenhub]
name = "TokenHub Local"
base_url = "http://127.0.0.1:8080/v1"
experimental_bearer_token = "paste your TokenHub project API key here"
wire_api = "responses"
```

Do not combine `experimental_bearer_token` with `env_key`, `[model_providers.tokenhub.auth]`, or `requires_openai_auth`. The key is stored in plaintext and this option is intended for development use only.

```bash
chmod 600 "$HOME/.codex/tokenhub.config.toml"
```

Never commit, upload, share, or screenshot a profile containing a key.

![Real TokenHub profile configuration with the Base URL redacted](assets/codex-profile/tokenhub-profile-config-redacted.png)

*Figure 1: Real environment-variable profile configuration. The Base URL is redacted and no API key is stored in the file.*

### 3.5 Optional: validate the TOML

Skip this step if the profile already starts successfully and passes the connectivity test in section 4.3.

```bash
python3 - <<'PY'
from pathlib import Path
import tomllib

path = Path.home() / ".codex" / "tokenhub.config.toml"
with path.open("rb") as file:
    config = tomllib.load(file)

print("Profile configuration loaded")
print("Model:", config["model"])
print("Provider:", config["model_provider"])
print("Base URL:", config["model_providers"]["tokenhub"]["base_url"])
PY
```

Expected output for this environment:

```text
Profile configuration loaded
Model: gpt-5.6-luna
Provider: tokenhub
Base URL: http://127.0.0.1:8080/v1
```

---

## 4. Daily use and validation

### 4.1 Enter the project API key

This section applies only when the profile uses `env_key = "TOKENHUB_API_KEY"`.

1. Run the `read` command below.
2. When the prompt appears, paste the real project API key and press Enter.
3. The terminal displays no characters or asterisks because input echo is disabled.
4. Run `export TOKENHUB_API_KEY`, then run `echo`.

```bash
read -r -s "TOKENHUB_API_KEY?TokenHub project API key: "
export TOKENHUB_API_KEY
echo
```

- `read` reads one line from the terminal.
- `-r` preserves backslashes.
- `-s` disables input echo.
- `TOKENHUB_API_KEY?...` stores the value in `TOKENHUB_API_KEY` and displays the text after `?`.
- `export TOKENHUB_API_KEY` makes the value available to Codex.
- `echo` restores a clean prompt line.

The variable exists only in the current terminal session. Avoid putting the real key directly in an `export` command because it can enter shell history.

Check only whether the variable exists:

```bash
test -n "${TOKENHUB_API_KEY:-}" &&
  echo "TOKENHUB_API_KEY is set"
```

### 4.2 Start Codex

In the current directory:

```bash
codex --profile tokenhub
```

For a specific project:

```bash
codex --profile tokenhub \
  --cd "/enter/the/absolute/project/path"
```

### 4.3 Run a one-time connectivity test

```bash
codex exec \
  --profile tokenhub \
  --ephemeral \
  --sandbox read-only \
  "Do not use tools. Reply only: Connection successful"
```

The real test for this environment returned:

```text
OpenAI Codex v0.145.0
model: gpt-5.6-luna
provider: tokenhub
Connection successful
```

Success requires the expected model, `provider: tokenhub`, the final response, and a corresponding HTTP 200 entry in TokenHub request logs.

### 4.4 Check runtime status

Run `/status` in Codex.

![Real Codex status through the TokenHub profile with sensitive information redacted](assets/codex-profile/codex-status-redacted.png)

*Figure 2: Real `/status` output. Provider details, window title, and session ID are redacted.*

---

## 5. Request path and dependencies

```text
Current terminal
  → tokenhub profile
  → http://127.0.0.1:8080/v1
  → TokenHub project authentication and model routing
  → Connected OpenAI Codex account resource
  → Model response
```

`GET /v1/models` proves only that the model is visible to the key. A successful request also requires a healthy provider, a healthy account resource, an enabled model route, a valid project API key, and streaming Responses API support.

This environment completed a real streaming Responses request for `gpt-5.6-luna`; TokenHub recorded HTTP 200.

---

## 6. Recover or disable the profile

Use the default Codex configuration:

```bash
codex
```

Clear the key from the current terminal:

```bash
unset TOKENHUB_API_KEY
```

Disable the profile:

```bash
mv "$HOME/.codex/tokenhub.config.toml" \
  "$HOME/.codex/tokenhub.config.toml.disabled"
```

Re-enable it:

```bash
mv "$HOME/.codex/tokenhub.config.toml.disabled" \
  "$HOME/.codex/tokenhub.config.toml"
```

If a profile existed before this setup, restore its backup instead of replacing it with the renamed file.

---

## 7. Troubleshooting

| Symptom | Likely cause | Action |
| --- | --- | --- |
| Missing `TOKENHUB_API_KEY` | The current terminal did not export the key | Repeat section 4.1 |
| HTTP 401 | Missing, expired, or wrong project API key | Copy or rotate the project key and enter it again |
| HTTP 503 / `provider_unavailable` | No healthy route for the model | Check provider, account resource, route, and provider status |
| Profile not found | File is missing or misnamed | Check `~/.codex/tokenhub.config.toml` |
| Old provider still active | The current process did not reload configuration | Exit Codex completely and restart with the profile |
| `doctor` rejects `--profile` | This Codex CLI command does not accept the profile argument | Validate with a real `codex exec --profile tokenhub` request |

The filename must be `tokenhub.config.toml`, not `tokenhub.toml` or `tokenhub.config.toml.txt`.

---

## 8. Security controls

- Prefer `env_key` and an environment variable.
- Use `experimental_bearer_token` only on a controlled personal development machine.
- Set profile permissions to `600` when it contains a key.
- Never commit `.env`, API keys, OAuth tokens, account credentials, or a profile containing a bearer token.
- Rotate a key immediately if it appears in chat, screenshots, or shell history.

## 9. Related documentation

- [Connect Codex to TokenHub: Four Configuration Methods and Recovery](codex-tokenhub-configuration.md)
- [Model API User Guide](user-guide.md)
