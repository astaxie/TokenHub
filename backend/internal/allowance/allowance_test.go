package allowance

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNormalizeRetainsSubscriptionWindowsAndModelScope(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	s := Normalize([]byte(`{"until":"2026-12-01T00:00:00Z","windows":[{"name":"Week","used":20,"span":604800,"resetsAt":"2026-11-19T12:00:00Z"},{"name":"Opus","used":100,"models":["opus"]},{"name":"Wallet","balance":50,"unit":"points","runsOutAt":"2026-11-14T00:00:00Z"},{"name":"Display","used":100,"aside":true}]}`), now)
	if s.Version != Version || s.Status != Available || s.Windows[0].PeriodSeconds != 604800 || s.ExpiresAt == nil {
		t.Fatalf("incomplete normalized quota: %+v", s)
	}
	if got := s.For("claude-opus-5-5", now, time.Hour); got.Status != Exhausted {
		t.Fatalf("model-specific exhaustion ignored: %+v", got)
	}
	if got := s.For("claude-sonnet-5-5", now, time.Hour); got.Status != Available || got.Balance == nil || *got.Balance != 50 || got.Unit != "points" {
		t.Fatalf("unrelated model or wallet damaged: %+v", got)
	}
	encoded, err := json.Marshal(map[string]any{"allowance": s})
	if err != nil {
		t.Fatal(err)
	}
	if got := Read(encoded, now.Add(time.Hour)); !got.FetchedAt.Equal(now) {
		t.Fatal("cache read renewed the observation age")
	}
}

func TestUnknownErrorAndExhaustedRemainDistinct(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name, raw, model string
		want             Status
	}{
		{"no share", `{"windows":[{"display":"Balance unavailable"}]}`, "model", Unknown},
		{"zero is known", `{"windows":[{"used":0}]}`, "model", Available},
		{"empty wallet", `{"windows":[{"balance":0,"unit":"USD"}]}`, "model", Exhausted},
		{"text balance", `{"balance":"25 credits"}`, "model", Unknown},
		{"excluded", `{"windows":[{"used":100,"notModels":["sonnet"]}]}`, "claude-sonnet", Unknown},
		{"explicit exhausted", `{"allowed":false}`, "model", Exhausted},
		{"negative reading", `{"windows":[{"used":-10}]}`, "model", Unknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := Normalize([]byte(tc.raw), now)
			if got := s.For(tc.model, now, time.Hour).Status; got != tc.want {
				t.Fatalf("status=%s want=%s", got, tc.want)
			}
		})
	}
	s := Normalize([]byte(`{"used_percent":25}`), now)
	s.Status, s.ErrorCode = QueryError, "provider_quota_unavailable"
	if got := s.For("model", now, time.Hour).Status; got != QueryError {
		t.Fatalf("failed query treated as %s", got)
	}
}

func TestExpiredObservationsNeverInventNewQuota(t *testing.T) {
	now := time.Now().UTC()
	s := Normalize([]byte(`{"rate_limit":{"primary_window":{"used_percent":100,"reset_after_seconds":30}}}`), now)
	if s.For("model", now.Add(time.Minute), time.Hour).Status != Unknown {
		t.Fatal("passed reset was treated as a new known window")
	}
	if s.For("model", now.Add(2*time.Hour), time.Hour).Status != Unknown {
		t.Fatal("stale quota was trusted")
	}
	if s.For("model", now.Add(-time.Hour), time.Hour).Status != Unknown {
		t.Fatal("future observation was trusted")
	}
}

func TestNativeCreditsAndUnknownModelLimitsArePreserved(t *testing.T) {
	now := time.Now().UTC()
	s := Normalize([]byte(`{"credits":{"has_credits":true,"balance":"58875.09","unit":"credits"},"rate_limit":{"primary_window":{"used_percent":1,"limit_window_seconds":604800}},"additional_rate_limits":[{"limit_name":"Vendor-specific pool","rate_limit":{"primary_window":{"used_percent":100}}}]}`), now)
	if s.Credits == nil || s.Credits.Balance == nil || *s.Credits.Balance != 58875.09 || s.Credits.Available == nil || !*s.Credits.Available {
		t.Fatalf("credits lost: %+v", s.Credits)
	}
	if s.For("gpt-model", now, time.Hour).Status != Available {
		t.Fatal("unscoped additional limit exhausted the whole account")
	}
}

func TestNormalizePartiallyUnreadableNativeWindows(t *testing.T) {
	now := time.Now().UTC()
	for _, missing := range []string{``, `"used_percent":null,`, `"used_percent":"invalid",`} {
		for _, prefix := range []string{"", "rate_limit"} {
			t.Run(prefix+missing, func(t *testing.T) {
				raw := `{"primary_window":{` + missing + `"limit_window_seconds":18000,"reset_after_seconds":3600},"secondary_window":{"used_percent":20,"reset_after_seconds":604800}}`
				if prefix != "" {
					raw = `{"rate_limit":` + raw + `}`
				}
				s := Normalize([]byte(raw), now)
				if got := s.For("synthetic-model", now, time.Hour).Status; got != Unknown {
					t.Fatalf("unreadable required window became %s", got)
				}
				if len(s.Windows) != 2 || !s.Windows[0].Unknown {
					t.Fatalf("missing window did not retain unknown state: %+v", s.Windows)
				}
			})
		}
	}
	for _, raw := range []string{
		`{"windows":[{"unlimited":true},{"used_percent":20}]}`,
		`{"windows":[{"informational":true},{"used_percent":20}]}`,
		`{"windows":[{"balance":10},{"used_percent":20}]}`,
		`{"windows":[{"amount":2,"limit":10},{"used_percent":20}]}`,
	} {
		if got := Normalize([]byte(raw), now).For("synthetic-model", now, time.Hour).Status; got != Available {
			t.Fatalf("known or informational window became %s: %s", got, raw)
		}
	}
}
