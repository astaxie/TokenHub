// Package accountrouting orders an already-authorized pool of accounts. It
// performs no provider calls, persistence, credential access, or model selection.
package accountrouting

import (
	"sort"
	"time"

	"tokenhub/backend/internal/allowance"
)

type Strategy string

const (
	Default   Strategy = "default"
	Smart     Strategy = "quota_smart"
	Expiry    Strategy = "expiry_first"
	LeastUsed Strategy = "least_used"
	Drain     Strategy = "drain"
)

func Valid(s Strategy) bool {
	return s == Default || s == Smart || s == Expiry || s == LeastUsed || s == Drain
}

type Candidate struct {
	ID           string
	Quota        allowance.View
	RecentTokens int64
}

type State struct {
	Revision string `json:"revision,omitempty"`
	Current  string `json:"current"`
	// Past is set only by a confirmed quota-exhaustion outcome, so a transient
	// failure never permanently advances a drain pool.
	Past bool `json:"past,omitempty"`
}

// Order returns a permutation of input indexes. Exhausted readings are retained
// as last-resort candidates: a cached reading cannot guarantee live availability
// or whether the upstream permits additional paid usage.
func Order(strategy Strategy, seats []Candidate, state State) []int {
	order := make([]int, len(seats))
	for i := range seats {
		order[i] = i
	}
	if strategy == Default || !Valid(strategy) {
		return order
	}
	// Stable identities keep ties independent of random model-route ordering.
	sort.SliceStable(order, func(i, j int) bool { return seats[order[i]].ID < seats[order[j]].ID })
	// Wallet balances are comparable only within one unit. Reorder those seats
	// in their existing slots, then keep this tie order for other comparisons.
	units := map[string][]int{}
	for pos, index := range order {
		if seats[index].Quota.Balance != nil {
			units[seats[index].Quota.Unit] = append(units[seats[index].Quota.Unit], pos)
		}
	}
	for _, positions := range units {
		wallets := make([]int, len(positions))
		for i, pos := range positions {
			wallets[i] = order[pos]
		}
		sort.SliceStable(wallets, func(i, j int) bool { return *seats[wallets[i]].Quota.Balance > *seats[wallets[j]].Quota.Balance })
		for i, pos := range positions {
			order[pos] = wallets[i]
		}
	}
	if strategy == Drain {
		at := 0
		for i, index := range order {
			if seats[index].ID == state.Current {
				at = i
				if state.Past {
					at = (i + 1) % len(order)
				}
				break
			}
		}
		order = append(append([]int{}, order[at:]...), order[:at]...)
		// A fresh exhausted account can advance before an inference refusal.
		sort.SliceStable(order, func(i, j int) bool {
			return seats[order[i]].Quota.Status != allowance.Exhausted && seats[order[j]].Quota.Status == allowance.Exhausted
		})
		return order
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := seats[order[i]], seats[order[j]]
		if x, y := tier(a.Quota.Status), tier(b.Quota.Status); x != y {
			return x < y
		}
		switch strategy {
		case LeastUsed:
			if a.RecentTokens != b.RecentTokens {
				return a.RecentTokens < b.RecentTokens
			}
		case Expiry:
			if x, y := expiry(a.Quota), expiry(b.Quota); !x.Equal(y) {
				return sooner(x, y)
			}
		case Smart:
			for k := 0; k < len(a.Quota.Renewals) || k < len(b.Quota.Renewals); k++ {
				var x, y time.Time
				if k < len(a.Quota.Renewals) {
					x = a.Quota.Renewals[k].Truncate(time.Hour)
				}
				if k < len(b.Quota.Renewals) {
					y = b.Quota.Renewals[k].Truncate(time.Hour)
				}
				if !x.Equal(y) {
					return sooner(x, y)
				}
			}
			if x, y := a.Quota.ExpiresAt, b.Quota.ExpiresAt; !x.Equal(y) {
				return sooner(x, y)
			}
		}
		x, y := 101.0, 101.0
		if a.Quota.UsedPercent != nil {
			x = *a.Quota.UsedPercent
		}
		if b.Quota.UsedPercent != nil {
			y = *b.Quota.UsedPercent
		}
		if x != y {
			return x < y
		}
		return false
	})
	return order
}

func tier(status allowance.Status) int {
	switch status {
	case allowance.Available:
		return 0
	case allowance.Unknown:
		return 1
	case allowance.QueryError:
		return 2
	case allowance.Exhausted:
		return 3
	default:
		return 1
	}
}

func sooner(a, b time.Time) bool { return !a.IsZero() && (b.IsZero() || a.Before(b)) }

func expiry(v allowance.View) time.Time {
	at := v.ExpiresAt
	for _, t := range v.Renewals {
		if sooner(t, at) {
			at = t
		}
	}
	return at
}
