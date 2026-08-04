# Canonical Migration Bundle Schema

## Version

The bundle uses SemVer for `schema_version`. The CLI refuses mismatched major versions.

## Resource Arrays

| Field | Description |
|-------|-------------|
| `providers` | AI provider configurations |
| `provider_resources` | Provider resource instances |
| `models` | Model definitions |
| `routes` | Routing rules linking models to providers |
| `teams` | Team references (metadata only) |
| `projects` | Project configurations |
| `users` | Admin user definitions |
| `api_keys` | API key definitions (secrets via `$secretRef`) |
| `quota_policies` | Quota policy templates |

## Secret References

Secrets use `{"$secretRef": "ENV_NAME"}`. The sink resolves them at apply time from:
- `env` — environment variables
- `file` — a secrets file
- `prompt` — interactive prompt

## Warnings

Each warning has:
- `severity`: `info`, `warn`, or `blocker`
- `code`: machine-readable code
- `message`: human-readable description
- `path`: optional path to the affected field

## ID Strategies

- `stable` — deterministic from external_ref
- `prefixed` — source prefix + hash (default)
- `source` — preserve source IDs
