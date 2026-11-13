package allowance

import (
	"testing"
	"time"
)

func TestEntitlementCapsAndCreditsRespectModelScope(t *testing.T) {
	now := time.Now().UTC()
	used := 100.0
	balance := 12.0
	s := Snapshot{Version: Version, Status: Available, FetchedAt: now, Credits: &Credits{Balance: &balance}, Windows: []Window{{Name: "Week", UsedPercent: &used, Models: []string{"opus"}}}}
	if d := Evaluate(s, Policy{Credits: "first"}, "claude-opus", now, time.Minute); d.Held || !d.OnCredits || d.CreditRank != -1 || d.View.Status != Available {
		t.Fatalf("credits decision: %+v", d)
	}
	if d := Evaluate(s, Policy{Cap: Cap{Percent: 70}, Credits: "first"}, "claude-opus", now, time.Minute); !d.Held || d.Reason != "usage_cap" {
		t.Fatalf("credits bypassed user cap: %+v", d)
	}
	if d := Evaluate(s, Policy{Credits: "never"}, "claude-sonnet", now, time.Minute); d.Held {
		t.Fatal("model-specific exhaustion held unrelated model")
	}
	s.Status = QueryError
	if d := Evaluate(s, Policy{Credits: "first"}, "claude-opus", now, time.Minute); d.OnCredits || d.View.Status != QueryError {
		t.Fatalf("query failure invented credits: %+v", d)
	}
}

func TestResetCandidateRequiresFreshCardAndActualWeeklyExhaustion(t *testing.T) {
	now := time.Now().UTC()
	used := 100.0
	expires := now.Add(30 * time.Minute)
	s := Snapshot{Version: Version, FetchedAt: now, Windows: []Window{{UsedPercent: &used, PeriodSeconds: 604800}}, Resets: &ResetCredits{Count: 1, CheckedAt: now, Cards: []ResetCard{{ID: "test-card", ExpiresAt: &expires}}}}
	if _, _, ok := ResetCandidate(s, ResetPolicy{}, now, true); ok {
		t.Fatal("automatic reset enabled by default")
	}
	if _, _, ok := ResetCandidate(s, ResetPolicy{OnExhausted: true}, now, false); ok {
		t.Fatal("reset ignored alternative account")
	}
	if c, reason, ok := ResetCandidate(s, ResetPolicy{OnExhausted: true}, now, true); !ok || reason != "weekly_exhausted" || c.ID != "test-card" {
		t.Fatalf("weekly decision: %+v %s %v", c, reason, ok)
	}
	s.Resets.Unknown = true
	if _, _, ok := ResetCandidate(s, ResetPolicy{BeforeExpiry: true}, now, true); ok {
		t.Fatal("unknown details spent card")
	}
}

func TestManualWalletConsumesExpiringGrantBeforeBaseAndRefills(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	expiry := start.Add(2 * time.Hour)
	refill := start.Add(24 * time.Hour)
	w := ManualWallet{Balance: 10, AsOf: start, Grants: []Grant{{ID: "grant", Amount: 5, ExpiresAt: &expiry}}, RefillAmount: 20, RefillSeconds: 86400, RefillAt: &refill}
	spends := []Spend{{At: start.Add(time.Hour), Amount: 7}, {At: start.Add(3 * time.Hour), Amount: 2}}
	if got := WalletBalance(w, spends, start.Add(4*time.Hour)); got != 6 {
		t.Fatalf("grant expiry consumed base twice: %v", got)
	}
	if got := WalletBalance(w, spends, start.Add(25*time.Hour)); got != 20 {
		t.Fatalf("non-carry refill: %v", got)
	}
	w.CarryOver = true
	if got := WalletBalance(w, spends, start.Add(25*time.Hour)); got != 26 {
		t.Fatalf("carry refill: %v", got)
	}
}
