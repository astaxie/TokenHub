# migration/bundle

CanonicalMigrationBundle types, schema validation, secret references,
and helper utilities shared by source adapters and sinks.

Current contract notes:
- Bundle JSON is versioned by `schema_version` and validated by the
  embedded JSON Schema.
- Secrets are represented as `{"$secretRef":"ENV_NAME"}` and never
  embedded as plaintext bundle fields.
- `quota_policies` is reserved in the v1 bundle shape but is not yet
  consumed by the TokenHub sink.
- Version 1.1 adds project/API-key model access modes and route tags while
  preserving compatibility with 1.0 bundles that omit those fields.
