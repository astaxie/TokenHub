package server

import (
	"context"
	"time"

	"tokenhub/backend/internal/allowance"
)

func (s *GormStore) computeLocalAllowance(ctx context.Context, id string, p allowance.Policy, now time.Time) (allowance.Snapshot, error) {
	a, err := s.computeManualAllowance(ctx, id, allowance.Snapshot{}, p.Wallet, now)
	if err != nil || p.SpendCap == nil {
		return a, err
	}
	start, reset := p.SpendCap.Window(now)
	var total struct{ Amount float64 }
	if err := s.db.WithContext(ctx).Model(&UsageRecord{}).Select("COALESCE(SUM(provider_cost_usd), 0) AS amount").Where("provider_resource_id = ? AND created_at >= ? AND created_at <= ?", id, start.UTC(), now.UTC()).Scan(&total).Error; err != nil {
		return a, err
	}
	spent, limit := max(0, total.Amount), p.SpendCap.USD
	used := spent / limit * 100
	w := allowance.Window{Name: "Spend cap", Amount: &spent, Limit: &limit, Unit: "USD", UsedPercent: &used, LocalLimit: true}
	if !reset.IsZero() {
		w.ResetAt = &reset
	}
	if a.FetchedAt.IsZero() {
		a = allowance.Snapshot{Version: allowance.Version, Source: "manual", FetchedAt: now, CheckedAt: now}
	}
	a.Windows = append(a.Windows, w)
	a.Status = a.For("", now, accountAllowanceMaxAge).Status
	return a, nil
}
