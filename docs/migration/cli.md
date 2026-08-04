# tokenhub-migrate CLI

## Commands

| Command | Description |
|---------|-------------|
| `sources` | List registered source adapters |
| `inspect [source]` | Inspect a source gateway configuration |
| `extract [source]` | Extract a canonical migration bundle |
| `plan` | Dry-run: show what apply would do against a remote TokenHub instance |
| `apply` | Apply a bundle to a remote TokenHub instance via the Admin API |
| `verify` | Verify bundle consistency against a remote TokenHub instance |
| `rollback` | Rollback from a checkpoint file against a remote TokenHub instance |

`plan`, `apply`, `verify`, and `rollback` require a TokenHub target: pass `--to`
or set `TOKENHUB_API` (plus `--token` or `TOKENHUB_ADMIN_TOKEN`). The CLI
refuses to run these commands against a transient in-memory store and exits
with code 5.

## Common Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--secret-source` | Secret resolution: env, file | `env` |
| `--secret-file` | `key=value` file used with `--secret-source=file` | — |
| `--id-strategy` | ID generation: stable, prefixed, source | `prefixed` |
| `--to` | TokenHub admin API base URL (or `TOKENHUB_API`) | — |
| `--token` | Admin API token (or `TOKENHUB_ADMIN_TOKEN`) | — |
| `--report` | Reserved for structured report output | — |
| `--log-level` | Reserved for future logging control | `info` |

### `apply` output files

| Flag | Description | Default |
|------|-------------|---------|
| `--checkpoint-out` | Rollback checkpoint JSON (written with mode 0600) | `<bundle>.checkpoint.json` |
| `--new-keys-out` | Newly generated API key secrets JSON (mode 0600, plaintext visible once — distribute securely, then delete) | `<bundle>.new-keys.json` |

> Note: `apply` creates users through the Admin CSV import endpoint, which
> requires an active email notification channel on the target instance.
> Applying a bundle that creates new users fails without one, and each newly
> imported user receives a password-reset email during apply.
>
> Remote apply is not transactional. If a later resource fails after earlier
> resources were changed, the command still writes the partial rollback
> checkpoint and any one-time API keys before returning exit code 5.

## Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 3 | Verify mismatch |
| 4 | Source unreadable |
| 5 | Sink rejected |
| 6 | Bundle schema mismatch |

## LiteLLM Walkthrough

```bash
# Inspect a LiteLLM config
tokenhub-migrate inspect litellm --from proxy_config.yaml

# Extract a bundle
tokenhub-migrate extract litellm --from proxy_config.yaml --out bundle.json

# Optional: preserve source IDs instead of the default prefixed IDs
tokenhub-migrate extract litellm --from proxy_config.yaml --out bundle.json --id-strategy source

# Target TokenHub instance for the remaining commands
export TOKENHUB_API=http://localhost:8080
export TOKENHUB_ADMIN_TOKEN=<admin-token>

# Plan the migration
tokenhub-migrate plan --bundle bundle.json

# Apply (dry-run)
tokenhub-migrate apply --bundle bundle.json --dry-run

# Apply for real; persists bundle.json.checkpoint.json and, when API keys
# were generated, bundle.json.new-keys.json (both mode 0600)
tokenhub-migrate apply --bundle bundle.json

# Resolve bundle secret references from a key=value file
tokenhub-migrate apply --bundle bundle.json --secret-source file --secret-file migration.secrets

# Verify command behavior
tokenhub-migrate verify --bundle bundle.json

# Rollback if needed
tokenhub-migrate rollback --checkpoint bundle.json.checkpoint.json
```
