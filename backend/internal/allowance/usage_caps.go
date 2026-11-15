package allowance

import (
	"strings"
	"time"
)

func normalizeWindowName(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

func hasUsageCaps(p Policy) bool {
	active := func(c Cap) bool { return c.Amount > 0 || c.Percent > 0 && c.Percent < 100 }
	if active(p.Cap) {
		return true
	}
	for _, cap := range p.Windows {
		if active(cap) {
			return true
		}
	}
	return false
}

func windowCapReached(w Window, p Policy, now time.Time) bool {
	if w.Unlimited || w.Unknown || w.ResetAt != nil && !now.Before(*w.ResetAt) || w.ExpiresAt != nil && !now.Before(*w.ExpiresAt) {
		return false
	}
	if w.LocalLimit {
		return w.UsedPercent != nil && *w.UsedPercent >= 100
	}
	cap, own := p.Windows[normalizeWindowName(w.Name)]
	if !own {
		cap = p.Cap
	}
	if w.Informational && !own {
		return false
	}
	amount := w.Amount
	if amount == nil {
		amount = w.Spent
	}
	if cap.Amount > 0 && amount != nil && (w.Limit != nil && *w.Limit > 0 || w.Spent != nil) {
		return *amount >= cap.Amount
	}
	if cap.Amount > 0 {
		cap = p.Cap
	}
	return cap.Percent > 0 && cap.Percent < 100 && w.UsedPercent != nil && *w.UsedPercent >= cap.Percent
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
