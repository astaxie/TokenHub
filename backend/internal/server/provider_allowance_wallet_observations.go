package server

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"time"

	"tokenhub/backend/internal/allowance"
	"tokenhub/backend/internal/quotasource"
)

func allowanceAccountRevision(r ProviderResource) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d", r.ID, r.CreatedAt.UTC().Format(time.RFC3339Nano), providerCredentialReplacementGeneration(r.Options))))
	return fmt.Sprintf("%x", sum[:])
}

// Shared wallets are explicit and pinned to account identity. Equal balances
// alone never establish that two keys draw on the same account's money.
func (s *GormStore) withWalletObservations(ctx context.Context, id string, a allowance.Snapshot, source quotasource.Config, now time.Time) (allowance.Snapshot, error) {
	if source.SharedAccountID != "" {
		target, ok := s.GetProviderResource(source.SharedAccountID)
		if !ok || source.SharedAccountRevision == "" || allowanceAccountRevision(target) != source.SharedAccountRevision {
			a.Status = allowance.QueryError
			a.ErrorCode = "shared_wallet_account_changed"
			return a, nil
		}
		targetConfig, err := s.providerAllowanceConfig(ctx, target.ID)
		if err != nil {
			return a, err
		}
		observation, ok := s.GetProviderResourceObservation(target.ID)
		if !ok && targetConfig.Policy.Wallet.Enabled {
			observation = ProviderResourceObservation{QuotaFetchedAt: &time.Time{}, QuotaSnapshot: `{"allowance":{"version":1,"status":"unknown"}}`}
			ok = true
		}
		if !ok || observation.QuotaFetchedAt == nil {
			a.Status = allowance.QueryError
			a.ErrorCode = "shared_wallet_unknown"
			return a, nil
		}
		shared := allowance.Read([]byte(observation.QuotaSnapshot), *observation.QuotaFetchedAt)
		if targetConfig.Policy.Wallet.Enabled {
			shared, err = s.withManualAllowance(ctx, target.ID, shared, targetConfig.Policy.Wallet, now)
			if err != nil {
				return a, err
			}
		}
		if (shared.For("", now, accountAllowanceMaxAge).Status == allowance.QueryError || shared.For("", now, accountAllowanceMaxAge).Status == allowance.Unknown) || now.Sub(shared.FetchedAt) > accountAllowanceMaxAge {
			a.Status = allowance.QueryError
			a.ErrorCode = "shared_wallet_stale"
			return a, nil
		}
		windows := make([]allowance.Window, 0, len(a.Windows))
		for _, w := range a.Windows {
			if w.Balance == nil {
				windows = append(windows, w)
			}
		}
		found := false
		for _, w := range shared.Windows {
			if w.Balance != nil {
				windows = append(windows, w)
				found = true
			}
		}
		if !found {
			a.Status = allowance.QueryError
			a.ErrorCode = "shared_wallet_unknown"
			return a, nil
		}
		a.Windows = windows
		if a.FetchedAt.IsZero() {
			a.FetchedAt = shared.FetchedAt
			a.CheckedAt = shared.CheckedAt
			a.Source = shared.Source
		}
		id = target.ID
	}

	for i, w := range a.Windows {
		if w.Balance == nil || w.Unknown {
			continue
		}
		var counter providerAllowanceWalletCounter
		err := s.db.WithContext(ctx).Where("resource_id = ? AND window_key = ? AND cycle = ?", id, allowanceWalletKey(w), allowanceWalletCycle(w, now)).First(&counter).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return a, err
		}
		if err == nil {
			spent := counter.Spent
			a.Windows[i].Spent = &spent
			since := now.UTC().Truncate(24 * time.Hour)
			if w.ResetAt != nil && w.PeriodSeconds > 0 {
				since = w.ResetAt.Add(-time.Duration(w.PeriodSeconds) * time.Second)
			}
			a.Windows[i].SpentSince = &since
		}
	}
	return a, nil
}
