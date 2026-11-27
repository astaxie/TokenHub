package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"tokenhub/backend/internal/accountrouting"
	"tokenhub/backend/internal/allowance"
	"tokenhub/backend/internal/quotasource"
)

func TestConfiguredAllowanceCollectsFusecodeAndKeepsSecretsEncrypted(t *testing.T) {
	server, store, _, _ := accountRoutingFixture(t, accountrouting.Default)
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer synthetic-quota-secret" || r.Header.Get("X-Test") != "synthetic-header-secret" {
			t.Error("quota authentication missing")
		}
		_, _ = w.Write([]byte(`{"remaining":12.5,"subscription":{"weekly_usage_usd":5,"weekly_limit_usd":100,"monthly_usage_usd":10,"monthly_limit_usd":200}}`))
	}))
	defer upstream.Close()
	cfg, err := store.saveProviderAllowanceConfig(t.Context(), "a", providerAllowanceConfig{Source: quotasource.Config{Enabled: true, Preset: "fusecode", URL: upstream.URL, Headers: map[string]string{"X-Test": "synthetic-header-secret"}}, Token: "synthetic-quota-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "" || cfg.Source.Headers["X-Test"] != "[redacted]" {
		t.Fatal("configuration exposed secret")
	}
	var row providerAllowanceSetting
	if err := store.db.First(&row, "resource_id = ?", "a").Error; err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(row)
	if strings.Contains(string(raw), "synthetic-") {
		t.Fatal("quota credentials persisted in plaintext")
	}
	resource, _ := store.GetProviderResource("a")
	result, handled, err := server.refreshConfiguredAllowance(t.Context(), resource, false)
	if err != nil || !handled {
		t.Fatalf("refresh: %v %v", handled, err)
	}
	if result.Allowance.For("model", time.Now(), accountAllowanceMaxAge).Status != allowance.Available {
		t.Fatalf("bad projection: %+v", result)
	}
	if _, _, err := server.refreshConfiguredAllowance(t.Context(), resource, false); err != nil || calls.Load() != 1 {
		t.Fatalf("cache bypassed: %v calls=%d", err, calls.Load())
	}
	points, err := store.providerAllowanceHistory(t.Context(), "a", time.Now().Add(-time.Hour), 100)
	if err != nil || len(points) != 1 {
		t.Fatalf("history: %d %v", len(points), err)
	}
	if _, err := store.saveProviderAllowanceConfig(t.Context(), "a", providerAllowanceConfig{}); AsHTTPError(err).Status != http.StatusConflict {
		t.Fatalf("stale configuration overwritten: %v", err)
	}
}

func TestAllowancePolicyHoldsApplyToDefaultRouting(t *testing.T) {
	server, store, call, routes := accountRoutingFixture(t, accountrouting.Default)
	_, err := store.saveProviderAllowanceConfig(t.Context(), "a", providerAllowanceConfig{Policy: allowance.Policy{Cap: allowance.Cap{Percent: 70}}})
	if err != nil {
		t.Fatal(err)
	}
	saveAccountQuota(t, store, "a", `{"windows":[{"name":"Week","used_percent":80}]}`)
	planned := server.planAccountOrder(context.Background(), call, routes)
	for _, route := range planned {
		if route.Resource.ID == "a" {
			t.Fatal("default strategy bypassed user cap")
		}
	}
	if len(planned) != len(routes)-1 {
		t.Fatalf("unexpected candidate count: %d", len(planned))
	}
}

func TestAllowanceHistoryDeduplicatesAndAlertRearms(t *testing.T) {
	_, store, _, _ := accountRoutingFixture(t, accountrouting.Default)
	now := time.Now().UTC()
	used := 95.0
	reset := now.Add(time.Hour)
	a := allowance.Snapshot{Version: allowance.Version, Status: allowance.Available, FetchedAt: now, Windows: []allowance.Window{{Name: "Week", UsedPercent: &used, ResetAt: &reset}}}
	encode := func() string { b, _ := json.Marshal(map[string]any{"allowance": a}); return string(b) }
	if err := store.SaveProviderResourceQuota("a", ProviderMock, encode(), now); err != nil {
		t.Fatal(err)
	}
	a.FetchedAt = now.Add(time.Second)
	if err := store.SaveProviderResourceQuota("a", ProviderMock, encode(), now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	points, _ := store.providerAllowanceHistory(t.Context(), "a", now.Add(-time.Hour), 100)
	if len(points) != 1 {
		t.Fatalf("unchanged reading duplicated: %d", len(points))
	}
	resource, _ := store.GetProviderResource("a")
	policy := allowance.AlertPolicy{Enabled: true, UsedPercent: 90}
	for i := 0; i < 2; i++ {
		if err := store.recordAllowanceAlerts(t.Context(), resource, a, policy, now); err != nil {
			t.Fatal(err)
		}
	}
	if len(store.ListAlerts()) != 1 {
		t.Fatal("alert repeated without rearming")
	}
	used = 20
	if err := store.recordAllowanceAlerts(t.Context(), resource, a, policy, now); err != nil {
		t.Fatal(err)
	}
	used = 95
	if err := store.recordAllowanceAlerts(t.Context(), resource, a, policy, now); err != nil {
		t.Fatal(err)
	}
	if len(store.ListAlerts()) != 2 {
		t.Fatal("alert did not rearm after top-up or recovery")
	}
}

func TestWalletCountersIgnoreTopUpsLateReadingsAndHistoryPruning(t *testing.T) {
	_, store, _, _ := accountRoutingFixture(t, accountrouting.Default)
	now := time.Now().UTC()
	base := now.Add(-time.Hour)
	save := func(balance float64, at time.Time) {
		t.Helper()
		raw := fmt.Sprintf(`{"windows":[{"name":"Wallet","balance":%v,"unit":"USD"}]}`, balance)
		if err := store.SaveProviderResourceQuota("a", ProviderMock, raw, at); err != nil {
			t.Fatal(err)
		}
	}
	save(100, base)
	save(80, base.Add(time.Minute))
	save(150, base.Add(2*time.Minute))
	save(140, base.Add(3*time.Minute))
	save(500, base.Add(time.Second))
	if err := store.db.Where("resource_id = ?", "a").Delete(&providerAllowancePoint{}).Error; err != nil {
		t.Fatal(err)
	}
	var counters []providerAllowanceWalletCounter
	if err := store.db.Where("resource_id = ?", "a").Find(&counters).Error; err != nil {
		t.Fatal(err)
	}
	if len(counters) != 1 || counters[0].Spent != 30 || counters[0].PreviousBalance != 140 {
		t.Fatalf("wallet counter changed: %+v", counters)
	}
}

func TestSharedWalletIsPinnedToAccountIdentity(t *testing.T) {
	_, store, _, _ := accountRoutingFixture(t, accountrouting.Default)
	cfg, err := store.saveProviderAllowanceConfig(t.Context(), "b", providerAllowanceConfig{Source: quotasource.Config{SharedAccountID: "a"}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	saveAccountQuota(t, store, "a", `{"windows":[{"name":"Wallet","balance":50,"unit":"USD"}]}`)
	a, err := store.withWalletObservations(t.Context(), "b", allowance.Snapshot{Version: allowance.Version}, cfg.Source, now)
	if err != nil || len(a.Windows) != 1 || *a.Windows[0].Balance != 50 {
		t.Fatalf("shared wallet: %+v %v", a, err)
	}
	if err := store.DeleteProviderResource("a"); err != nil {
		t.Fatal(err)
	}
	a, err = store.withWalletObservations(t.Context(), "b", a, cfg.Source, now)
	if err != nil || a.ErrorCode != "shared_wallet_account_changed" {
		t.Fatalf("deleted shared account reused: %+v %v", a, err)
	}
}

func TestAllowanceHTTPReadsCacheAndRejectsUnauthorizedWrites(t *testing.T) {
	_, store, _, _ := accountRoutingFixture(t, accountrouting.Default)
	server := NewWithConfig(store, Config{AdminToken: "synthetic-admin-token"})
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	req := func(method, path, token, body string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		return response
	}
	for _, endpoint := range []string{"/api/admin/provider-allowances", "/api/admin/provider-allowances/presets", "/api/admin/provider-resources/a/allowance/config", "/api/admin/provider-resources/a/allowance/history", "/api/admin/provider-resources/a/allowance/events"} {
		if response := req("GET", endpoint, "", ""); response.Code != http.StatusUnauthorized {
			t.Fatalf("unauthorized %s: %d", endpoint, response.Code)
		}
		if response := req("GET", endpoint, "synthetic-admin-token", ""); response.Code != http.StatusOK {
			t.Fatalf("cache read %s: %d %s", endpoint, response.Code, response.Body.String())
		}
	}
	if response := req("PUT", "/api/admin/provider-resources/a/allowance/config", "", `{}`); response.Code != http.StatusUnauthorized {
		t.Fatal("unauthorized configuration write accepted")
	}
	if response := req("DELETE", "/api/admin/provider-resources/a/allowance/config", "synthetic-admin-token", ""); response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("wrong method: %d", response.Code)
	}
}

func TestAPIKeyReplacementClearsAllowanceIdentityAndConfig(t *testing.T) {
	_, store, _, _ := accountRoutingFixture(t, accountrouting.Default)
	_, err := store.saveProviderAllowanceConfig(t.Context(), "a", providerAllowanceConfig{Policy: allowance.Policy{Credits: "never"}})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := store.GetProviderResource("a")
	saveAccountQuota(t, store, "a", `{"windows":[{"name":"Wallet","balance":50,"unit":"USD"}]}`)
	if _, err := store.UpdateProviderResource("a", ProviderResource{APIKey: "synthetic-rotated-key"}); err != nil {
		t.Fatal(err)
	}
	after, _ := store.GetProviderResource("a")
	if sameProviderAccountSnapshot(before, after) {
		t.Fatal("API-key replacement retained wallet identity")
	}
	cfg, err := store.providerAllowanceConfig(t.Context(), "a")
	if err != nil || cfg.Revision != "" {
		t.Fatalf("old quota credentials survived replacement: %+v %v", cfg, err)
	}
	if err := store.SaveProviderResourceQuotaIfCurrent(t.Context(), "a", ProviderMock, `{"balance":999}`, time.Now().UTC(), before); AsHTTPError(err).Status != 409 {
		t.Fatalf("late replaced-key quota accepted: %v", err)
	}
}

func TestConfiguredAllowanceFencesLateResultsAfterRuleChange(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(fmt.Sprintf("query_failed_%v", failed), func(t *testing.T) {
			server, store, _, _ := accountRoutingFixture(t, accountrouting.Default)
			entered := make(chan struct{})
			release := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				<-release
				if failed {
					w.WriteHeader(503)
					return
				}
				_, _ = w.Write([]byte(`{"remaining":12}`))
			}))
			defer func() { unblock(); upstream.Close() }()
			cfg, err := store.saveProviderAllowanceConfig(t.Context(), "a", providerAllowanceConfig{Source: quotasource.Config{Enabled: true, URL: upstream.URL, Expression: "$remaining"}})
			if err != nil {
				t.Fatal(err)
			}
			resource, _ := store.GetProviderResource("a")
			finished := make(chan error, 1)
			go func() { _, _, err := server.refreshConfiguredAllowance(t.Context(), resource, true); finished <- err }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("synthetic collector did not start")
			}
			cfg.Source.URL = upstream.URL + "?revision=new"
			if _, err := store.saveProviderAllowanceConfig(t.Context(), "a", cfg); err != nil {
				t.Fatal(err)
			}
			unblock()
			if err := <-finished; err == nil {
				t.Fatal("late result was accepted")
			}
			if observation, found := store.GetProviderResourceObservation("a"); found && observation.QuotaSnapshot != "" {
				t.Fatal("old collection result changed the new configuration's cache")
			}
		})
	}
}

func TestProjectAllowanceAPIRespectsModelAccessAndHidesAccounts(t *testing.T) {
	server, store, call, _ := accountRoutingFixture(t, accountrouting.Default)
	project := store.CreateProject(Project{Name: "Synthetic quota reader", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{Name: "Synthetic quota reader key", Allowed: []string{call.Model.Name}, ModelAccessMode: ModelAccessModeRestricted}, "synthetic-quota-reader-key")
	if err != nil {
		t.Fatal(err)
	}
	saveAccountQuota(t, store, "a", `{"windows":[{"used_percent":20}]}`)
	saveAccountQuota(t, store, "b", `{"windows":[{"used_percent":100}]}`)
	request := func(model string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("GET", "/v1/allowance?model="+model, nil)
		r.Header.Set("Authorization", "Bearer "+secret)
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, r)
		return w
	}
	response := request(call.Model.Name)
	if response.Code != 200 {
		t.Fatalf("quota read: %d %s", response.Code, response.Body.String())
	}
	var result projectAllowanceSummary
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Available != 1 || result.Exhausted != 1 || result.Unknown != 1 {
		t.Fatalf("incorrect availability: %+v", result)
	}
	for _, private := range []string{"resource_id", "provider_id", "balance", "source", "config", "api_key"} {
		if strings.Contains(response.Body.String(), private) {
			t.Fatalf("aggregate read exposed %s", private)
		}
	}
	if denied := request("not-authorized"); denied.Code != 403 {
		t.Fatalf("model access was bypassed: %d", denied.Code)
	}
}
