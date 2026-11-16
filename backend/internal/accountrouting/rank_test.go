package accountrouting

import (
	"reflect"
	"testing"
	"time"

	"tokenhub/backend/internal/allowance"
)

func TestStrategiesChooseTheirOwnAccountSignal(t *testing.T) {
	now := time.Now().UTC()
	seats := []Candidate{
		{ID: "a", RecentTokens: 100, Quota: allowance.View{Status: allowance.Available, Renewals: []time.Time{now.Add(7 * 24 * time.Hour), now.Add(time.Hour)}}},
		{ID: "b", RecentTokens: 10, Quota: allowance.View{Status: allowance.Available, Renewals: []time.Time{now.Add(24 * time.Hour), now.Add(5 * time.Hour)}}},
		{ID: "c", RecentTokens: 0, Quota: allowance.View{Status: allowance.Unknown}},
		{ID: "d", Quota: allowance.View{Status: allowance.Exhausted}},
	}
	for _, tc := range []struct {
		strategy Strategy
		first    int
	}{{Smart, 1}, {Expiry, 0}, {LeastUsed, 1}} {
		if order := Order(tc.strategy, seats, State{}); order[0] != tc.first || order[3] != 3 {
			t.Fatalf("%s order=%v", tc.strategy, order)
		}
	}
	if order := Order(Drain, seats, State{Current: "a"}); order[0] != 0 {
		t.Fatalf("drain moved before exhaustion: %v", order)
	}
	if order := Order(Drain, seats, State{Current: "a", Past: true}); order[0] != 1 {
		t.Fatalf("drain did not advance: %v", order)
	}
	seats[0].Quota.Status = allowance.Exhausted
	if order := Order(Drain, seats, State{Current: "a"}); order[0] != 1 {
		t.Fatalf("fresh exhaustion was ignored: %v", order)
	}
	seats[0].Quota.Status = allowance.QueryError
	if order := Order(Drain, seats, State{Current: "a"}); order[0] != 0 {
		t.Fatalf("a failed query advanced drain: %v", order)
	}
}

func TestUnknownQuotaDoesNotAppearUnusedAndDefaultPreservesOrder(t *testing.T) {
	seats := []Candidate{{ID: "unknown", Quota: allowance.View{Status: allowance.Unknown}}, {ID: "failed", Quota: allowance.View{Status: allowance.QueryError}}, {ID: "known", RecentTokens: 1000, Quota: allowance.View{Status: allowance.Available}}}
	if got := Order(LeastUsed, seats, State{}); !reflect.DeepEqual(got, []int{2, 0, 1}) {
		t.Fatalf("unknown quota preferred: %v", got)
	}
	if got := Order(Default, seats, State{}); !reflect.DeepEqual(got, []int{0, 1, 2}) {
		t.Fatalf("default changed: %v", got)
	}
}

func TestWalletUnitsAreNeverCompared(t *testing.T) {
	x, y, z := 10.0, 10000.0, 20.0
	seats := []Candidate{
		{ID: "a", Quota: allowance.View{Status: allowance.Available, Balance: &x, Unit: "USD"}},
		{ID: "b", Quota: allowance.View{Status: allowance.Available, Balance: &y, Unit: "points"}},
		{ID: "c", Quota: allowance.View{Status: allowance.Available, Balance: &z, Unit: "USD"}},
	}
	if got := Order(Smart, seats, State{}); !reflect.DeepEqual(got, []int{2, 1, 0}) {
		t.Fatalf("wallet units mixed: %v", got)
	}
}
