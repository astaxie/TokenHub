package quotasource

import (
	"encoding/json"
	"math"
	"time"

	"tokenhub/backend/internal/allowance"
)

func parseSub2API(body []byte, c Config, now time.Time) (Result, error) {
	var r struct {
		RateLimits []struct {
			Window string   `json:"window"`
			Limit  *float64 `json:"limit"`
			Used   *float64 `json:"used"`
			Reset  any      `json:"reset_at"`
		} `json:"rate_limits"`
	}
	_ = json.Unmarshal(body, &r)
	// quota_limited mode need not include a wallet balance at all.
	if _, err := ReadNumber(body, "remaining"); err != nil {
		c.Expression = ""
	}
	out, err := Parse(body, c, now)
	if err != nil && len(r.RateLimits) > 0 {
		out = Result{Allowance: allowance.Snapshot{Version: allowance.Version, Source: "sub2api", FetchedAt: now, CheckedAt: now}}
	}
	for _, limit := range r.RateLimits {
		spans := map[string]int64{"5h": 18000, "1d": 86400, "7d": 604800}
		span, known := spans[limit.Window]
		if !known || limit.Limit == nil || *limit.Limit <= 0 {
			continue
		}
		w := allowance.Window{Name: limit.Window, Amount: limit.Used, Limit: limit.Limit, Unit: "USD", PeriodSeconds: span, ResetAt: Time(limit.Reset), Unknown: limit.Used == nil}
		if limit.Used != nil {
			percent := *limit.Used / *limit.Limit * 100
			w.UsedPercent = &percent
		}
		out.Allowance.Windows = append(out.Allowance.Windows, w)
	}
	if len(out.Allowance.Windows) > 0 {
		out.Allowance.Status = out.Allowance.For("", now, 0).Status
		return out, nil
	}
	return out, err
}

func parseDeepSeek(body []byte, c Config, now time.Time) (Result, error) {
	var r struct {
		Available *bool `json:"is_available"`
		Balances  []struct {
			Currency string `json:"currency"`
			Total    any    `json:"total_balance"`
			Granted  any    `json:"granted_balance"`
			Topped   any    `json:"topped_up_balance"`
		} `json:"balance_infos"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return Result{}, err
	}
	out := Result{Allowance: allowance.Snapshot{Version: allowance.Version, Source: "deepseek", FetchedAt: now, CheckedAt: now}}
	active := -1
	for _, balance := range r.Balances {
		value, ok := number(balance.Total)
		if !ok || math.IsNaN(value) || math.IsInf(value, 0) {
			continue
		}
		w := allowance.Window{Name: balance.Currency, Balance: &value, Unit: balance.Currency, Informational: true}
		out.Allowance.Windows = append(out.Allowance.Windows, w)
		if active < 0 || value > 0 {
			active = len(out.Allowance.Windows) - 1
		}
	}
	if active < 0 {
		return Parse(body, c, now)
	}
	// Currency wallets are alternatives; a zero in an unused currency does not
	// exhaust another wallet with money. Do not add different currencies.
	out.Allowance.Windows[active].Informational = false
	if r.Available != nil && !*r.Available {
		out.Allowance.ExplicitExhausted = true
	}
	out.Allowance.Status = out.Allowance.For("", now, 0).Status
	return out, nil
}
