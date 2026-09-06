package server

import (
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
	return dbschema.Migration{Version: 4, Name: "add-metering-evidence", Statements: []string{
		`CREATE TABLE IF NOT EXISTS metering_entries (id text PRIMARY KEY, kind text NOT NULL, scope text NOT NULL, payload text NOT NULL, created_at timestamp NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_metering_entries_scope ON metering_entries (kind, scope, created_at)`,
	}}
}
func saveMeteringEntry(tx *gorm.DB, id, kind, scope string, value any, at time.Time) error {
	payload, err := encodeMetering(value)
	if err != nil {
		return err
	}
	return tx.Create(&meteringEntry{ID: id, Kind: kind, Scope: scope, Payload: payload, CreatedAt: at}).Error
}
