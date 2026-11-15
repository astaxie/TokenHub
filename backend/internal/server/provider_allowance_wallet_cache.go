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

func localAllowanceFingerprint(p allowance.Policy) string {
	if p.SpendCap == nil {
		return manualAllowanceFingerprint(p.Wallet)
	}
	encoded, _ := json.Marshal(struct {
		Wallet   allowance.ManualWallet
		SpendCap *allowance.SpendCap
	}{p.Wallet, p.SpendCap})
	return fmt.Sprintf("%x", sha256.Sum256(encoded))
}

// Only background work scans the usage ledger. Readers use this revision-fenced
// snapshot, including its original age and any failed-computation status.
func (s *GormStore) refreshManualAllowance(ctx context.Context, id string, cfg providerAllowanceConfig, now time.Time) error {
	if !cfg.Policy.Wallet.Enabled && cfg.Policy.SpendCap == nil {
		return nil
	}
	a, computeErr := s.computeLocalAllowance(ctx, id, cfg.Policy, now)
	cache := manualAllowanceCache{Fingerprint: localAllowanceFingerprint(cfg.Policy), Allowance: a}
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
	return s.withLocalAllowance(ctx, id, a, allowance.Policy{Wallet: w}, now)
}

func (s *GormStore) withLocalAllowance(ctx context.Context, id string, a allowance.Snapshot, p allowance.Policy, now time.Time) (allowance.Snapshot, error) {
	w := p.Wallet
	if !w.Enabled && p.SpendCap == nil {
		return a, nil
	}
	var row providerAllowanceSetting
	if err := s.db.WithContext(ctx).Select("wallet_snapshot").First(&row, "resource_id = ?", id).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return a, err
	}
	var cache manualAllowanceCache
	local := allowance.Snapshot{Version: allowance.Version, Source: "manual", FetchedAt: now, Windows: []allowance.Window{{Name: "Manual wallet (estimated)", Unit: w.Unit, Unknown: true}}}
	if !w.Enabled {
		local.Windows = nil
	}
	if p.SpendCap != nil {
		local.Windows = append(local.Windows, allowance.Window{Name: "Spend cap", Unit: "USD", Unknown: true, LocalLimit: true})
	}
	if json.Unmarshal([]byte(row.WalletSnapshot), &cache) == nil && cache.Fingerprint == localAllowanceFingerprint(p) {
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
	// A hand-entered balance is Magpie's fallback when the vendor cannot tell a
	// money balance; it must not exhaust an account with a fresh vendor wallet.
	if a.Status != allowance.QueryError && a.ErrorCode == "" && now.Sub(a.FetchedAt) <= accountAllowanceMaxAge {
		for _, window := range a.Windows {
			if window.Balance != nil && !window.Unknown && !window.Informational && (window.Unit == "USD" || window.Unit == "CNY" || window.Unit == "EUR" || window.Unit == "GBP") {
				kept := local.Windows[:0]
				for _, value := range local.Windows {
					if value.LocalLimit {
						kept = append(kept, value)
					}
				}
				local.Windows = kept
				if len(local.Windows) == 0 {
					return a, nil
				}
				break
			}
		}
	}
	a.Local = &local
	a.Windows = append(append([]allowance.Window(nil), a.Windows...), local.Windows...)
	if local.Status == allowance.QueryError {
		a.Status, a.ErrorCode = allowance.QueryError, local.ErrorCode
	} else {
		a.Status = a.For("", now, accountAllowanceMaxAge).Status
	}
	return a, nil
}
