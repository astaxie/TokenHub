package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"tokenhub/backend/internal/accountrouting"
	"tokenhub/backend/internal/allowance"
	"tokenhub/backend/internal/quotasource"
)

func TestMagpieNativeConfigurationImport(t *testing.T) {
	raw := json.RawMessage(`{"providers":[{"id":"other"},{"id":"codex","accountCaps":{"a@example.test":70},"accountWindowCaps":{"a@example.test":{"weekly":100}},"accountWindowAmounts":{"a@example.test":{"daily":25}},"keyOwnBalances":{"a@example.test":{"cny":100,"at":"2026-01-01T00:00:00Z","refill":{"every":"month","day":31},"expirings":[{"amount":20,"until":"2026-12-31","at":"2026-01-02T00:00:00Z"}]}},"keySpendCaps":{"a@example.test":{"usd":10,"period":"month"}},"capStop":true,"capCareful":true}]}`)
	p, err := decodeMagpieQuotaProvider(raw, "codex")
	if err != nil {
		t.Fatal(err)
	}
	s := magpieQuotaSettings{CodexAutoReset: []string{"a@example.test"}, CodexAutoResetOnly: map[string]string{"a@example.test": "expiring"}, CodexCreditsLast: []string{"a@example.test"}, CodexResetLead: 5, UsageAlert: 80, BalanceAlert: 2, ResetReminder: 48}
	policy, err := magpieImportPolicy(p, s, "a@example.test", "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	if policy.Cap.Percent != 70 || policy.Windows["weekly"].Percent != 100 || policy.Windows["daily"].Amount != 25 || policy.Credits != "last" || !policy.CapStop || !policy.CapCareful || policy.Reset.OnExhausted || !policy.Reset.BeforeExpiry || policy.Reset.LeadSeconds != 300 || policy.Alerts.UnusedBeforeResetSeconds != 48*3600 || policy.Wallet.Unit != "CNY" || policy.Wallet.Refill.Day != 31 || policy.Wallet.Grants[0].Until != "2026-12-31" || policy.SpendCap.Period != "month" {
		t.Fatalf("native config changed: %+v", policy)
	}
}

func TestMagpieImportEndpointStagesWholeFilesWithoutMutatingCredentials(t *testing.T) {
	_, store, _, _ := accountRoutingFixture(t, accountrouting.Default)
	s := NewWithConfig(store, Config{AdminToken: "synthetic-admin"})
	t.Cleanup(func() { _ = s.Shutdown(t.Context()) })
	_, err := store.UpdateProviderResource("a", ProviderResource{APIKey: "synthetic-existing-key"})
	if err != nil {
		t.Fatal(err)
	}
	raw := `{"providers":{"providers":[{"id":"relay","key":"ignored-magpie-inference-key","balanceURL":"https://quota.example/balance","balancePath":"$remaining","accountCaps":{"legacy-key":70},"keyOwnBalances":{"legacy-key":{"usd":50,"at":"2026-01-01T00:00:00Z","refill":{"every":"week","day":1}}}}]},"provider_id":"relay","account_ref":"legacy-key","timezone":"Asia/Shanghai","settings":{"usageAlert":80,"resetReminder":48}}`
	query := func(token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/admin/provider-resources/a/allowance/import-magpie", strings.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	if w := query(""); w.Code != 401 {
		t.Fatalf("unauthorized import: %d", w.Code)
	}
	w := query("synthetic-admin")
	var draft providerAllowanceConfig
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &draft) != nil || draft.Policy.Cap.Percent != 70 || draft.Policy.Wallet.Refill.Day != 1 || draft.Policy.Alerts.UnusedBeforeResetSeconds != 48*3600 || strings.Contains(w.Body.String(), "ignored-magpie-inference-key") {
		t.Fatalf("native import: %d %s", w.Code, w.Body)
	}
	r, _ := store.GetProviderResource("a")
	saved, _ := store.providerAllowanceConfig(t.Context(), "a")
	if r.APIKey != "synthetic-existing-key" || saved.Revision != "" {
		t.Fatal("staging import changed saved state")
	}
}

func TestMagpieSpendCapUsesLocalCalendarAndPreservesTotalStart(t *testing.T) {
	_, store, _, _ := accountRoutingFixture(t, accountrouting.Default)
	now := time.Now().UTC()
	if err := store.db.Create(&UsageRecord{ID: NewID("usage"), ProviderResourceID: "a", ProviderCostUSD: 12, CreatedAt: now.Add(-time.Minute)}).Error; err != nil {
		t.Fatal(err)
	}
	cfg, err := store.saveProviderAllowanceConfig(t.Context(), "a", providerAllowanceConfig{Policy: allowance.Policy{SpendCap: &allowance.SpendCap{USD: 10, Period: "month", Timezone: "Asia/Shanghai"}}})
	if err != nil {
		t.Fatal(err)
	}
	runtime, _ := store.providerAllowanceConfig(t.Context(), "a")
	if err := store.refreshManualAllowance(t.Context(), "a", runtime, now); err != nil {
		t.Fatal(err)
	}
	a, err := store.withLocalAllowance(t.Context(), "a", allowance.Snapshot{}, runtime.Policy, now)
	if err != nil || !allowance.Evaluate(a, runtime.Policy, "model", now, time.Minute).Held {
		t.Fatalf("spend cap ignored: %+v %v", a, err)
	}
	cfg.Policy.SpendCap = &allowance.SpendCap{USD: 20, Period: "total"}
	cfg, err = store.saveProviderAllowanceConfig(t.Context(), "a", cfg)
	if err != nil {
		t.Fatal(err)
	}
	start := cfg.Policy.SpendCap.Since
	cfg.Policy.SpendCap.USD = 30
	cfg.Policy.SpendCap.Since = time.Time{}
	cfg, err = store.saveProviderAllowanceConfig(t.Context(), "a", cfg)
	if err != nil || !cfg.Policy.SpendCap.Since.Equal(start) {
		t.Fatal("raising a total cap reset its origin")
	}
}

func TestMagpieLocalCostWindowsUseUTCStorageAtLocalMidnight(t *testing.T) {
	_, store, _, _ := accountRoutingFixture(t, accountrouting.Default)
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Date(2026, 1, 1, 0, 30, 0, 0, loc)
	for _, spend := range []struct {
		at  time.Time
		usd float64
	}{{now.Add(-31 * time.Minute), 8}, {now.Add(-29 * time.Minute), 3}} {
		if err := store.db.Create(&UsageRecord{ID: NewID("usage"), ProviderResourceID: "a", ProviderCostUSD: spend.usd, CreatedAt: spend.at.UTC()}).Error; err != nil {
			t.Fatal(err)
		}
	}
	p := allowance.Policy{
		Wallet:   allowance.ManualWallet{Enabled: true, Balance: 10, Unit: "USD", AsOf: now.Add(-24 * time.Hour), Timezone: "Asia/Shanghai", Refill: &allowance.CalendarRefill{Every: "month", Day: 1}},
		SpendCap: &allowance.SpendCap{USD: 5, Period: "day", Timezone: "Asia/Shanghai"},
	}
	a, err := store.computeLocalAllowance(t.Context(), "a", p, now)
	if err != nil || len(a.Windows) != 2 || a.Windows[0].Balance == nil || *a.Windows[0].Balance != 7 || a.Windows[1].Amount == nil || *a.Windows[1].Amount != 3 {
		t.Fatalf("local midnight used wrong SQL boundary: %+v %v", a, err)
	}
}

func TestMagpieVendorBalanceOverridesFailedManualEstimate(t *testing.T) {
	_, store, _, _ := accountRoutingFixture(t, accountrouting.Default)
	now := time.Now().UTC()
	p := allowance.Policy{Wallet: allowance.ManualWallet{Enabled: true, Balance: -5, Unit: "USD", AsOf: now}}
	_, err := store.saveProviderAllowanceConfig(t.Context(), "a", providerAllowanceConfig{Policy: p})
	if err != nil {
		t.Fatal(err)
	}
	local := manualAllowanceCache{Fingerprint: localAllowanceFingerprint(p), Allowance: allowance.Snapshot{Version: allowance.Version, Status: allowance.QueryError, ErrorCode: "synthetic_compute_failure", Windows: []allowance.Window{{Unknown: true}}}}
	raw, _ := json.Marshal(local)
	if err := store.db.Model(&providerAllowanceSetting{}).Where("resource_id = ?", "a").Update("wallet_snapshot", string(raw)).Error; err != nil {
		t.Fatal(err)
	}
	balance := 10.0
	vendor := allowance.Snapshot{Version: allowance.Version, FetchedAt: now, Status: allowance.Available, Windows: []allowance.Window{{Name: "Wallet", Balance: &balance, Unit: "USD"}}}
	a, err := store.withLocalAllowance(t.Context(), "a", vendor, p, now)
	if err != nil || len(a.Windows) != 1 || a.Status != allowance.Available || a.ErrorCode != "" {
		t.Fatalf("failed fallback overrode vendor money: %+v %v", a, err)
	}
}

func TestMagpieAutomaticBalanceDiscoveryCachesBothProbeOutcomes(t *testing.T) {
	for _, found := range []bool{true, false} {
		t.Run(map[bool]string{true: "found", false: "missing"}[found], func(t *testing.T) {
			s, store, _, _ := accountRoutingFixture(t, accountrouting.Default)
			var calls atomic.Int64
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Header.Get("Authorization") != "Bearer synthetic-key" {
					t.Error("inference key not used for probe")
				}
				if found && r.URL.Path == "/api/usage/token" {
					_, _ = w.Write([]byte(`{"data":{"total_available":250000}}`))
					return
				}
				w.WriteHeader(404)
			}))
			defer upstream.Close()
			provider := store.AddProvider(Provider{ID: NewID("auto"), Type: ProviderOpenAI, BaseURL: upstream.URL + "/v1", APIKey: "synthetic-key", Status: StatusActive})
			r, err := store.AddProviderResource(ProviderResource{ID: NewID("auto"), Name: "Synthetic relay key", ProviderID: provider.ID, ResourceType: ProviderResourceAPIKey, APIKey: "synthetic-key", Status: StatusActive})
			if err != nil {
				t.Fatal(err)
			}
			r, _ = store.GetProviderResource(r.ID)
			cfg := providerAllowanceConfig{Source: quotasource.Config{Enabled: true, Auth: "magpie"}}
			result, err := s.queryConfiguredAllowance(t.Context(), cfg, r, provider, false)
			if (err == nil) != found || calls.Load() != 2 {
				t.Fatalf("probe: calls=%d err=%v", calls.Load(), err)
			}
			if found && (len(result.Parts) != 1 || result.Parts[0].Text != "$0.50" || !strings.HasSuffix(result.QueryURL, "/api/usage/token")) {
				t.Fatalf("detected reading changed: %+v", result)
			}
			_, _ = s.queryConfiguredAllowance(t.Context(), cfg, r, provider, false)
			want := int64(2)
			if found {
				want = 3
			}
			if calls.Load() != want {
				t.Fatalf("probe cache missed: %d", calls.Load())
			}
			cfg.Source.NoBalanceProbe = true
			_, _ = s.queryConfiguredAllowance(t.Context(), cfg, r, provider, false)
			if calls.Load() != want {
				t.Fatal("disabled probes still requested upstream")
			}
		})
	}
}

func TestMagpieStopAtCapReturns429BeforeFallback(t *testing.T) {
	s, store, call, routes := accountRoutingFixture(t, accountrouting.Default)
	for _, id := range []string{"a", "b", "c"} {
		_, err := store.saveProviderAllowanceConfig(t.Context(), id, providerAllowanceConfig{Policy: allowance.Policy{Cap: allowance.Cap{Percent: 70}, CapStop: id == "a"}})
		if err != nil {
			t.Fatal(err)
		}
		saveAccountQuota(t, store, id, `{"windows":[{"name":"Week","used_percent":80}]}`)
	}
	planned := s.planAccountOrder(t.Context(), call, routes)
	if len(planned) != 1 || planned[0].AllowanceHold == nil {
		t.Fatalf("cap stop did not stop plan: %+v", planned)
	}
	calls := 0
	_, _, _, _, err := executeRoutedWithStore(context.Background(), store, RoutedCall{Call: call, Routes: planned}, false, func(context.Context, RouteSelection, bool, int) (struct{}, Usage, error) {
		calls++
		return struct{}{}, Usage{}, nil
	})
	if AsHTTPError(err).Status != 429 || calls != 0 {
		t.Fatalf("cap stop called upstream: %v calls=%d", err, calls)
	}
	affinity := &RequestAffinity{AdapterType: planned[0].Provider.Type, Kind: AffinityKindProviderSession, KeyHash: "synthetic-cap-session"}
	_, _, err = store.CommitAdapterSessionBinding(t.Context(), AdapterSessionBinding{
		AdapterType: affinity.AdapterType, AffinityKind: affinity.Kind,
		ProviderID: planned[0].Provider.ID, ResourceID: "b",
		AffinityKeyHash: providerScopedAffinityKeyHash(affinity.KeyHash, planned[0].Provider.ID),
	}, noBindingGeneration)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, _, err = executeRoutedWithStore(context.Background(), store, RoutedCall{Call: call, Routes: planned, Affinity: affinity}, false, func(context.Context, RouteSelection, bool, int) (struct{}, Usage, error) {
		calls++
		return struct{}{}, Usage{}, nil
	})
	if AsHTTPError(err).Status != 429 || calls != 0 {
		t.Fatalf("session binding bypassed cap stop: %v calls=%d", err, calls)
	}
}

func TestMagpieCarefulCapRefreshesNearLimit(t *testing.T) {
	s, store, call, routes := accountRoutingFixture(t, accountrouting.Default)
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); _, _ = w.Write([]byte(`{"used":0.99}`)) }))
	defer upstream.Close()
	_, err := store.saveProviderAllowanceConfig(t.Context(), "a", providerAllowanceConfig{Source: quotasource.Config{Enabled: true, Auth: "none", URL: upstream.URL, Expression: "Week: used %"}, Policy: allowance.Policy{Cap: allowance.Cap{Percent: 90}, CapCareful: true}})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-20 * time.Second)
	if err := store.SaveProviderResourceQuota("a", ProviderMock, `{"windows":[{"name":"Week","used_percent":86}]}`, at); err != nil {
		t.Fatal(err)
	}
	planned := s.planAccountOrder(t.Context(), call, routes)
	for _, r := range planned {
		if r.Resource.ID == "a" {
			t.Fatal("freshly capped account kept")
		}
	}
	_ = s.planAccountOrder(t.Context(), call, routes)
	if calls.Load() != 1 {
		o, _ := store.GetProviderResourceObservation("a")
		r, _ := store.GetProviderResource("a")
		result, handled, refreshErr := s.refreshConfiguredAllowance(t.Context(), r, true)
		t.Fatalf("careful read did not respect freshness: %d, snapshot=%s, refresh=%v handled=%v result=%+v", calls.Load(), o.QuotaSnapshot, refreshErr, handled, result)
	}
}

func TestMagpieAlertMarksTolerateResetDriftAndRemindExpiringCards(t *testing.T) {
	_, store, _, _ := accountRoutingFixture(t, accountrouting.Default)
	resource, _ := store.GetProviderResource("a")
	now := time.Now().UTC()
	used := 95.0
	reset := now.Add(2 * time.Hour)
	expiry := now.Add(30 * time.Minute)
	a := allowance.Snapshot{Version: allowance.Version, FetchedAt: now, Windows: []allowance.Window{{Name: "Week", PeriodSeconds: 7 * 86400, UsedPercent: &used, ResetAt: &reset}}, Resets: &allowance.ResetCredits{Count: 1, CheckedAt: now, Cards: []allowance.ResetCard{{ID: "synthetic-card", ExpiresAt: &expiry}}}}
	p := allowance.AlertPolicy{Enabled: true, UsedPercent: 90, UnusedBeforeResetSeconds: 3600}
	if err := store.recordAllowanceAlerts(t.Context(), resource, a, p, now); err != nil {
		t.Fatal(err)
	}
	reset = reset.Add(time.Minute)
	if err := store.recordAllowanceAlerts(t.Context(), resource, a, p, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(store.ListAlerts()) != 2 {
		t.Fatalf("reset drift repeated alerts or card reminder missing: %d", len(store.ListAlerts()))
	}
	used = 10
	if err := store.recordAllowanceAlerts(t.Context(), resource, a, p, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	used = 95
	if err := store.recordAllowanceAlerts(t.Context(), resource, a, p, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(store.ListAlerts()) != 3 {
		t.Fatal("usage recovery did not rearm alert")
	}
}
