package server

import (
	"encoding/json"
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
