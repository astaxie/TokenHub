# LiteLLM Source Adapter

## Supported Versions

`>=1.52.0, <1.70.0`

## Input

The adapter reads a LiteLLM `proxy_config.yaml` file. Future versions will also support Postgres DB dumps.

## Feature Mapping

| LiteLLM Feature | TokenHub Equivalent | Status |
|----------------|-------------------|--------|
| `model_list` providers | Providers + Provider Resources | Supported |
| `model_list` models | Models | Supported |
| `model_list` routing | Routes | Supported |
| `key_management_settings.teams` | Teams (metadata) | Supported |
| `key_management_settings.users` | Admin Users | Supported |
| `key_management_settings.virtual_keys` | API Keys | Supported |
| `key_management_settings.budgets` | — | Warning only |
| `general_settings` | — | Partial, warning |
| `router_settings` | — | Partial, warning |
| `environment_variables` | SecretRef | Supported |

## Unsupported Features

The following LiteLLM features are not yet mapped and will appear as warnings:
- Budgets and spend limits
- Callbacks (success/failure)
- Guardrails
- Fallback chains
- Pass-through routes
- Non-OpenAI-compatible providers
- Audio and image endpoints
- Prompt caching

## Secret Handling

- `os.environ/ENV_NAME` values are preserved as `SecretRef{Ref: "ENV_NAME"}`
- Inline API keys are converted to `SecretRef` values
- No plaintext secrets are embedded in the bundle

## Key Rotation

After migration, TokenHub issues new API keys. The old LiteLLM keys should be rotated. The `report.new_keys[]` field in the apply report maps old key references to new TokenHub key values.
