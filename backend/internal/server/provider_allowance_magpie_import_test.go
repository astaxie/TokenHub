package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"tokenhub/backend/internal/accountrouting"
)

func TestMagpieImportRejectsInvalidSelectionsAndMaskedCredentials(t *testing.T) {
	_, store, _, _ := accountRoutingFixture(t, accountrouting.Default)
	s := NewWithConfig(store, Config{AdminToken: "synthetic-admin"})
	t.Cleanup(func() { _ = s.Shutdown(t.Context()) })
	for _, tc := range []struct {
		name, provider, ref string
	}{
		{"empty object", `{}`, ""},
		{"unrecognized object", `{"route":"unrelated"}`, ""},
		{"empty providers", `{"providers":[]}`, ""},
		{"unknown account", `{"id":"relay","accountCaps":{"existing":70}}`, "missing"},
		{"ambiguous account", `{"id":"relay","accountCaps":{"one":70,"two":80}}`, ""},
		{"masked token", `{"balanceURL":"https://quota.example/balance","balanceToken":"[redacted]"}`, ""},
		{"masked header", `{"balanceURL":"https://quota.example/balance","headers":{"Cookie":"[redacted]"}}`, ""},
		{"negative grant", `{"id":"relay","keyOwnBalances":{"key":{"usd":1,"at":"2026-01-01T00:00:00Z","expirings":[{"amount":-1,"until":"2026-12-31"}]}}}`, "key"},
		{"two currencies", `{"id":"relay","keyOwnBalances":{"key":{"usd":1,"cny":2,"at":"2026-01-01T00:00:00Z"}}}`, "key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(magpieQuotaImport{Providers: json.RawMessage(tc.provider), AccountRef: tc.ref})
			req := httptest.NewRequest("POST", "/api/admin/provider-resources/a/allowance/import-magpie", strings.NewReader(string(body)))
			req.Header.Set("Authorization", "Bearer synthetic-admin")
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, req)
			if w.Code != 400 {
				t.Fatalf("invalid import accepted: %d %s", w.Code, w.Body)
			}
		})
	}
	cfg, _ := store.providerAllowanceConfig(t.Context(), "a")
	if cfg.Revision != "" {
		t.Fatal("rejected import changed saved configuration")
	}
}

func TestMagpieImportMatchesCurrentKeyPlanAndNegativeBalance(t *testing.T) {
	_, store, _, _ := accountRoutingFixture(t, accountrouting.Default)
	s := NewWithConfig(store, Config{AdminToken: "synthetic-admin"})
	t.Cleanup(func() { _ = s.Shutdown(t.Context()) })
	_, err := store.UpdateProviderResource("a", ProviderResource{APIKey: "synthetic-second-key"})
	if err != nil {
		t.Fatal(err)
	}
	keyID := magpieImportKeyID("synthetic-second-key")
	provider := map[string]any{
		"id": "volcengine", "key": "synthetic-first-key", "keyPlan": "coding",
		"keys":        []map[string]string{{"key": "synthetic-second-key", "plan": "agent"}},
		"accessKeyID": "synthetic-access-id", "secretAccessKey": "synthetic-cloud-secret",
		"keyOwnBalances": map[string]any{strings.ToUpper(keyID): map[string]any{"usd": -5, "at": "2026-01-01T00:00:00Z"}},
	}
	raw, _ := json.Marshal(provider)
	body, _ := json.Marshal(magpieQuotaImport{Providers: raw, Timezone: "Asia/Shanghai"})
	req := httptest.NewRequest("POST", "/api/admin/provider-resources/a/allowance/import-magpie", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer synthetic-admin")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	var cfg providerAllowanceConfig
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &cfg) != nil || cfg.Source.Preset != "volcengine-agent" || cfg.Policy.Wallet.Balance != -5 || cfg.Token != "synthetic-cloud-secret" {
		t.Fatalf("native key plan or balance changed: %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "synthetic-second-key") || strings.Contains(w.Body.String(), "synthetic-first-key") {
		t.Fatal("import returned inference credentials")
	}
}

func TestMagpieNativeAutomaticConfigurationCanBeSaved(t *testing.T) {
	_, store, _, _ := accountRoutingFixture(t, accountrouting.Default)
	s := NewWithConfig(store, Config{AdminToken: "synthetic-admin"})
	t.Cleanup(func() { _ = s.Shutdown(t.Context()) })
	req := httptest.NewRequest("POST", "/api/admin/provider-resources/a/allowance/import-magpie", strings.NewReader(`{"providers":{"id":"relay","noBalanceProbe":true}}`))
	req.Header.Set("Authorization", "Bearer synthetic-admin")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	var cfg providerAllowanceConfig
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &cfg) != nil {
		t.Fatalf("automatic native import: %d %s", w.Code, w.Body)
	}
	if _, err := store.saveProviderAllowanceConfig(t.Context(), "a", cfg); err != nil {
		t.Fatalf("automatic native save: %v", err)
	}
	resource, _ := store.GetProviderResource("a")
	_, handled, err := s.refreshConfiguredAllowance(t.Context(), resource, true)
	if handled || err != nil {
		t.Fatalf("disabled relay probe became a query failure: handled=%v err=%v", handled, err)
	}
}
