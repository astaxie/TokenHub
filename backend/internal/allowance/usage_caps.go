package allowance

import (
	"strings"
	"time"
)

func windowCapReached(w Window, p Policy, now time.Time) bool {
	if w.Unlimited || w.Unknown || w.ResetAt != nil && !now.Before(*w.ResetAt) || w.ExpiresAt != nil && !now.Before(*w.ExpiresAt) {
		return false
	}
	cap, own := p.Windows[strings.ToLower(w.Name)]
	if !own {
		cap = p.Cap
	}
	if w.Informational && !own && w.Pool == "" {
		return false
	}
	amount := w.Amount
	if amount == nil {
		amount = w.Spent
	}
	return cap.Percent > 0 && w.UsedPercent != nil && *w.UsedPercent >= cap.Percent || cap.Amount > 0 && amount != nil && *amount >= cap.Amount
}

// ResetHeldByCap checks all windows affected by an account reset, including
// model-scoped windows. An empty routing model intentionally excludes those
// windows, so Evaluate alone cannot authorize an account-wide reset.
func ResetHeldByCap(s Snapshot, p Policy, now time.Time, maxAge time.Duration) bool {
	if s.Version != Version || s.FetchedAt.IsZero() || s.FetchedAt.After(now.Add(time.Minute)) || maxAge > 0 && now.Sub(s.FetchedAt) > maxAge || s.Status == QueryError || s.ErrorCode != "" {
		return false
	}
	for _, w := range s.Windows {
		if windowCapReached(w, p, now) {
			return true
		}
	}
	return false
}
