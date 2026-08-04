# migration/sink/tokenhub

Store-backed and HTTP-backed TokenHub sink implementations for planning,
applying, verifying, and rolling back canonical migration bundles.

Current scope:
- applies providers, provider resources, models, routes, users,
  projects, and API keys
- verifies bundle presence by business keys and supports checkpoint-
  based rollback for resources created during apply
- uses canonical reference fields such as `provider_ref`, `team_ref`,
  and `project_ref` instead of requiring source external IDs inside the
  embedded TokenHub specs
- enforces zero-write idempotency on a second apply when the target
  state already matches the bundle
- keeps resolved raw API key secrets only for keys created during the
  current sink instance lifecycle via `NewKeys()`

Current limitations:
- does not yet implement quota policy materialization
- does not yet implement update/delete rollback for pre-existing resources

Store-backed sink:
- in-process sink used for unit and integration testing

HTTP-backed sink:
- remote sink using the TokenHub Admin API for plan, apply, verify, and rollback flows
