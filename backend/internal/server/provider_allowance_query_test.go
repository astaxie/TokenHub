package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"tokenhub/backend/internal/accountrouting"
	"tokenhub/backend/internal/quotasource"
)

func TestAllowancePreviewUsesUnsavedFormWithoutPersisting(t *testing.T) {
	_, store, _, _ := accountRoutingFixture(t, accountrouting.Default)
	s := NewWithConfig(store, Config{AdminToken: "synthetic-admin"})
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })
	if _, err := store.UpdateProviderResource("a", ProviderResource{APIKey: "synthetic-key"}); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	var refused atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if refused.Load() {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"opaque-panel-token synthetic-key"}`))
			return
		}
		body, _ := io.ReadAll(r.Body)
		if r.Method != "POST" || len(body) != 0 || r.Header.Get("Authorization") != "opaque-panel-token" || r.Header.Get("X-Quota") != "saved-header" || r.URL.Query().Get("key") != "synthetic-key" {
			t.Errorf("wrong preview request: %s, %v, %s", r.Method, r.Header, body)
		}
		_, _ = w.Write([]byte(`{"remaining":12.5,"used":0.252}`))
	}))
	defer upstream.Close()
	cfg, err := store.saveProviderAllowanceConfig(t.Context(), "a", providerAllowanceConfig{Source: quotasource.Config{Enabled: true, Auth: "magpie", URL: "https://saved.example/balance", Expression: "$old", Headers: map[string]string{"X-Quota": "saved-header"}}, Token: "opaque-panel-token"})
	if err != nil {
		t.Fatal(err)
	}
	saveAccountQuota(t, store, "a", `{"windows":[{"name":"saved","used_percent":10}]}`)
	before, _ := store.GetProviderResourceObservation("a")
	request := func(method, token string, cfg providerAllowanceConfig) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(cfg)
		r := httptest.NewRequest(method, "/api/admin/provider-resources/a/allowance/preview", strings.NewReader(string(body)))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	cfg.Source.URL = upstream.URL + "/balance?key={token}"
	cfg.Source.Method = "POST"
	cfg.Source.Expression = "$remaining; Month: used %; Missing: absent"
	if w := request("POST", "", cfg); w.Code != 401 || calls.Load() != 0 {
		t.Fatalf("unauthorized query: %d", w.Code)
	}
	if w := request("GET", "synthetic-admin", cfg); w.Code != 405 {
		t.Fatalf("method accepted: %d", w.Code)
	}
	w := request("POST", "synthetic-admin", cfg)
	var result quotasource.Result
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result.Parts) != 3 || result.Parts[0].Text != "$12.50" || result.Parts[1].Text != "25.2%" || result.Parts[2].Text != "—" {
		t.Fatalf("preview: %d %s", w.Code, w.Body)
	}
	after, _ := store.GetProviderResourceObservation("a")
	saved, _ := store.providerAllowanceConfig(t.Context(), "a")
	points, _ := store.providerAllowanceHistory(t.Context(), "a", time.Now().Add(-time.Hour), 100)
	if after.QuotaSnapshot != before.QuotaSnapshot || saved.Revision != cfg.Revision || saved.Source.URL != "https://saved.example/balance" || len(points) != 1 {
		t.Fatal("preview modified saved configuration or observations")
	}
	refused.Store(true)
	w = request("POST", "synthetic-admin", cfg)
	if w.Code != 502 || strings.Contains(w.Body.String(), "opaque-panel-token") || strings.Contains(w.Body.String(), "synthetic-key") {
		t.Fatalf("preview exposed an upstream refusal: %d %s", w.Code, w.Body)
	}
	after, _ = store.GetProviderResourceObservation("a")
	if after.QuotaSnapshot != before.QuotaSnapshot {
		t.Fatal("failed preview changed the saved observation")
	}
	cfg.Revision = "stale"
	if w := request("POST", "synthetic-admin", cfg); w.Code != 409 || calls.Load() != 2 {
		t.Fatalf("stale preview sent secrets: %d", w.Code)
	}
	cfg.Revision = saved.Revision
	cfg.Source.Headers = map[string]string{"X-Renamed": "[redacted]"}
	if w := request("POST", "synthetic-admin", cfg); w.Code != 400 || calls.Load() != 2 {
		t.Fatalf("renamed header accepted: %d", w.Code)
	}
	data, _ := json.Marshal(result)
	if err := store.SaveProviderResourceQuota("a", ProviderMock, string(data), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	read := httptest.NewRequest("GET", "/api/admin/provider-allowances", nil)
	read.Header.Set("Authorization", "Bearer synthetic-admin")
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, read)
	var entries struct {
		Data []providerAllowanceEntry `json:"data"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &entries) != nil {
		t.Fatalf("cached balance: %d %s", w.Code, w.Body)
	}
	for _, entry := range entries.Data {
		if entry.ResourceID == "a" && (len(entry.BalanceParts) != 3 || entry.BalanceParts[0].Text != "$12.50" || entry.BalanceParts[2].Text != "—") {
			t.Fatalf("cached formatting lost: %+v", entry.BalanceParts)
		}
	}
}
