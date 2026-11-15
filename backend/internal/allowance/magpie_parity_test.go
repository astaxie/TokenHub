package allowance

import (
	"testing"
	"time"
)

func TestMagpieCalendarAndExpiringBalances(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	at := func(day, hour int) time.Time { return time.Date(2026, 1, day, hour, 0, 0, 0, loc) }
	w := ManualWallet{Enabled: true, Balance: 10, Unit: "USD", AsOf: at(1, 0), Timezone: "Asia/Shanghai", Grants: []Grant{{ID: "grant", Amount: 5, At: at(1, 12), Until: "2026-01-01"}}}
	spends := []Spend{{At: at(1, 8), Amount: 6}, {At: at(1, 15), Amount: 3}}
	if got := WalletBalance(w, spends, at(1, 21)); got != 6 {
		t.Fatalf("grant activation changed: %v", got)
	}
	if got := WalletBalance(w, spends, at(2, 1)); got != 4 {
		t.Fatalf("inclusive expiry changed: %v", got)
	}
	w.Grants = nil
	if got := WalletBalance(w, []Spend{{At: at(1, 8), Amount: 12}}, at(2, 1)); got != -2 {
		t.Fatalf("negative balance clamped: %v", got)
	}
	r := CalendarRefill{Every: "month", Day: 31}
	now := time.Date(2026, 2, 28, 9, 0, 0, 0, loc)
	if r.Last(now).Day() != 28 || r.Next(now).Day() != 31 || r.Next(now).Month() != time.March {
		t.Fatal("month-end refill drifted")
	}
	w.Refill = &r
	if got := WalletBalance(w, []Spend{{At: at(2, 8), Amount: 9}}, now); got != 10 {
		t.Fatalf("calendar refill did not restore balance: %v", got)
	}
	ny, _ := time.LoadLocation("America/New_York")
	spring := time.Date(2026, 3, 8, 12, 0, 0, 0, ny)
	weekly := CalendarRefill{Every: "week", Day: 7}
	if last := weekly.Last(spring); last.Hour() != 0 || last.Weekday() != time.Sunday {
		t.Fatalf("weekly refill ignored local midnight: %v", last)
	}
}

func TestMagpieCapsAndDefaultCredits(t *testing.T) {
	now := time.Now().UTC()
	used, balance, amount := 90.0, 10.0, 9.0
	s := Snapshot{Version: Version, FetchedAt: now, Credits: &Credits{Balance: &balance}, Windows: []Window{{Name: " Week ", UsedPercent: &used}}}
	p := Policy{Cap: Cap{Percent: 70}, Windows: map[string]Cap{"week": {Percent: 100}}}
	if Evaluate(s, p, "model", now, time.Minute).Held {
		t.Fatal("100% window override inherited account cap")
	}
	s.Windows[0].Informational = true
	s.Windows[0].Pool = "pool"
	p.Windows = nil
	if Evaluate(s, p, "model", now, time.Minute).Held {
		t.Fatal("account cap applied to aside pool")
	}
	s.Windows[0].Informational = false
	s.Windows[0].Pool = ""
	s.Windows[0].Amount = &amount
	p.Windows = map[string]Cap{"week": {Amount: 10}}
	if !Evaluate(s, p, "model", now, time.Minute).Held {
		t.Fatal("amount without a total did not inherit account share")
	}
	used = 100
	p = Policy{}
	if d := Evaluate(s, p, "model", now, time.Minute); !d.OnCredits || d.View.Status != Available {
		t.Fatal("default credit behavior diverged from Magpie")
	}
	s.Windows[0].LocalLimit = true
	if d := Evaluate(s, p, "model", now, time.Minute); !d.Held || d.OnCredits {
		t.Fatal("credits bypassed spend cap")
	}
}

func TestMagpieCarefulThresholds(t *testing.T) {
	now := time.Now().UTC()
	used := 66.0
	p := Policy{Cap: Cap{Percent: 70}, CapCareful: true}
	s := Snapshot{Version: Version, FetchedAt: now.Add(-16 * time.Second), Windows: []Window{{Name: "Week", UsedPercent: &used}}}
	if !CarefulNeedsRefresh(s, p, "model", now) {
		t.Fatal("near-cap read was not refreshed after 15 seconds")
	}
	s.FetchedAt = now.Add(-14 * time.Second)
	if CarefulNeedsRefresh(s, p, "model", now) {
		t.Fatal("fresh read queried again")
	}
	used = 10
	s.FetchedAt = now.Add(-6 * time.Minute)
	if !CarefulNeedsRefresh(s, p, "model", now) {
		t.Fatal("five-minute old read was accepted")
	}
	p.Cap = Cap{Amount: 10}
	if !Evaluate(Snapshot{}, p, "model", now, time.Minute).Held {
		t.Fatal("careful amount cap allowed unknown quota")
	}
	p.Cap = Cap{}
	p.Windows = map[string]Cap{"week": {Percent: 100}}
	if Evaluate(Snapshot{}, p, "model", now, time.Minute).Held || CarefulNeedsRefresh(Snapshot{}, p, "model", now) {
		t.Fatal("100% override created a careful hold without any cap")
	}
}
