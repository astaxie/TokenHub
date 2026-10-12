package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRetrievalBillingPreservesFreeCostsAndRejectsNegativeCounters(t *testing.T) {
	for _, tc := range []struct {
		name, tenant string
		negative     bool
	}{
		{"free_tenant", "0", false}, {"negative_upstream_tokens", "0.003", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body := map[string]any{"results": []any{map[string]any{"index": 0, "relevance_score": 0.8}}, "meta": map[string]any{"billed_units": map[string]any{"search_units": 1}}}
				if tc.negative {
					body["usage"] = map[string]any{"prompt_tokens": -100, "total_tokens": -100}
				}
				writeJSON(w, 200, body)
			}))
			defer upstream.Close()
			store := NewMemoryStore()
			if err := SeedDemoData(store); err != nil {
				t.Fatal(err)
			}
			store.AddProvider(Provider{ID: "billing-rerank", Type: ProviderOpenAICompatible, BaseURL: upstream.URL, Status: StatusActive, Healthy: true, Options: map[string]string{"rerank_protocol": "cohere"}})
			store.AddModel(Model{Name: "billing-rerank", Modality: "rerank", Status: StatusActive, Metadata: map[string]string{retrievalSearchUnitPriceKey: tc.tenant}})
			store.AddProviderModel(ProviderModel{ProviderID: "billing-rerank", UpstreamModel: "model", Modality: "rerank", Metadata: map[string]string{retrievalSearchUnitPriceKey: "0.001"}})
			store.AddRoute(ModelRoute{ModelName: "billing-rerank", ProviderID: "billing-rerank", ProviderModel: "model", Status: StatusActive, Weight: 100})
			key, secret, err := store.CreateAPIKey("prj_demo", APIKey{Name: "billing-test", Allowed: []string{"billing-rerank"}, Status: StatusActive}, "test-billing-key")
			if err != nil {
				t.Fatal(err)
			}
			response := doJSON(t, New(store).Handler(), http.MethodPost, "/v1/rerank", map[string]any{"model": "billing-rerank", "query": "q", "documents": []string{"doc"}}, secret)
			if response.Code != 200 {
				t.Fatalf("status=%d body=%s", response.Code, response.Body)
			}
			var rows []UsageRecord
			if err := store.db.Where("model_name = ?", "billing-rerank").Find(&rows).Error; err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 {
				t.Fatalf("missing native usage record: %+v", rows)
			}
			if rows[0].InputTokens < 0 || rows[0].TotalTokens < 0 {
				t.Fatalf("negative usage persisted: %+v", rows[0])
			}
			if !tc.negative && (rows[0].CostUSD != 0 || rows[0].ProviderCostUSD != 0.001) {
				t.Fatalf("free request lost provider cost: %+v", rows[0])
			}
			var counters []struct {
				TotalTokens  int64
				PromptTokens int64
			}
			if err := store.db.Table("quota_buckets").Where("key_id = ?", key.ID).Find(&counters).Error; err != nil {
				t.Fatal(err)
			}
			if len(counters) == 0 {
				t.Fatal("quota counters missing")
			}
			for _, counter := range counters {
				if counter.TotalTokens < 0 || counter.PromptTokens < 0 {
					t.Fatalf("negative quota counter: %+v", counter)
				}
			}
			if tc.negative {
				if rows[0].CostUSD != 0.003 || rows[0].ProviderCostUSD != 0.001 {
					t.Fatalf("valid native quantity lost its prices: %+v", rows[0])
				}
				var entry meteringEntry
				if err := store.db.Where("id = ?", rows[0].RequestID+":shadow").First(&entry).Error; err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(entry.Payload, "auxiliary_token_usage_invalid") {
					t.Fatalf("invalid evidence discarded: %s", entry.Payload)
				}
			}
		})
	}
}
