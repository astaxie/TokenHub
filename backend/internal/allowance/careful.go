package allowance

import "time"

// CarefulNeedsRefresh ports Magpie's five-point, 15-second and five-minute
// thresholds. Failed reads retry at most every 15 seconds in the caller.
func CarefulNeedsRefresh(s Snapshot, p Policy, model string, now time.Time) bool {
	if !p.CapCareful || !hasUsageCaps(p) {
		return false
	}
	if s.For(model, now, 5*time.Minute).Status == Unknown || s.Status == QueryError || s.ErrorCode != "" {
		return true
	}
	if now.Sub(s.FetchedAt) <= 15*time.Second {
		return false
	}
	for _, w := range s.Windows {
		if !s.WindowApplies(w, model) || w.Unknown || w.LocalLimit {
			continue
		}
		c, own := p.Windows[normalizeWindowName(w.Name)]
		if !own {
			c = p.Cap
		}
		if w.Informational && !own {
			continue
		}
		if c.Amount > 0 && w.Limit != nil && *w.Limit > 0 {
			c.Percent = c.Amount / *w.Limit * 100
		}
		if c.Percent > 0 && c.Percent < 100 && w.UsedPercent != nil && *w.UsedPercent >= c.Percent-5 {
			return true
		}
	}
	return false
}
