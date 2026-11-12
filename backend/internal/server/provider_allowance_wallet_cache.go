package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"tokenhub/backend/internal/allowance"
)

type manualAllowanceCache struct {
	Fingerprint string             `json:"fingerprint"`
	Allowance   allowance.Snapshot `json:"allowance"`
}

func manualAllowanceFingerprint(w allowance.ManualWallet) string {
	encoded, _ := json.Marshal(w)
	return fmt.Sprintf("%x", sha256.Sum256(encoded))
}

// Only background work scans the usage ledger. Readers use this revision-fenced
// snapshot, including its original age and any failed-computation status.
func (s *GormStore) refreshManualAllowance(ctx context.Context, id string, cfg providerAllowanceConfig, now time.Time) error {
	if !cfg.Policy.Wallet.Enabled {
		return nil
	}
	a, computeErr := s.computeManualAllowance(ctx, id, allowance.Snapshot{}, cfg.Policy.Wallet, now)
	cache := manualAllowanceCache{Fingerprint: manualAllowanceFingerprint(cfg.Policy.Wallet), Allowance: a}
	if computeErr != nil {
		var previous providerAllowanceSetting
		if err := s.db.WithContext(ctx).Select("wallet_snapshot").First(&previous, "resource_id = ?", id).Error; err != nil {
			return err
		}
		var old manualAllowanceCache
		if json.Unmarshal([]byte(previous.WalletSnapshot), &old) == nil && old.Fingerprint == cache.Fingerprint {
			cache = old
		}
		cache.Allowance.Version = allowance.Version
		cache.Allowance.Status, cache.Allowance.ErrorCode, cache.Allowance.CheckedAt = allowance.QueryError, "manual_wallet_compute_failed", now
	}
	encoded, err := json.Marshal(cache)
	if err != nil {
		return err
	}
	updated := s.db.WithContext(ctx).Model(&providerAllowanceSetting{}).Where("resource_id = ? AND revision = ?", id, cfg.Revision).Update("wallet_snapshot", string(encoded))
	if updated.Error != nil {
		return updated.Error
	}
	if updated.RowsAffected != 1 {
		return NewHTTPError(409, "allowance_config_changed", "Wallet policy changed during computation")
	}
	return computeErr
}

func (s *GormStore) withManualAllowance(ctx context.Context, id string, a allowance.Snapshot, w allowance.ManualWallet, now time.Time) (allowance.Snapshot, error) {
	if !w.Enabled {
		return a, nil
	}
	var row providerAllowanceSetting
	if err := s.db.WithContext(ctx).Select("wallet_snapshot").First(&row, "resource_id = ?", id).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return a, err
	}
	var cache manualAllowanceCache
	local := allowance.Snapshot{Version: allowance.Version, Source: "manual", FetchedAt: now, Windows: []allowance.Window{{Name: "Manual wallet (estimated)", Unit: w.Unit, Unknown: true}}}
	if json.Unmarshal([]byte(row.WalletSnapshot), &cache) == nil && cache.Fingerprint == manualAllowanceFingerprint(w) {
		if len(cache.Allowance.Windows) == 0 {
			cache.Allowance.Windows = local.Windows
		}
		local = cache.Allowance
		if local.For("", now, accountAllowanceMaxAge).Status == allowance.Unknown {
			for i := range local.Windows {
				local.Windows[i].Unknown = true
			}
		}
	}
	if a.FetchedAt.IsZero() {
		a.Version, a.FetchedAt, a.CheckedAt, a.Source = allowance.Version, local.FetchedAt, local.CheckedAt, "manual"
	}
	a.Windows = append(append([]allowance.Window(nil), a.Windows...), local.Windows...)
	if local.Status == allowance.QueryError {
		a.Status, a.ErrorCode = allowance.QueryError, local.ErrorCode
	} else {
		a.Status = a.For("", now, accountAllowanceMaxAge).Status
	}
	return a, nil
}
