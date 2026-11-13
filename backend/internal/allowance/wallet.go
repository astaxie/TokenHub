package allowance

import (
	"sort"
	"time"
)

type Spend struct {
	At     time.Time
	Amount float64
}

type walletBucket struct {
	amount  float64
	expires *time.Time
}

// WalletBalance consumes expiring grants first and applies calendar-independent
// recurring refills. It is a deterministic estimate from recorded provider cost,
// never a claim that the upstream reported this balance.
func WalletBalance(w ManualWallet, spends []Spend, now time.Time) float64 {
	buckets := []walletBucket{{amount: w.Balance}}
	for _, g := range w.Grants {
		buckets = append(buckets, walletBucket{amount: g.Amount, expires: g.ExpiresAt})
	}
	expire := func(at time.Time) {
		for i, b := range buckets {
			if b.expires != nil && !at.Before(*b.expires) {
				buckets[i].amount = 0
			}
		}
	}
	sort.SliceStable(buckets, func(i, j int) bool {
		a, b := buckets[i].expires, buckets[j].expires
		return a != nil && (b == nil || a.Before(*b))
	})
	spends = append([]Spend(nil), spends...)
	sort.SliceStable(spends, func(i, j int) bool { return spends[i].At.Before(spends[j].At) })
	next := time.Time{}
	if w.RefillAt != nil {
		next = *w.RefillAt
	}
	refill := func(at time.Time) {
		if next.IsZero() || w.RefillSeconds < 3600 || w.RefillAmount <= 0 || at.Before(next) {
			return
		}
		count := int64(at.Sub(next)/time.Second)/w.RefillSeconds + 1
		// Index zero is not necessarily the base wallet after expiry sorting.
		base := -1
		for i, b := range buckets {
			if b.expires == nil {
				base = i
				break
			}
		}
		if base < 0 {
			buckets = append(buckets, walletBucket{})
			base = len(buckets) - 1
		}
		if w.CarryOver {
			buckets[base].amount += float64(count) * w.RefillAmount
		} else {
			buckets[base].amount = w.RefillAmount
		}
		next = next.Add(time.Duration(count*w.RefillSeconds) * time.Second)
	}
	for _, spend := range spends {
		if spend.At.Before(w.AsOf) || spend.At.After(now) || !finite(spend.Amount) || spend.Amount <= 0 {
			continue
		}
		expire(spend.At)
		refill(spend.At)
		left := spend.Amount
		for i := range buckets {
			used := min(max(0, buckets[i].amount), left)
			buckets[i].amount -= used
			left -= used
			if left <= 0 {
				break
			}
		}
	}
	expire(now)
	refill(now)
	total := 0.0
	for _, b := range buckets {
		total += max(0, b.amount)
	}
	return total
}
