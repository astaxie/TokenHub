//go:build integration

package server

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestPostgresPricingAnalysisProcurementConcurrency(t *testing.T) {
	admin, url := openPostgresAdmin(t)
	schema := createPostgresSchema(t, admin, "pricing_analysis_")
	dsn, err := withSearchPath(url, schema)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{SecretKey: "pricing-integration-only-secret-key"}
	first, err := NewStoreWithDialect(dsn, cfg)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewStoreWithDialect(dsn, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	app, store, token, _, _ := pricingImpactGatewayOnStore(t, first)
	other := New(second).Handler()
	var providerModelID string
	for _, pm := range store.ListProviderModels() {
		if pm.ProviderID == "prv_claude_code" && pm.UpstreamModel == "upstream-model" {
			providerModelID = pm.ID
		}
	}
	for i := range 8 {
		reset := doJSON(t, app, "PATCH", "/api/admin/models/claude-tokenhub-test", map[string]any{"input_price_usd_per_1m": 2}, token)
		if reset.Code != 200 {
			t.Fatalf("reset model: %s", reset.Body)
		}
		reset = doJSON(t, app, "PATCH", "/api/admin/provider-models/"+providerModelID, map[string]any{"input_price_usd_per_1m": 1}, token)
		if reset.Code != 200 {
			t.Fatalf("reset cost: %s", reset.Body)
		}
		query := pricingImpactRequest(t, app, token)
		query["basis"] = "current_procurement"
		analyzed := doJSON(t, app, "POST", "/api/admin/billing/model-pricing/impact", query, token)
		if analyzed.Code != 200 {
			t.Fatalf("analysis: %d %s", analyzed.Code, analyzed.Body)
		}
		var receipt struct {
			Receipt string `json:"receipt"`
		}
		if err := json.Unmarshal([]byte(analyzed.Body), &receipt); err != nil {
			t.Fatal(err)
		}
		type outcome struct {
			kind string
			code int
			body string
		}
		results := make(chan outcome, 2)
		start := make(chan struct{})
		go func() {
			<-start
			r := doJSON(t, app, "POST", "/api/admin/billing/model-pricing/apply", map[string]any{"card": query["card"], "fingerprint": query["fingerprint"], "analysis_receipt": receipt.Receipt, "request_id": fmt.Sprintf("pg-pricing-analysis-%d", i), "confirmed": true, "risk_acknowledged": true}, token)
			results <- outcome{"apply", r.Code, r.Body}
		}()
		go func() {
			<-start
			r := doJSON(t, other, "PATCH", "/api/admin/provider-models/"+providerModelID, map[string]any{"input_price_usd_per_1m": 2}, token)
			results <- outcome{"cost", r.Code, r.Body}
		}()
		close(start)
		for range 2 {
			result := <-results
			if result.kind == "cost" && result.code != 200 || result.kind == "apply" && result.code != 200 && result.code != 409 {
				t.Errorf("unexpected concurrent %s result: %d %s", result.kind, result.code, result.body)
			}
		}
	}
}
