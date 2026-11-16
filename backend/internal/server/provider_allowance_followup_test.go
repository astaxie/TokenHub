package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"tokenhub/backend/internal/accountrouting"
	"tokenhub/backend/internal/allowance"
	"tokenhub/backend/internal/quotasource"
)

func TestLocalSpendCapDoesNotDependOnVendorFreshness(t *testing.T) {
	for _, vendorState := range []string{"healthy", "failed", "stale"} {
		for _, localState := range []string{"fresh", "failed", "stale"} {
			t.Run(vendorState+"/"+localState, func(t *testing.T) {
				s, store, call, routes := accountRoutingFixture(t, accountrouting.Default)
				cfg, err := store.saveProviderAllowanceConfig(t.Context(), "a", providerAllowanceConfig{Policy: allowance.Policy{CapStop: true, SpendCap: &allowance.SpendCap{USD: 10, Period: "total"}}})
				if err != nil {
					t.Fatal(err)
				}
				now := time.Now().UTC()
				if err := store.db.Create(&UsageRecord{ID: NewID("usage"), ProviderResourceID: "a", ProviderCostUSD: 12, CreatedAt: now}).Error; err != nil {
					t.Fatal(err)
				}
				if err := store.refreshManualAllowance(t.Context(), "a", cfg, now); err != nil {
					t.Fatal(err)
				}
				if localState != "fresh" {
					var row providerAllowanceSetting
					if err := store.db.First(&row, "resource_id = ?", "a").Error; err != nil {
						t.Fatal(err)
					}
					var cache manualAllowanceCache
					if err := json.Unmarshal([]byte(row.WalletSnapshot), &cache); err != nil {
						t.Fatal(err)
					}
					if localState == "failed" {
						cache.Allowance.Status, cache.Allowance.ErrorCode = allowance.QueryError, "manual_wallet_compute_failed"
					} else {
						cache.Allowance.FetchedAt = now.Add(-11 * time.Minute)
					}
					raw, _ := json.Marshal(cache)
					if err := store.db.Model(&row).Update("wallet_snapshot", string(raw)).Error; err != nil {
						t.Fatal(err)
					}
				}
				used := 20.0
				vendor := allowance.Snapshot{Version: allowance.Version, FetchedAt: now, Windows: []allowance.Window{{Name: "Week", UsedPercent: &used}}}
				if vendorState == "failed" {
					vendor.Status, vendor.ErrorCode = allowance.QueryError, "allowance_query_failed"
				}
				if vendorState == "stale" {
					vendor.FetchedAt = now.Add(-11 * time.Minute)
				}
				raw, _ := json.Marshal(map[string]any{"allowance": vendor})
				if err := store.SaveProviderResourceQuota("a", ProviderMock, string(raw), vendor.FetchedAt); err != nil {
					t.Fatal(err)
				}
				for _, route := range routes {
					if route.Resource.ID != "a" {
						continue
					}
					planned := s.planAccountOrder(t.Context(), call, []RouteSelection{route})
					held := len(planned) == 1 && planned[0].AllowanceHold != nil
					if held != (localState == "fresh") {
						t.Fatalf("local spend cap: vendor=%s local=%s held=%v", vendorState, localState, held)
					}
					return
				}
				t.Fatal("fixture account missing")
			})
		}
	}
}

func TestCNYWalletUncertaintyOnlyIncludesSurvivingCosts(t *testing.T) {
	for _, tc := range []struct {
		name        string
		grant       *allowance.Grant
		currentCost bool
		unknown     bool
	}{
		{name: "discarded prior period"},
		{name: "expired grant", grant: &allowance.Grant{Amount: 20, Until: "2026-10-31"}},
		{name: "surviving grant", grant: &allowance.Grant{Amount: 20, Until: "2026-12-31"}, unknown: true},
		{name: "later grant", grant: &allowance.Grant{Amount: 20, At: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC), Until: "2026-12-31"}},
		{name: "current period", currentCost: true, unknown: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, store, _, _ := accountRoutingFixture(t, accountrouting.Default)
			now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
			wallet := allowance.ManualWallet{Enabled: true, Balance: 100, Unit: "CNY", AsOf: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Timezone: "UTC", Refill: &allowance.CalendarRefill{Every: "month", Day: 1}}
			if tc.grant != nil {
				wallet.Grants = []allowance.Grant{*tc.grant}
			}
			at := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
			if tc.currentCost {
				at = now.Add(-time.Hour)
			}
			if err := store.db.Create(&UsageRecord{ID: NewID("usage"), ProviderResourceID: "a", ProviderCostUSD: 1, CreatedAt: at}).Error; err != nil {
				t.Fatal(err)
			}
			a, err := store.computeManualAllowance(t.Context(), "a", allowance.Snapshot{}, wallet, now)
			if err != nil || len(a.Windows) != 1 {
				t.Fatalf("wallet: %+v %v", a, err)
			}
			window := a.Windows[0]
			if window.Unknown != tc.unknown {
				t.Fatalf("irrelevant spending affected uncertainty: %+v", window)
			}
			if !tc.unknown {
				want := 100.0
				if tc.name == "later grant" {
					want += 20
				}
				if window.Balance == nil || *window.Balance != want {
					t.Fatalf("restored balance: %+v, want %v", window, want)
				}
			}
		})
	}
}

func TestAllowanceHeadersHonorCaseInsensitivePrecedence(t *testing.T) {
	_, store, _, _ := accountRoutingFixture(t, accountrouting.Default)
	s := New(store)
	t.Cleanup(func() { _ = s.Shutdown(t.Context()) })
	var wrong atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Quota-Secret") != "synthetic-source" {
			wrong.Add(1)
		}
		_, _ = w.Write([]byte(`{"balance":10}`))
	}))
	defer upstream.Close()
	provider := Provider{Headers: map[string]string{"X-Quota-Secret": "synthetic-provider"}}
	resource := ProviderResource{Headers: map[string]string{"X-QUOTA-SECRET": "synthetic-resource"}}
	cfg := providerAllowanceConfig{Source: quotasource.Config{Enabled: true, Auth: "magpie", URL: upstream.URL, Expression: "$balance", Headers: map[string]string{"x-quota-secret": "synthetic-source"}}}
	for range 128 {
		if _, err := s.queryConfiguredAllowance(t.Context(), cfg, resource, provider, true); err != nil {
			t.Fatal(err)
		}
	}
	if wrong.Load() != 0 {
		t.Fatalf("quota credentials overwritten on %d requests", wrong.Load())
	}
}

func TestDailyUnusedQuotaReminderPreservesNativeThresholds(t *testing.T) {
	for _, tc := range []struct {
		name    string
		seconds int64
		used    float64
		want    int
	}{
		{"daily unused", 86400, 20, 1}, {"weekly unused", 7 * 86400, 84.9, 1}, {"usage cutoff", 7 * 86400, 85, 0}, {"short window", 86399, 20, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, store, _, _ := accountRoutingFixture(t, accountrouting.Default)
			resource, _ := store.GetProviderResource("a")
			now := time.Now().UTC()
			reset := now.Add(30 * time.Minute)
			a := allowance.Snapshot{Version: allowance.Version, FetchedAt: now, Windows: []allowance.Window{{Name: "Plan", PeriodSeconds: tc.seconds, UsedPercent: &tc.used, ResetAt: &reset}}}
			p := allowance.AlertPolicy{Enabled: true, UnusedBeforeResetSeconds: 3600}
			for range 2 {
				if err := store.recordAllowanceAlerts(t.Context(), resource, a, p, now); err != nil {
					t.Fatal(err)
				}
			}
			if got := len(store.ListAlerts()); got != tc.want {
				t.Fatalf("unused-quota reminders=%d, want %d", got, tc.want)
			}
		})
	}
}
