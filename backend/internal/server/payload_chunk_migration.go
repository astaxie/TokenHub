package server

import (
	"context"
	"fmt"

	"tokenhub/backend/internal/dbschema"
)

// payloadChunkDedupMigration adds the content-addressed chunk store for large
// audit snapshots: payload_chunks holds unique chunk bodies, payload_chunk_refs
// keeps the ordered references per request, and the payload log rows gain
// flags marking which side is chunked.
func payloadChunkDedupMigration() dbschema.Migration {
	return dbschema.Migration{
		Version:          12,
		Name:             "add-payload-chunk-dedup",
		Go:               addPayloadChunkDedup,
		ChecksumOverride: "tokenhub-schema-payload-chunk-dedup-v1",
		// Two tables (each with one index), two columns, and the SQLite column
		// probes.
		StatementBudget: 12,
	}
}

func addPayloadChunkDedup(ctx context.Context, db dbschema.MigrationExecer) error {
	// current_schema() is available on PostgreSQL; SQLite reports an ordinary
	// statement error without aborting its transaction, so this stays a safe
	// read-only dialect probe (same pattern as the audit correlation migration).
	var schema string
	if err := db.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema); err == nil {
		return addPayloadChunkDedupPostgres(ctx, db)
	}
	return addPayloadChunkDedupSQLite(ctx, db)
}

func addPayloadChunkDedupPostgres(ctx context.Context, db dbschema.MigrationExecer) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS "payload_chunks" ("hash" text PRIMARY KEY,"body" text NOT NULL,"created_at" timestamptz)`,
		`CREATE INDEX IF NOT EXISTS "idx_payload_chunks_created_at" ON "payload_chunks" ("created_at")`,
		`CREATE TABLE IF NOT EXISTS "payload_chunk_refs" ("request_id" text,"kind" text,"position" bigint,"chunk_hash" text,PRIMARY KEY ("request_id","kind","position"))`,
		`CREATE INDEX IF NOT EXISTS "idx_payload_chunk_refs_chunk_hash" ON "payload_chunk_refs" ("chunk_hash")`,
		`ALTER TABLE "request_payload_logs" ADD COLUMN IF NOT EXISTS "request_chunked" boolean`,
		`ALTER TABLE "request_payload_logs" ADD COLUMN IF NOT EXISTS "response_chunked" boolean`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply postgres payload chunk dedup migration: %w", err)
		}
	}
	return nil
}

func addPayloadChunkDedupSQLite(ctx context.Context, db dbschema.MigrationExecer) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS "payload_chunks" ("hash" text PRIMARY KEY,"body" text NOT NULL,"created_at" datetime)`,
		`CREATE INDEX IF NOT EXISTS "idx_payload_chunks_created_at" ON "payload_chunks" ("created_at")`,
		`CREATE TABLE IF NOT EXISTS "payload_chunk_refs" ("request_id" text,"kind" text,"position" integer,"chunk_hash" text,PRIMARY KEY ("request_id","kind","position"))`,
		`CREATE INDEX IF NOT EXISTS "idx_payload_chunk_refs_chunk_hash" ON "payload_chunk_refs" ("chunk_hash")`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply sqlite payload chunk dedup migration: %w", err)
		}
	}
	for _, column := range []string{"request_chunked", "response_chunked"} {
		exists, err := sqliteColumnExists(ctx, db, "request_payload_logs", column)
		if err != nil {
			return fmt.Errorf("inspect sqlite request_payload_logs column %s: %w", column, err)
		}
		if exists {
			continue
		}
		if _, err := db.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE "request_payload_logs" ADD COLUMN "%s" numeric`, column)); err != nil {
			return fmt.Errorf("add sqlite request_payload_logs column %s: %w", column, err)
		}
	}
	return nil
}
