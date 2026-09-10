package server

import (
	"context"
	"encoding/json"
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
	// Version 4 is already present in released ledgers; keep its SQL immutable.
	return dbschema.Migration{Version: 4, Name: "add-metering-evidence", Statements: []string{
		`CREATE TABLE IF NOT EXISTS metering_entries (id text PRIMARY KEY, kind text NOT NULL, scope text NOT NULL, payload text NOT NULL, created_at timestamp NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_metering_entries_scope ON metering_entries (kind, scope, created_at)`,
	}, ChecksumOverride: "tokenhub-schema-metering-evidence-v2", StatementBudget: 10}
}

func auditCorrelationMigration() dbschema.Migration {
	return dbschema.Migration{
		Version:          5,
		Name:             "add-audit-event-correlation",
		Go:               addAuditEventCorrelation,
		ChecksumOverride: "tokenhub-schema-audit-event-correlation-v1",
		StatementBudget:  10,
	}
}

func addAuditEventCorrelation(ctx context.Context, db dbschema.MigrationExecer) error {
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
func loadMeteringCard(tx *gorm.DB, kind, target string, at time.Time) (*meteringRateCard, error) {
	var rows []meteringEntry
	if err := tx.Where("kind = ? AND scope = ?", "rate_card", kind+":"+target).Order("created_at DESC, id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	var selected *meteringRateCard
	for _, row := range rows {
		var card meteringRateCard
		if err := json.Unmarshal([]byte(row.Payload), &card); err != nil {
			return nil, err
		}
		if at.Before(card.EffectiveFrom) {
			continue
		}
		if selected == nil || card.EffectiveFrom.After(selected.EffectiveFrom) || card.EffectiveFrom.Equal(selected.EffectiveFrom) && card.Revision > selected.Revision {
			copy := card
			selected = &copy
		}
	}
	return selected, nil
}
