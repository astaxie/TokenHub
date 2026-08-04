# Migration Framework Architecture

## Overview

The migration framework uses a three-phase architecture:

1. **Source Adapter** — reads competitor gateway configuration and emits a `CanonicalMigrationBundle`
2. **Canonical Bundle** — a versioned, secret-free JSON intermediate representation
3. **TokenHub Sink** — idempotently applies the bundle to TokenHub via the Admin API

## Adding a New Source Adapter

1. Implement the `source.Extractor` interface in `backend/internal/migration/source/<name>/`
2. Register the adapter via `init()` using `source.Register()`
3. Add fixtures under `testdata/`
4. Add documentation under `docs/migration/<name>.md`

### Extractor Interface

```go
type Extractor interface {
    Name() string
    SupportedVersions() []string
    Probe(ctx context.Context, opts ExtractOptions) (Info, error)
    Extract(ctx context.Context, opts ExtractOptions) (*bundle.CanonicalMigrationBundle, error)
}
```

## Sink Operations

- **Plan** — dry-run, reports what would change
- **Apply** — idempotent upsert into TokenHub
- **Verify** — confirms applied state matches bundle
- **Rollback** — reverts to pre-apply state using checkpoint
