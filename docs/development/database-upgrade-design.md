# Database Upgrade Design (SQLite to PostgreSQL)

Status: design accepted, phase 1 (read-only preflight) implemented.
Target audience: backend maintainers and operators graduating a single-node
SQLite deployment to a multi-replica PostgreSQL deployment.

## Problem

A private TokenHub deployment starts on SQLite and outgrows it. The
documented migration path in `docs/postgresql-setup.md` is a manual
`sqlite3 .dump`, hand-edited SQL syntax conversion, and a `psql` import.
That path is fragile and unsafe for this codebase:

- The two dialects have diverged deliberately: per-dialect baseline schemas,
  different triggers for `request_logs.commit_sequence`, different CTE
  flavors in backfills, and `real` versus `decimal` billing columns.
- Sensitive columns persist `enc:v1:` AES-256-GCM ciphertext. A wrong
  secret key on the target silently decrypts to empty strings (the store
  layer returns `""` on failure), which would corrupt provider credentials
  without any visible error.
- `analytics_sequences` watermarks and the migration ledger are runtime
  state a blind SQL copy cannot rebuild.

## Research Summary

The industry converges on the same shape for single-binary, multi-dialect
self-hosted products (Gitea, n8n, NetBird, Vikunja, community guides for
Directus; one-api/new-api users fall back to third-party GORM copiers):

1. **Offline maintenance window**, not dual-write or CDC. SQLite has no
   logical replication; trigger-based CDC and WAL tailing are not
   production-grade substrates for a shipped feature.
2. **The application authors the target schema.** Copying SQLite DDL (via
   pgloader or dump conversion) produces type drift (booleans as bigint,
   typemod changes, missing partial indexes). Pointing the application at
   an empty PostgreSQL database already adopts the full baseline schema
   through the dbschema ledger, so the upgrade never synthesizes DDL.
3. **Rows copy through the application's own layer** when fidelity matters:
   type marshalling, NULL handling, and ciphertext handling live in one
   place. pgloader remains a fine community fallback but is not a product
   surface (extra runtime, schema drift, no knowledge of `enc:v1:`).

Properties that make this codebase cheaper to migrate than the general
case: primary keys are prefixed strings (no autoincrement sequence rewiring
outside the ledger's `migration_attempts`), JSON columns are stored as TEXT
and cast to `jsonb` at read time (no document normalization during copy),
and there are no declared foreign keys.

## Goals

- One supported, verifiable path from a file-backed SQLite database to an
  empty PostgreSQL database, executable by an operator in a maintenance
  window.
- Ciphertext is either proven intact before the copy runs, or re-encrypted
  under a new key as part of it. No silent plaintext loss.
- The source database is never modified; it is the rollback.
- Machine-readable output that later phases and CI can assert on.

## Non-Goals

- Zero-downtime or online migration (dual-write, CDC).
- Merging into a target that already holds data.
- Cross-dialect conversion for any pair other than SQLite to PostgreSQL.
- Migrating `sqlite_backup_records`: backups are bound to the SQLite file
  and stay with the retired deployment.

## Architecture

The feature ships as `tokenhub db` subcommands in `backend/internal/dbcli`,
backed by `backend/internal/dbupgrade`. Both reuse the existing maintenance
surface: `server.OpenRawDatabase` for dialect-aware raw handles and the
same house style for output and exit codes.

### Phases

| Phase | Command | State |
| --- | --- | --- |
| P1 | `tokenhub db upgrade-plan [--from <url>] --target <url> [--secret-key <key>] [--json]` | **Implemented** — read-only preflight |
| P2 | `tokenhub db upgrade-run` | Planned — snapshot, schema adopt, batched copy, watermark rebuild, verify |
| P3 | `tokenhub db upgrade-verify` | Planned — row-count and sampled checksum comparison, JSON report |

### P1: upgrade-plan (implemented)

The preflight is strictly read-only and produces the contract the later
phases consume (`dbupgrade.Plan`, rendered as text or JSON):

1. **Source inventory.** Enumerates tables from the dialect catalog, counts
   rows, and buckets each table as `protected` (carries `enc:v1:`
   columns), `history` (append-only evidence tables), `system` (ledger and
   coordination tables the target maintains itself), or `other`. The bucket
   list lives in `backend/internal/dbupgrade/registry.go`; the encrypted
   column registry is derived from the store layer's `encryptSecret` call
   sites and must gain an entry in the same change as any new protected
   column.
2. **Secret key resolution.** `--secret-key`, then `TOKENHUB_SECRET_KEY`,
   then the `.secret-key` sidecar file beside the source database
   (read-only through `server.ReadSQLiteSecretKeySidecar`; provisioning
   stays with startup). Resolution order mirrors `PrepareForStartup` so an
   explicit environment key keeps winning over the sidecar.
3. **Ciphertext canary.** For every registered protected column present in
   the source, scan rows in `rowid` pages, extract `enc:v1:` tokens from
   the raw column text (this also covers ciphertext embedded in JSON maps
   such as `providers.headers`), and verify each distinct token decrypts
   with the resolved key through `server.VerifySecretCiphertext`. Failures
   on serving-configuration columns (provider credentials, connector
   credentials, bootstrap password) are blockers; failures on historical
   artifacts (image/response job payloads, OAuth session records) are
   warnings. This exists because `decryptSecret` returns an empty string
   on any failure: without the canary, a wrong key is indistinguishable
   from empty plaintext until after cutover.
4. **Target classification.** `empty` (no tables; a SQLite-style URL whose
   file does not exist yet is classified without connecting, so the
   preflight cannot create the file as a side effect), `tokenhub` (ledger
   present; occupied targets block, freshly adopted empty targets warn),
   or `unrecognized` (tables but no ledger; blocks).
5. **Verdict.** Blockers and warnings are findings, not command failures:
   the command exits 0 whenever the preflight ran, and the rendered result
   states `READY` or `NOT READY`.

### P2: upgrade-run (planned)

1. **Consistent source snapshot.** Copy the source database with the
   existing go-sqlite3 online backup API (`copySQLiteDatabase`) plus its
   SHA-256 verification, and read from the snapshot so a still-running
   instance cannot race the copy. The operator stops the instance before
   the final cutover regardless.
2. **Schema adoption.** Point the maintenance store flow at the empty
   target so the baseline and expand migrations adopt exactly as a first
   server boot would.
3. **Batched copy.** Protected and other configuration tables first (small
   row counts), then history tables in keyset pages (`request_logs` by
   `commit_sequence`, others by primary-key cursor), via the GORM models so
   Go-side marshalling matches the serving path. If volume demands it, the
   history tables move to pgx `CopyFrom`; configuration tables never need
   it. The same-secret-key default copies ciphertext bytes verbatim; a
   `--source-secret-key` flag switches those columns to
   decrypt-and-re-encrypt during the copy, making the upgrade a key
   rotation checkpoint.
4. **Runtime state rebuild.** Recompute `analytics_sequences` watermarks
   from copied data; verify the `request_logs` commit-sequence trigger
   does not reassign copied values (the trigger definition must be read
   and verified during implementation, not assumed); do not copy ledger
   rows from the source.
5. **Post-copy verification.** Per-table row counts plus sampled row
   hashes against the snapshot, emitted as the same JSON report shape.

### Rollback

The source database is untouched. Rolling back means repointing
`TOKENHUB_DATABASE_URL` at the SQLite file and restarting. Only after the
operator accepts the PostgreSQL deployment should the SQLite file be
archived.

## Testing

- Unit tests run on file-backed SQLite through the real store flow
  (`OpenStoreWithConfig`), covering the canary (right key, wrong key, no
  key), key resolution including the sidecar, target classification
  (missing file, empty, adopted-empty, occupied, unrecognized), JSON
  round-trip, and the CLI surface. `internal/dbupgrade` and `internal/dbcli`
  hold these today.
- PostgreSQL coverage joins the existing opt-in integration pattern
  (`//go:build integration` with `TEST_POSTGRES_URL`) in P2, alongside the
  copy and verification paths that actually exercise the dialect.

## Known Constraints

- The backend does not compile on native Windows (issue #302,
  `syscall.Flock` in `backend/internal/dbschema/lock.go`). The upgrade
  commands therefore run on Linux hosts or containers, matching every
  supported deployment. Development on Windows uses a Go container.
- Money fields are `float64` in Go over `real` (SQLite) and `decimal`
  (PostgreSQL) columns. Copying through the GORM models keeps the
  marshalling identical to the serving path; a raw SQL copy would not.
