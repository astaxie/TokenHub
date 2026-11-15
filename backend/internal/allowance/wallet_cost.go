package allowance

import "time"

// WalletSpendAffectsBalance excludes costs discarded by a non-carrying refill.
// Earlier costs still require conversion if they could consume a surviving grant.
func WalletSpendAffectsBalance(w ManualWallet, at, now time.Time) bool {
	if at.After(now) {
		return false
	}
	start := w.AsOf
	if w.Refill != nil {
		if last := w.Refill.Last(now.In(walletLocation(w.Timezone))); last.After(start) {
			start = last
		}
	} else if !w.CarryOver && w.RefillAt != nil && w.RefillSeconds >= 3600 && w.RefillAmount > 0 && !now.Before(*w.RefillAt) {
		period := time.Duration(w.RefillSeconds) * time.Second
		last := w.RefillAt.Add(now.Sub(*w.RefillAt) / period * period)
		if last.After(start) {
			start = last
		}
	}
	if !at.Before(start) {
		return true
	}
	for _, grant := range w.Grants {
		activated := grant.At
		if activated.IsZero() {
			activated = w.AsOf
		}
		expires := GrantExpiry(grant, w.Timezone)
		if grant.Amount > 0 && !at.Before(activated) && (expires == nil || now.Before(*expires)) {
			return true
		}
	}
	return false
}
