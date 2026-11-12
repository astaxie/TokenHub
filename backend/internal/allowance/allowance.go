// Package allowance describes provider-reported account quota independently of
// the provider protocol, persistence, and routing policy.
package allowance

import (
	"math"
	"path"
	"strings"
	"time"
)

type Status string

const (
	Unknown    Status = "unknown"
	Available  Status = "available"
	Exhausted  Status = "exhausted"
	QueryError Status = "query_error"
)

const Version = 1

type Window struct {
	Name           string     `json:"name,omitempty"`
	UsedPercent    *float64   `json:"used_percent,omitempty"`
	Amount         *float64   `json:"amount,omitempty"`
	Limit          *float64   `json:"limit,omitempty"`
	Balance        *float64   `json:"balance,omitempty"`
	Unit           string     `json:"unit,omitempty"`
	PeriodSeconds  int64      `json:"period_seconds,omitempty"`
	ResetAt        *time.Time `json:"reset_at,omitempty"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	Models         []string   `json:"models,omitempty"`
	ExcludedModels []string   `json:"excluded_models,omitempty"`
	Informational  bool       `json:"informational,omitempty"`
	Credits        bool       `json:"credits,omitempty"`
}

type Credits struct {
	Available *bool    `json:"available,omitempty"`
	Unlimited bool     `json:"unlimited,omitempty"`
	Balance   *float64 `json:"balance,omitempty"`
	Unit      string   `json:"unit,omitempty"`
}

type Snapshot struct {
	Version   int        `json:"version"`
	Status    Status     `json:"status"`
	Windows   []Window   `json:"windows,omitempty"`
	Credits   *Credits   `json:"credits,omitempty"`
	FetchedAt time.Time  `json:"fetched_at,omitzero"`
	CheckedAt time.Time  `json:"checked_at,omitzero"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	ErrorCode string     `json:"error_code,omitempty"`
	// ExplicitExhausted applies to an account-wide upstream refusal when no
	// measurable windows were reported. It never widens a model-specific limit.
	ExplicitExhausted bool `json:"explicit_exhausted,omitempty"`
}

type View struct {
	Status      Status
	UsedPercent *float64
	Renewals    []time.Time
	ExpiresAt   time.Time
	Balance     *float64
	Unit        string
}

// For uses only observations that are still current. A passed reset does not
// prove a new window is empty; it makes the old reading unknown until refreshed.
func (s Snapshot) For(model string, now time.Time, maxAge time.Duration) View {
	v := View{Status: Unknown}
	if s.Status == QueryError || s.ErrorCode != "" {
		v.Status = QueryError
		return v
	}
	if s.Version != Version || s.FetchedAt.IsZero() || s.FetchedAt.After(now.Add(time.Minute)) || (maxAge > 0 && now.Sub(s.FetchedAt) > maxAge) {
		return v
	}
	if s.ExpiresAt != nil {
		v.ExpiresAt = *s.ExpiresAt
		if !now.Before(*s.ExpiresAt) {
			if s.FetchedAt.After(*s.ExpiresAt) {
				v.Status = Exhausted
			}
			return v
		}
	}
	known, expired := false, false
	for _, w := range s.Windows {
		if w.Informational || !w.Applies(model) {
			continue
		}
		if w.ResetAt != nil && !now.Before(*w.ResetAt) {
			expired = true
			continue
		}
		if w.ExpiresAt != nil {
			if !now.Before(*w.ExpiresAt) {
				expired = true
				continue
			}
			if v.ExpiresAt.IsZero() || w.ExpiresAt.Before(v.ExpiresAt) {
				v.ExpiresAt = *w.ExpiresAt
			}
		}
		if w.UsedPercent != nil && finite(*w.UsedPercent) {
			used := math.Max(0, math.Min(100, *w.UsedPercent))
			if v.UsedPercent == nil || used > *v.UsedPercent {
				v.UsedPercent = &used
			}
			known = true
			if used >= 100 {
				v.Status = Exhausted
			}
			if w.ResetAt != nil {
				v.Renewals = append(v.Renewals, *w.ResetAt)
			}
		}
		if w.Balance != nil && finite(*w.Balance) && *w.Balance >= 0 {
			known = true
			if v.Balance == nil || (v.Unit == w.Unit && *w.Balance < *v.Balance) {
				balance := *w.Balance
				v.Balance, v.Unit = &balance, w.Unit
			}
			if *w.Balance == 0 {
				v.Status = Exhausted
			}
		}
	}
	if v.Status == Exhausted {
		return v
	}
	// Missing or expired applicable windows cannot establish spare quota.
	if expired {
		return View{Status: Unknown}
	}
	if s.ExplicitExhausted {
		v.Status = Exhausted
	} else if known {
		v.Status = Available
	}
	return v
}

func (w Window) Applies(model string) bool {
	if model == "" {
		return len(w.Models) == 0 && len(w.ExcludedModels) == 0
	}
	if len(w.Models) > 0 && !matchesAny(w.Models, model) {
		return false
	}
	return !matchesAny(w.ExcludedModels, model)
}

func matchesAny(patterns []string, model string) bool {
	model = strings.ToLower(model)
	for _, pattern := range patterns {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		if pattern == "" || model == "" {
			continue
		}
		if matched, _ := path.Match(pattern, model); matched {
			return true
		}
		// Providers also report family names such as "opus", rather than IDs.
		if !strings.ContainsAny(pattern, "*?[") && strings.Contains(model, pattern) {
			return true
		}
	}
	return false
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
