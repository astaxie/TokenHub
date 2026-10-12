package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestRerankNativeCostDoesNotFallBackToAuxiliaryTokens(t *testing.T) {
	for _, tc := range []struct {
		name, price, pendingReason string
		reported                   bool
		wantCost                   float64
	}{
		{"missing_price", "", "native_unit_price_required", true, 0},
		{"paid_native_price", "0.003", "", true, 0.006},
		{"free_native_price", "0", "", true, 0},
		{"unknown_native_usage", "0.003", "usage_presence_unknown", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body := map[string]any{
					"results": []any{map[string]any{"index": 0, "relevance_score": 0.8}},
					"usage":   map[string]any{"prompt_tokens": 100, "total_tokens": 100},
				}
				if tc.reported {
					body["meta"] = map[string]any{"billed_units": map[string]any{"search_units": 2}}
				}
				writeJSON(w, http.StatusOK, body)
			}))
			defer upstream.Close()
			store := NewMemoryStore()
			project := store.CreateProject(Project{Name: "native-cost", Status: StatusActive})
			store.AddProvider(Provider{ID: "native-cost", Type: ProviderOpenAICompatible, BaseURL: upstream.URL, Status: StatusActive, Healthy: true, Options: map[string]string{"rerank_protocol": "cohere"}})
			store.AddModel(Model{Name: "native-cost", Modality: "rerank", Status: StatusActive, Metadata: map[string]string{retrievalSearchUnitPriceKey: "0.004"}})
			metadata := map[string]string{}
			if tc.price != "" {
				metadata[retrievalSearchUnitPriceKey] = tc.price
			}
			store.AddProviderModel(ProviderModel{ProviderID: "native-cost", UpstreamModel: "model", Modality: "rerank", InputPriceUSDPer1M: 10, Metadata: metadata})
			store.AddRoute(ModelRoute{ModelName: "native-cost", ProviderID: "native-cost", ProviderModel: "model", Status: StatusActive, Weight: 100})
			_, secret, err := store.CreateAPIKey(project.ID, APIKey{Name: "native-cost", Allowed: []string{"native-cost"}, Status: StatusActive}, "thk_synthetic_native_cost")
			if err != nil {
				t.Fatal(err)
			}
			app := New(store)
			defer func() { _ = app.Shutdown(context.Background()) }()
			response := doJSON(t, app.Handler(), http.MethodPost, "/v1/rerank", map[string]any{"model": "native-cost", "query": "q", "documents": []string{"doc"}}, secret)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body)
			}
			var row UsageRecord
			if err := store.db.Where("model_name = ?", "native-cost").First(&row).Error; err != nil {
				t.Fatal(err)
			}
			if row.ProviderCostUSD != tc.wantCost || row.InputTokens != 100 || row.TotalTokens != 100 {
				t.Fatalf("unexpected cost or lost auxiliary usage: %+v", row)
			}
			wantTenant := 0.0
			if tc.reported {
				wantTenant = 0.008
			}
			if row.CostUSD != wantTenant {
				t.Fatalf("tenant charge=%v want=%v", row.CostUSD, wantTenant)
			}
			var entry meteringEntry
			if err := store.db.First(&entry, "id = ?", row.RequestID+":shadow").Error; err != nil {
				t.Fatal(err)
			}
			var shadow struct {
				Attempts []meteringAttemptCharge `json:"attempts"`
			}
			if err := json.Unmarshal([]byte(entry.Payload), &shadow); err != nil {
				t.Fatal(err)
			}
			if len(shadow.Attempts) != 1 {
				t.Fatalf("expected one attempt: %+v", shadow)
			}
			charge := shadow.Attempts[0].Charge
			if charge.Evidence == nil || charge.Evidence.Unit != "search_unit" || (charge.Evidence.Quantity != nil) != tc.reported {
				t.Fatalf("lost native usage evidence: %+v", charge)
			}
			if tc.pendingReason != "" {
				if charge.Status != "pending" || charge.Reason != tc.pendingReason || charge.Charge != nil || charge.LegacyUSD != "" {
					t.Fatalf("unknown cost became a priced amount: %+v", charge)
				}
			} else {
				if charge.Status != "estimated" || charge.Charge == nil {
					t.Fatalf("configured native cost lost: %+v", charge)
				}
				for _, amount := range []string{charge.Charge.USD, charge.LegacyUSD} {
					cost, err := strconv.ParseFloat(amount, 64)
					if err != nil || cost != row.ProviderCostUSD {
						t.Fatalf("evidence amount %q differs from usage cost %v", amount, row.ProviderCostUSD)
					}
				}
			}
		})
	}
}
