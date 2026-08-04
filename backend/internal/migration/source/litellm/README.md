# migration/source/litellm

LiteLLM file-based source adapter for the migration framework.

Supported versions: `>=1.52.0, <1.70.0`.

Current scope:
- reads `proxy_config.yaml`-style LiteLLM config files
- migrates a practical subset into `CanonicalMigrationBundle`
- supports providers, provider resources, models, routes, teams,
  users, projects, and API keys when they are statically representable

Reference materialization:
- teams are materialized from declared teams and from team ids referenced
  by users or virtual keys
- team-backed projects are materialized from actual referenced team ids,
  even if the team was not explicitly declared in `key_management_settings.teams`
- API keys preserve project linkage, and LiteLLM `user_id` is carried in
  API key metadata as `litellm_user_id` and `litellm_user_ref`

Current limitations:
- no database/runtime state extraction yet
- budgets, callbacks, guardrails, fallback chains, and many router
  settings are warning-only
- inline `api_key` values are converted into operator-supplied
  secret refs; `os.environ/ENV_NAME` preserves the original env name
- username normalization may still require future collision handling for
  large real-world migrations
