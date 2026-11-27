package server

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"tokenhub/backend/internal/allowance"
)

type providerAllowanceWalletCounter struct {
	ResourceID      string `gorm:"primaryKey"`
	WindowKey       string `gorm:"primaryKey"`
	Cycle           string `gorm:"primaryKey"`
	PreviousBalance float64
	Spent           float64
	ObservedAt      time.Time
}

func allowanceWalletKey(w allowance.Window) string {
	encoded, _ := json.Marshal([]any{w.Name, w.Unit, w.Models, w.ExcludedModels, w.Pool, w.Period, w.PeriodSeconds})
	return fmt.Sprintf("%x", sha256.Sum256(encoded))
}
func allowanceWalletCycle(w allowance.Window, now time.Time) string {
	if w.ResetAt != nil {
		return w.ResetAt.UTC().Format(time.RFC3339Nano)
	}
	return now.UTC().Format(time.DateOnly)
}

// Wallet counters are separate from sampled history. An indexed, atomic upsert
// keeps cap arithmetic correct across concurrent collectors, top-ups, history
// pruning, restarts, and late observations.
func recordAllowanceWalletCounters(db *gorm.DB, id, raw string, now time.Time) error {
	a := allowance.Read([]byte(raw), now)
	for _, w := range a.Windows {
		if w.Balance == nil || w.Unknown || w.ResetAt != nil && !now.Before(*w.ResetAt) {
			continue
		}
		row := providerAllowanceWalletCounter{ResourceID: id, WindowKey: allowanceWalletKey(w), Cycle: allowanceWalletCycle(w, now), PreviousBalance: *w.Balance, ObservedAt: now.UTC()}
		err := db.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "resource_id"}, {Name: "window_key"}, {Name: "cycle"}},
			DoUpdates: clause.Assignments(map[string]any{
				"spent":            gorm.Expr(`provider_allowance_wallet_counters.spent + CASE WHEN provider_allowance_wallet_counters.previous_balance > excluded.previous_balance THEN provider_allowance_wallet_counters.previous_balance - excluded.previous_balance ELSE 0 END`),
				"previous_balance": gorm.Expr("excluded.previous_balance"), "observed_at": gorm.Expr("excluded.observed_at"),
			}), Where: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "provider_allowance_wallet_counters.observed_at < excluded.observed_at"}}},
		}).Create(&row).Error
		if err != nil {
			return err
		}
	}
	return nil
}
