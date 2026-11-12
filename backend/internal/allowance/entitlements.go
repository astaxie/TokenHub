package allowance

import (
	"errors"
	"strings"
	"time"
)

type ResetCard struct {
	ID        string     `json:"id,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Window    string     `json:"window,omitempty"`
}

type ResetCredits struct {
	Count     int         `json:"count"`
	Cards     []ResetCard `json:"cards,omitempty"`
	CheckedAt time.Time   `json:"checked_at,omitzero"`
	Unknown   bool        `json:"unknown,omitempty"`
}

type Cap struct {
	Percent float64 `json:"percent,omitempty"`
	Amount  float64 `json:"amount,omitempty"`
}

type ResetPolicy struct {
	OnExhausted  bool  `json:"on_exhausted"`
	BeforeExpiry bool  `json:"before_expiry"`
	LeadSeconds  int64 `json:"lead_seconds,omitempty"`
	WeeklyLimit  int   `json:"weekly_limit,omitempty"`
}

type AlertPolicy struct {
	ChannelIDs               []string `json:"channel_ids,omitempty"`
	Enabled                  bool     `json:"enabled"`
	UsedPercent              float64  `json:"used_percent,omitempty"`
	LowBalance               *float64 `json:"low_balance,omitempty"`
	UnusedBeforeResetSeconds int64    `json:"unused_before_reset_seconds,omitempty"`
}

type Grant struct {
	ID        string     `json:"id"`
	Amount    float64    `json:"amount"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type ManualWallet struct {
	CostPerUSD    float64    `json:"cost_per_usd,omitempty"`
	Enabled       bool       `json:"enabled"`
	Balance       float64    `json:"balance"`
	Unit          string     `json:"unit"`
	AsOf          time.Time  `json:"as_of,omitzero"`
	Grants        []Grant    `json:"grants,omitempty"`
	RefillAmount  float64    `json:"refill_amount,omitempty"`
	RefillSeconds int64      `json:"refill_seconds,omitempty"`
	RefillAt      *time.Time `json:"refill_at,omitempty"`
	CarryOver     bool       `json:"carry_over"`
}

type Policy struct {
	Cap     Cap            `json:"cap"`
	Windows map[string]Cap `json:"windows,omitempty"`
	// Empty preserves the gateway's previous behavior. Spending beyond a
	// reported window is opt-in: allow, never, first, or last.
	Credits string       `json:"credits,omitempty"`
	Reset   ResetPolicy  `json:"reset"`
	Alerts  AlertPolicy  `json:"alerts"`
	Wallet  ManualWallet `json:"wallet"`
}

func (p Policy) Validate() error {
	switch p.Credits {
	case "", "allow", "never", "first", "last":
	default:
		return errors.New("invalid credit policy")
	}
	validCap := func(c Cap) bool {
		return finite(c.Percent) && finite(c.Amount) && c.Percent >= 0 && c.Percent <= 100 && c.Amount >= 0
	}
	if !validCap(p.Cap) || len(p.Windows) > 64 {
		return errors.New("invalid allowance cap")
	}
	for k, c := range p.Windows {
		if strings.TrimSpace(k) == "" || !validCap(c) {
			return errors.New("invalid window cap")
		}
	}
	if p.Reset.LeadSeconds < 0 || p.Reset.LeadSeconds > 86400 || p.Reset.WeeklyLimit < 0 || p.Reset.WeeklyLimit > 100 {
		return errors.New("invalid automatic reset policy")
	}
	if len(p.Alerts.ChannelIDs) > 10 {
		return errors.New("too many quota notification channels")
	}
	if !finite(p.Alerts.UsedPercent) || p.Alerts.UsedPercent < 0 || p.Alerts.UsedPercent > 100 || p.Alerts.UnusedBeforeResetSeconds < 0 || p.Alerts.UnusedBeforeResetSeconds > 86400 || p.Alerts.LowBalance != nil && (!finite(*p.Alerts.LowBalance) || *p.Alerts.LowBalance < 0) {
		return errors.New("invalid allowance alert policy")
	}
	w := p.Wallet
	if !finite(w.CostPerUSD) || w.CostPerUSD < 0 {
		return errors.New("invalid wallet conversion rate")
	}
	if !finite(w.Balance) || w.Balance < 0 || !finite(w.RefillAmount) || w.RefillAmount < 0 || w.RefillSeconds < 0 || w.RefillSeconds > 366*86400 || len(w.Grants) > 64 || w.Enabled && (w.AsOf.IsZero() || w.AsOf.After(time.Now().Add(time.Minute)) || strings.TrimSpace(w.Unit) == "" || w.CostPerUSD == 0 && !strings.EqualFold(w.Unit, "USD")) {
		return errors.New("invalid manual wallet")
	}
	if w.RefillAmount > 0 && (w.RefillSeconds < 3600 || w.RefillAt == nil) {
		return errors.New("wallet refill requires a start and interval of at least one hour")
	}
	seen := map[string]bool{}
	for _, g := range w.Grants {
		if g.ID == "" || seen[g.ID] || !finite(g.Amount) || g.Amount <= 0 {
			return errors.New("invalid wallet grant")
		}
		seen[g.ID] = true
	}
	return nil
}

type Decision struct {
	View       View
	Held       bool
	Reason     string
	OnCredits  bool
	CreditRank int
}

// Evaluate never refreshes upstream state. Caps are user holds and cannot
// authorize an automatic reset or be bypassed by purchased credits.
func Evaluate(s Snapshot, p Policy, model string, now time.Time, maxAge time.Duration) Decision {
	d := Decision{View: s.For(model, now, maxAge)}
	fresh := s.Version == Version && !s.FetchedAt.IsZero() && s.FetchedAt.Before(now.Add(time.Minute)) && (maxAge <= 0 || now.Sub(s.FetchedAt) <= maxAge) && s.Status != QueryError && s.ErrorCode == ""
	if !fresh {
		return d
	}
	for _, w := range s.Windows {
		if !s.WindowApplies(w, model) || w.Unlimited || w.Unknown || w.ResetAt != nil && !now.Before(*w.ResetAt) || w.ExpiresAt != nil && !now.Before(*w.ExpiresAt) {
			continue
		}
		cap, own := p.Windows[strings.ToLower(w.Name)]
		if !own {
			cap = p.Cap
		}
		if w.Informational && !own && w.Pool == "" {
			continue
		}
		amount := w.Amount
		if amount == nil {
			amount = w.Spent
		}
		if cap.Percent > 0 && w.UsedPercent != nil && *w.UsedPercent >= cap.Percent || cap.Amount > 0 && amount != nil && *amount >= cap.Amount {
			d.Held = true
			d.Reason = "usage_cap"
			return d
		}
	}
	if d.View.Status != Exhausted {
		return d
	}
	if p.Credits == "never" {
		d.Held = true
		d.Reason = "credits_disabled"
		return d
	}
	hasCredits := s.Credits != nil && (s.Credits.Unlimited || s.Credits.Available != nil && *s.Credits.Available || s.Credits.Balance != nil && *s.Credits.Balance > 0)
	if !hasCredits || p.Credits == "" {
		return d
	}
	// A wallet limit, expired account, or manual balance cannot be bought past.
	if s.ExpiresAt != nil && !now.Before(*s.ExpiresAt) {
		return d
	}
	for _, w := range s.Windows {
		if s.WindowApplies(w, model) && !w.Informational && w.Balance != nil && *w.Balance <= 0 {
			return d
		}
	}
	d.View.Status = Available
	d.OnCredits = true
	if p.Credits == "first" {
		d.CreditRank = -1
	}
	if p.Credits == "last" {
		d.CreditRank = 1
	}
	return d
}

// ResetCandidate chooses only a fresh, explicitly eligible card. The caller
// must coordinate a durable idempotent operation and enforce the weekly bound.
func ResetCandidate(s Snapshot, p ResetPolicy, now time.Time, noAlternative bool) (ResetCard, string, bool) {
	if !p.OnExhausted && !p.BeforeExpiry || s.Resets == nil || s.Resets.Unknown || s.Resets.Count <= 0 || now.Sub(s.Resets.CheckedAt) > 2*time.Minute || s.Resets.CheckedAt.After(now.Add(time.Minute)) || s.Status == QueryError || now.Sub(s.FetchedAt) > 2*time.Minute {
		return ResetCard{}, "", false
	}
	weekly := false
	usedAny := false
	var back time.Time
	unknownBack := false
	for _, w := range s.Windows {
		if !w.Informational && !w.Unknown && w.UsedPercent != nil && *w.UsedPercent > 0 && (w.ResetAt == nil || now.Before(*w.ResetAt)) {
			usedAny = true
			if *w.UsedPercent >= 100 {
				if w.ResetAt == nil {
					unknownBack = true
				} else if w.ResetAt.After(back) {
					back = *w.ResetAt
				}
			}
		}
	}
	for _, w := range s.Windows {
		if w.Informational || w.Unknown || w.PeriodSeconds < 7*86400 || w.UsedPercent == nil || *w.UsedPercent < 100 || w.ResetAt != nil && !now.Before(*w.ResetAt) {
			continue
		}
		weekly = true
	}
	lead := time.Duration(p.LeadSeconds) * time.Second
	if lead == 0 {
		lead = 30 * time.Minute
	}
	var chosen ResetCard
	reason := ""
	for _, c := range s.Resets.Cards {
		if c.ID == "" || c.ExpiresAt != nil && !now.Before(*c.ExpiresAt) {
			continue
		}
		why := ""
		if p.OnExhausted && weekly && noAlternative {
			why = "weekly_exhausted"
		}
		if p.BeforeExpiry && usedAny && c.ExpiresAt != nil && (c.ExpiresAt.Sub(now) <= lead || (unknownBack || !back.IsZero() && !back.Before(*c.ExpiresAt))) {
			why = "expiring_card"
		}
		if why != "" && (reason == "" || c.ExpiresAt != nil && (chosen.ExpiresAt == nil || c.ExpiresAt.Before(*chosen.ExpiresAt))) {
			chosen = c
			reason = why
		}
	}
	return chosen, reason, reason != ""
}
