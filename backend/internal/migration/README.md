# migration

Migration framework for moving competing AI gateways into TokenHub.

## Packages

| Package | Purpose |
|---------|--------|
| `bundle/` | Canonical bundle types, JSON Schema validation, secret references |
| `source/` | Source adapter interface and registry |
| `source/litellm/` | LiteLLM file-based adapter |
| `sink/tokenhub/` | Store-backed and HTTP-backed TokenHub sink |
| `cli/` | Command implementations for `tokenhub-migrate` |

See `docs/migration/` for user-facing documentation.
