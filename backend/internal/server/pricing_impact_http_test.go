package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func pricingImpactGateway(t *testing.T) (http.Handler, *GormStore, string, string, string) {
	return pricingImpactGatewayOnStore(t, NewMemoryStore())
}
func pricingImpactGatewayOnStore(t *testing.T, storage *GormStore) (http.Handler, *GormStore, string, string, string) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{"id":"analysis-response","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1000000,"completion_tokens":10000,"total_tokens":1010000,"prompt_tokens_details":{"cached_tokens":500000},"cache_write_input_tokens":100000,"cache_write_5m_input_tokens":60000,"cache_write_1h_input_tokens":30000}}`)
	}))
	t.Cleanup(upstream.Close)
	server, store, key := newAnthropicGatewayServerOnStore(t, upstream.URL, ProviderOpenAICompatible, nil, storage)
	handler := server.Handler()
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.UpdateModel("claude-tokenhub-test", Model{InputPriceUSDPer1M: 2, OutputPriceUSDPer1M: 6, CacheReadPriceUSDPer1M: .5}); err != nil {
		t.Fatal(err)
	}
	for _, pm := range store.ListProviderModels() {
		if pm.ProviderID == "prv_claude_code" && pm.UpstreamModel == "upstream-model" {
			pm.InputPriceUSDPer1M = 1
			pm.OutputPriceUSDPer1M = 2
			pm.CacheReadPriceUSDPer1M = .2
			pm.Metadata = map[string]string{"input_price_configured": "true", "output_price_configured": "true", "cache_read_price_configured": "true"}
			if _, err := store.UpdateProviderModel(pm.ID, pm); err != nil {
				t.Fatal(err)
			}
		}
	}
	response := doAnthropicRequest(t, handler, "/v1/chat/completions", map[string]any{"model": "claude-tokenhub-test", "messages": []map[string]string{{"role": "user", "content": "hello"}}}, "Bearer "+key, "")
	if response.Code != 200 {
		t.Fatalf("gateway: %d %s", response.Code, response.Body.String())
	}
	return handler, store, billingAdminToken(t, store), response.Header().Get("x-request-id"), key
}
func pricingImpactRequest(t *testing.T, app http.Handler, token string) map[string]any {
	t.Helper()
	loaded := doJSON(t, app, "GET", "/api/admin/billing/models/claude-tokenhub-test/pricing", nil, token)
	var draft modelPricingDraft
	if err := json.Unmarshal([]byte(loaded.Body), &draft); err != nil {
		t.Fatal(err)
	}
	draft.Card.Rates.Input = "3"
	now := time.Now().UTC()
	return map[string]any{"card": draft.Card, "fingerprint": draft.Fingerprint, "basis": "historical", "timezone": "UTC", "from": now.Format("2006-01-02"), "to": now.AddDate(0, 0, 1).Format("2006-01-02"), "project_ids": []string{}}
}
func TestPricingImpactHTTPUsesSameHistoricalRequests(t *testing.T) {
	app, _, token, _, _ := pricingImpactGateway(t)
	response := doJSON(t, app, "POST", "/api/admin/billing/model-pricing/impact", pricingImpactRequest(t, app, token), token)
	if response.Code != 200 {
		t.Fatalf("analysis: %d %s", response.Code, response.Body)
	}
	var result struct {
		Requests, Computable int
		Overall              *struct {
			Current   string `json:"current_usd"`
			Candidate string `json:"candidate_usd"`
			Cost      string `json:"cost_usd"`
			Delta     string `json:"charge_delta_usd"`
			Margin    string `json:"candidate_margin_usd"`
		} `json:"overall"`
		Receipt string `json:"receipt"`
	}
	if err := json.Unmarshal([]byte(response.Body), &result); err != nil {
		t.Fatal(err)
	}
	if result.Requests != 1 || result.Computable != 1 || result.Overall == nil {
		t.Fatalf("wrong cohort: %s", response.Body)
	}
	a := result.Overall
	if a.Current != "1.310000000000" || a.Candidate != "1.810000000000" || a.Cost != "0.620000000000" || a.Delta != "0.500000000000" || a.Margin != "1.190000000000" {
		t.Fatalf("incorrect worked example: %+v", a)
	}
	if result.Receipt == "" {
		t.Fatal("analysis lacks a verifiable decision basis")
	}
}

func TestPricingImpactHTTPKeepsPartialSamplesSeparate(t *testing.T) {
	app, store, token, _, key := pricingImpactGateway(t)
	for _, pm := range store.ListProviderModels() {
		if pm.ProviderID == "prv_claude_code" && pm.UpstreamModel == "upstream-model" {
			changed := doJSON(t, app, "PATCH", "/api/admin/provider-models/"+pm.ID, map[string]any{"input_price_usd_per_1m": 0, "input_price_configured": false}, token)
			if changed.Code != 200 {
				t.Fatalf("configuration: %d %s", changed.Code, changed.Body)
			}
		}
	}
	second := doAnthropicRequest(t, app, "/v1/chat/completions", map[string]any{"model": "claude-tokenhub-test", "messages": []map[string]string{{"role": "user", "content": "hello"}}}, "Bearer "+key, "")
	if second.Code != 200 {
		t.Fatalf("gateway: %d %s", second.Code, second.Body.String())
	}
	response := doJSON(t, app, "POST", "/api/admin/billing/model-pricing/impact", pricingImpactRequest(t, app, token), token)
	if response.Code != 200 {
		t.Fatalf("analysis: %d %s", response.Code, response.Body)
	}
	var report struct {
		Requests, Computable int
		Overall              any    `json:"overall"`
		Subset               any    `json:"computable_amounts"`
		RequestCoverage      string `json:"request_coverage_percent"`
		ChargeCoverage       string `json:"known_charge_coverage_percent"`
	}
	if err := json.Unmarshal([]byte(response.Body), &report); err != nil {
		t.Fatal(err)
	}
	if report.Requests != 2 || report.Computable != 1 || report.Overall != nil || report.Subset == nil || report.RequestCoverage != "50.00" || report.ChargeCoverage != "50.00" {
		t.Fatalf("partial scope misrepresented: %s", response.Body)
	}
}
func TestPricingImpactHTTPBindsDecisionToCandidate(t *testing.T) {
	app, _, token, _, _ := pricingImpactGateway(t)
	query := pricingImpactRequest(t, app, token)
	response := doJSON(t, app, "POST", "/api/admin/billing/model-pricing/impact", query, token)
	if response.Code != 200 {
		t.Fatalf("analysis: %d %s", response.Code, response.Body)
	}
	var report struct {
		Receipt string `json:"receipt"`
	}
	if err := json.Unmarshal([]byte(response.Body), &report); err != nil {
		t.Fatal(err)
	}
	card := query["card"].(meteringRateCard)
	card.Rates.Input = "4"
	body := map[string]any{"card": card, "fingerprint": query["fingerprint"], "analysis_receipt": report.Receipt, "request_id": "analysis-bound-candidate", "confirmed": true, "risk_acknowledged": true}
	rejected := doJSON(t, app, "POST", "/api/admin/billing/model-pricing/apply", body, token)
	if rejected.Code != 409 {
		t.Fatalf("altered candidate accepted: %d %s", rejected.Code, rejected.Body)
	}
	card.Rates.Input = "3"
	body["card"] = card
	for range 2 {
		applied := doJSON(t, app, "POST", "/api/admin/billing/model-pricing/apply", body, token)
		if applied.Code != 200 {
			t.Fatalf("valid analysis rejected: %d %s", applied.Code, applied.Body)
		}
	}
	changes := doJSON(t, app, "GET", "/api/admin/billing/price-changes", nil, token)
	var result struct {
		Data []struct {
			RequestID string `json:"request_id"`
			State     string `json:"analysis_state"`
			Analysis  *struct {
				Basis      string `json:"basis"`
				Computable int    `json:"computable"`
			} `json:"analysis"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(changes.Body), &result); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, change := range result.Data {
		if change.RequestID == "analysis-bound-candidate" {
			count++
			if change.State != "performed" || change.Analysis == nil || change.Analysis.Basis != "historical" || change.Analysis.Computable != 1 {
				t.Fatalf("missing decision basis: %+v", change)
			}
		}
	}
	if count != 1 {
		t.Fatalf("applied decision count=%d", count)
	}
}

func TestPricingImpactHTTPCurrentProcurementAndStaleCost(t *testing.T) {
	app, store, token, _, _ := pricingImpactGateway(t)
	query := pricingImpactRequest(t, app, token)
	var id string
	for _, pm := range store.ListProviderModels() {
		if pm.ProviderID == "prv_claude_code" && pm.UpstreamModel == "upstream-model" {
			id = pm.ID
		}
	}
	change := func(price int) {
		t.Helper()
		r := doJSON(t, app, "PATCH", "/api/admin/provider-models/"+id, map[string]any{"input_price_usd_per_1m": price}, token)
		if r.Code != 200 {
			t.Fatalf("procurement price: %d %s", r.Code, r.Body)
		}
	}
	change(2)
	query["basis"] = "current_procurement"
	response := doJSON(t, app, "POST", "/api/admin/billing/model-pricing/impact", query, token)
	if response.Code != 200 {
		t.Fatalf("current-price scenario: %d %s", response.Code, response.Body)
	}
	var result struct {
		Overall *struct {
			Cost string `json:"cost_usd"`
		} `json:"overall"`
		Receipt string `json:"receipt"`
	}
	if err := json.Unmarshal([]byte(response.Body), &result); err != nil {
		t.Fatal(err)
	}
	if result.Overall == nil || result.Overall.Cost != "1.120000000000" {
		t.Fatalf("wrong configured-cost scenario: %s", response.Body)
	}
	change(3)
	response = doJSON(t, app, "POST", "/api/admin/billing/model-pricing/apply", map[string]any{"card": query["card"], "fingerprint": query["fingerprint"], "request_id": "stale-procurement-price", "confirmed": true, "risk_acknowledged": true, "analysis_receipt": result.Receipt}, token)
	if response.Code != 409 {
		t.Fatalf("stale procurement basis accepted: %d %s", response.Code, response.Body)
	}
}
