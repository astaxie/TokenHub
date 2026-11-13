package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"tokenhub/backend/internal/quotasource"

	"tokenhub/backend/internal/allowance"
)

func (s *GormStore) computeManualAllowance(ctx context.Context, id string, a allowance.Snapshot, w allowance.ManualWallet, now time.Time) (allowance.Snapshot, error) {
	if !w.Enabled {
		return a, nil
	}
	rate := w.CostPerUSD
	if rate == 0 {
		if !strings.EqualFold(w.Unit, "USD") {
			return a, errors.New("manual wallet requires a USD conversion rate for its unit")
		}
		rate = 1
	}
	ids := []string{id}
	target, ok := s.GetProviderResource(id)
	if ok {
		var linked []providerAllowanceSetting
		if err := s.db.WithContext(ctx).Where("shared_account_id = ?", id).Find(&linked).Error; err != nil {
			return a, err
		}
		for _, row := range linked {
			var source quotasource.Config
			if json.Unmarshal([]byte(s.decryptSecret(row.SourceEncrypted)), &source) == nil && source.SharedAccountRevision == allowanceAccountRevision(target) {
				ids = append(ids, row.ResourceID)
			}
		}
	}
	var rows []struct {
		CreatedAt       time.Time
		ProviderCostUSD float64
	}
	if err := s.db.WithContext(ctx).Model(&UsageRecord{}).Select("created_at, provider_cost_usd").Where("provider_resource_id IN ? AND created_at >= ? AND created_at <= ?", ids, w.AsOf, now).Order("created_at asc").Find(&rows).Error; err != nil {
		return a, err
	}
	spends := make([]allowance.Spend, 0, len(rows))
	for _, row := range rows {
		spends = append(spends, allowance.Spend{At: row.CreatedAt, Amount: max(0, row.ProviderCostUSD) * rate})
	}
	balance := allowance.WalletBalance(w, spends, now)
	since := now.UTC().Truncate(24 * time.Hour)
	spent := 0.0
	for _, record := range spends {
		if !record.At.Before(since) {
			spent += record.Amount
		}
	}
	// A manual wallet is its own local observation and remains fresh without an
	// upstream collector. Native windows keep their original age and errors.
	if a.FetchedAt.IsZero() {
		a = allowance.Snapshot{Version: allowance.Version, FetchedAt: now, CheckedAt: now, Source: "manual"}
	}
	a.Windows = append(append([]allowance.Window(nil), a.Windows...), allowance.Window{Name: "Manual wallet (estimated)", Balance: &balance, Unit: w.Unit, Spent: &spent, SpentSince: &since})
	a.Status = a.For("", now, accountAllowanceMaxAge).Status
	return a, nil
}
