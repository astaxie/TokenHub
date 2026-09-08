package server

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"tokenhub/backend/internal/dbschema"
)

type meteringEntry struct {
	ID        string    `json:"id" gorm:"primaryKey"`
	Kind      string    `json:"kind"`
	Scope     string    `json:"scope"`
	Payload   string    `json:"payload"`
	CreatedAt time.Time `json:"created_at"`
}

func meteringMigration() dbschema.Migration {
	return dbschema.Migration{
		Version:          4,
		Name:             "add-metering-evidence",
		Go:               addMeteringEvidence,
		ChecksumOverride: "tokenhub-schema-metering-evidence-v2",
		StatementBudget:  10,
	}
}

func addMeteringEvidence(ctx context.Context, db dbschema.MigrationExecer) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS metering_entries (id text PRIMARY KEY, kind text NOT NULL, scope text NOT NULL, payload text NOT NULL, created_at timestamp NOT NULL)`); err != nil {
		return fmt.Errorf("create metering entries: %w", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_metering_entries_scope ON metering_entries (kind, scope, created_at)`); err != nil {
		return fmt.Errorf("index metering entries: %w", err)
	}

	// current_schema() is available on PostgreSQL. SQLite reports an ordinary
	// statement error without aborting its transaction, so this is a safe,
	// read-only dialect probe inside the migration callback.
	var schema string
	if err := db.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema); err == nil {
		if _, err := db.ExecContext(ctx, `ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS correlation_id text`); err != nil {
			return fmt.Errorf("add PostgreSQL audit correlation column: %w", err)
		}
		if _, err := db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_audit_events_correlation_id ON audit_events (correlation_id)`); err != nil {
			return fmt.Errorf("index PostgreSQL audit correlation column: %w", err)
		}
		return nil
	}

	exists, err := sqliteColumnExists(ctx, db, "audit_events", "correlation_id")
	if err != nil {
		return fmt.Errorf("inspect SQLite audit correlation column: %w", err)
	}
	if !exists {
		if _, err := db.ExecContext(ctx, `ALTER TABLE audit_events ADD COLUMN correlation_id text`); err != nil {
			return fmt.Errorf("add SQLite audit correlation column: %w", err)
		}
	}
	if _, err := db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_audit_events_correlation_id ON audit_events (correlation_id)`); err != nil {
		return fmt.Errorf("index SQLite audit correlation column: %w", err)
	}
	return nil
}
func saveMeteringEntry(tx *gorm.DB, id, kind, scope string, value any, at time.Time) error {
	payload, err := encodeMetering(value)
	if err != nil {
		return err
	}
	return tx.Create(&meteringEntry{ID: id, Kind: kind, Scope: scope, Payload: payload, CreatedAt: at}).Error
}
