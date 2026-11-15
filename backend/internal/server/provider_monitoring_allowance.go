package server

import (
	"encoding/json"
	"time"

	"tokenhub/backend/internal/allowance"
)

func projectProviderMonitoringAllowance(quota ProviderAccountQuotaSnapshot, now time.Time) {
	raw, _ := json.Marshal(quota)
	a := allowance.Read(raw, time.Time{})
	view := allowance.Evaluate(a, allowance.Policy{}, "", now, accountAllowanceMaxAge).View
	if projection, ok := quotaObject(quota["allowance"]); ok {
		projection["status"] = string(view.Status)
	}
	quota["limit_reached"] = view.Status == allowance.Exhausted
	if view.Status == allowance.Exhausted {
		quota["remaining_percent"] = 0
	} else if view.Status == allowance.Available && view.UsedPercent != nil {
		quota["remaining_percent"] = 100 - *view.UsedPercent
	}
	quota["fetched_at"] = a.FetchedAt.Unix()
	if a.Plan != "" {
		quota["plan_type"] = a.Plan
	}
}

// Provider-level exhaustion requires every active account to be known and
// exhausted. Model-only windows, stale reads and credits retain their semantics.
func applyProviderQuotaAvailability(s *ProviderMonitoringSnapshot, now time.Time) {
	if s.Provider.Status != StatusActive || s.ActiveResourceCount == 0 {
		return
	}
	exhausted, available := 0, false
	oldest := now
	for _, account := range s.Quota.Accounts {
		if account.ErrorCode != "" {
			continue
		}
		raw, err := json.Marshal(account.Quota)
		if err != nil {
			continue
		}
		at := time.Unix(quotaInt64(account.Quota["fetched_at"]), 0)
		a := allowance.Read(raw, at)
		view := allowance.Evaluate(a, allowance.Policy{}, "", now, accountAllowanceMaxAge).View
		if view.Status == allowance.Available {
			available = true
		}
		if view.Status != allowance.Exhausted {
			continue
		}
		exhausted++
		if a.FetchedAt.Before(oldest) {
			oldest = a.FetchedAt
		}
	}
	if available && s.State == "exhausted" {
		s.State, s.StatusLabel, s.StatusDetail = "degraded", "Degraded", "quota:provider_quota_exhausted"
		return
	}
	if exhausted != s.ActiveResourceCount {
		return
	}
	for _, signal := range []ProviderMonitoringSignal{s.ActiveProbe, s.Gateway} {
		if signal.ObservedAt != nil && signal.ObservedAt.After(oldest) && (signal.State == "down" || signal.State == "degraded") && !providerQuotaExhaustedCode(signal.ErrorCode) {
			return
		}
	}
	s.State, s.StatusLabel, s.StatusDetail = "exhausted", "Quota Exhausted", "quota:provider_quota_exhausted"
}
