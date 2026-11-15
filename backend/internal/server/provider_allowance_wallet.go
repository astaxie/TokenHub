package server

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
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
	if rate == 0 && strings.EqualFold(w.Unit, "CNY") {
		fx, err := loadMeteringFX(s.db.WithContext(ctx), "CNY", now)
		if err != nil {
			return a, err
		}
		if fx != nil {
			if usd, err := strconv.ParseFloat(fx.Rate, 64); err == nil && usd > 0 && !math.IsInf(1/usd, 0) {
				rate = 1 / usd
			}
		}
	}
	if rate == 0 && strings.EqualFold(w.Unit, "USD") {
		rate = 1
	}
	if rate == 0 && !strings.EqualFold(w.Unit, "CNY") {
		return a, errors.New("manual wallet requires a USD conversion rate for its unit")
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
	start := w.AsOf
	for _, g := range w.Grants {
		if !g.At.IsZero() && g.At.Before(start) {
			start = g.At
		}
	}
	if err := s.db.WithContext(ctx).Model(&UsageRecord{}).Select("created_at, provider_cost_usd").Where("provider_resource_id IN ? AND created_at >= ? AND created_at <= ?", ids, start.UTC(), now.UTC()).Order("created_at asc").Find(&rows).Error; err != nil {
		return a, err
	}
	spends := make([]allowance.Spend, 0, len(rows))
	unknown := false
	for _, row := range rows {
		if rate == 0 && row.ProviderCostUSD > 0 {
			unknown = true
		}
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
	window := allowance.Window{Name: "Manual wallet (estimated)", Unit: w.Unit, Spent: &spent, SpentSince: &since, Unknown: unknown}
	if unknown {
		window.Spent = nil
	}
	if !unknown {
		window.Balance = &balance
	}
	if w.Refill != nil {
		loc := time.UTC
		if w.Timezone != "" {
			loc, _ = time.LoadLocation(w.Timezone)
			if loc == nil {
				loc = time.UTC
			}
		}
		next := w.Refill.Next(now.In(loc))
		window.ResetAt = &next
	}
	a.Windows = append(append([]allowance.Window(nil), a.Windows...), window)
	a.Status = a.For("", now, accountAllowanceMaxAge).Status
	return a, nil
}
