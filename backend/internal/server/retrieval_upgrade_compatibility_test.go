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
)

// Reproduce the published, zero-cost local inventory shown in issue #362.
// Upstream procurement evidence must not decide whether an existing route runs.
func TestRetrievalUpgradeKeepsPublishedLocalModelsCallable(t *testing.T) {
	for _, tc := range []struct {
		name, modality, protocol string
		cost                     float64
	}{
		{"embedding_auto_zero_cost", "embedding", "", 0},
		{"embedding_explicit_zero_cost", "embedding", "openai", 0},
		{"rerank_explicit_zero_cost", "rerank", "jina", 0},
		{"rerank_auto_paid_cost", "rerank", "", 1},
		{"rerank_auto_zero_cost", "rerank", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var upstreamCalls atomic.Int64
			endpoint := "/v1/embeddings"
			request := `{"model":"Local-Qwen-Embedding","input":["test"],"encoding_format":"float"}`
			response := `{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.1,0.2]}],"usage":{"prompt_tokens":2,"total_tokens":2}}`
			name, upstreamName := "Local-Qwen-Embedding", "qwen3-embedding-4b"
			if tc.modality == "rerank" {
				endpoint = "/v1/rerank"
				name, upstreamName = "Local-Qwen-Rerank", "qwen3-reranker-4b"
				request = `{"model":"Local-Qwen-Rerank","query":"test","documents":["first","second"],"top_n":2}`
				response = `{"results":[{"index":1,"relevance_score":0.9},{"index":0,"relevance_score":0.1}],"usage":{"total_tokens":2}}`
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upstreamCalls.Add(1)
				if r.URL.Path != endpoint {
					t.Errorf("path=%s want=%s", r.URL.Path, endpoint)
				}
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				if payload["model"] != upstreamName {
					t.Errorf("upstream model=%v", payload["model"])
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, response)
			}))
			defer upstream.Close()
			store := NewMemoryStore()
			project := store.CreateProject(Project{Name: "upgrade", Status: StatusActive})
			store.AddModel(Model{Name: name, Modality: tc.modality, Status: StatusActive, EmbeddingPriceUSDPer1M: 1, InputPriceUSDPer1M: 1})
			provider := store.AddProvider(Provider{ID: "local-provider", Type: ProviderOpenAICompatible, BaseURL: upstream.URL + "/v1", Healthy: true, Status: StatusActive, Options: map[string]string{tc.modality + "_protocol": tc.protocol}})
			inventory := store.AddProviderModel(ProviderModel{ProviderID: provider.ID, UpstreamModel: upstreamName, Modality: tc.modality, InputPriceUSDPer1M: tc.cost, Status: StatusActive})
			store.AddRoute(ModelRoute{ID: "published-local-route", ProviderID: provider.ID, ProviderModel: upstreamName, ModelName: name, Status: StatusActive, Weight: 100})
			_, key, err := store.CreateAPIKey(project.ID, APIKey{Name: "upgrade", Status: StatusActive, Allowed: []string{name}}, "thk_synthetic_upgrade_test")
			if err != nil {
				t.Fatal(err)
			}
			app := New(store)
			defer func() { _ = app.Shutdown(context.Background()) }()
			gateway := httptest.NewServer(app.Handler())
			defer gateway.Close()
			req, err := http.NewRequest(http.MethodPost, gateway.URL+endpoint, strings.NewReader(request))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer "+key)
			req.Header.Set("Content-Type", "application/json")
			resp, err := gateway.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("status=%d upstream_calls=%d response=%s", resp.StatusCode, upstreamCalls.Load(), body)
			if resp.StatusCode != 200 || upstreamCalls.Load() != 1 {
				t.Fatalf("published local model blocked: %d %s", resp.StatusCode, body)
			}
			var attempts []RouteAttemptLog
			if err := store.db.Find(&attempts).Error; err != nil {
				t.Fatal(err)
			}
			if len(attempts) != 1 {
				t.Fatalf("attempts=%d", len(attempts))
			}
			var records []UsageRecord
			if err := store.db.Find(&records).Error; err != nil {
				t.Fatal(err)
			}
			if len(records) != 1 || records[0].TotalTokens != 2 || records[0].CostUSD != 0.000002 {
				t.Fatalf("tenant charge or usage changed: %+v", records)
			}
			var saved ProviderModel
			if err := store.db.First(&saved, "id = ?", inventory.ID).Error; err != nil {
				t.Fatal(err)
			}
			if saved.Metadata["retrieval_pricing_confirmed"] != "" || saved.InputPriceUSDPer1M != tc.cost {
				t.Fatal("upgrade silently changed procurement configuration")
			}
			var entry meteringEntry
			if err := store.db.Where("kind = ?", "shadow_settlement").First(&entry).Error; err != nil {
				t.Fatal(err)
			}
			var shadow struct {
				Attempts []meteringAttemptCharge `json:"attempts"`
			}
			if err := json.Unmarshal([]byte(entry.Payload), &shadow); err != nil {
				t.Fatal(err)
			}
			if len(shadow.Attempts) != 1 {
				t.Fatal("missing cost evidence")
			}
			cost := shadow.Attempts[0].Charge
			if tc.cost == 0 && (cost.Charge != nil || cost.Status != "pending" || cost.LegacyUSD != "") {
				t.Fatalf("unknown cost became confirmed free: %+v", cost)
			}
			if tc.cost > 0 && cost.Charge == nil {
				t.Fatalf("configured cost lost: %+v", cost)
			}
		})
	}
}

func TestRerankAutoProtocolPreservesCatalogAndExplicitSelection(t *testing.T) {
	for _, tc := range []struct{ name, kind, catalog, configured, want string }{
		{"custom_auto", ProviderOpenAICompatible, "", "", "jina"},
		{"local_auto", "local", "", "", "jina"},
		{"local_catalog", "local", "local", "", "jina"},
		{"compatible_catalog", ProviderOpenAICompatible, ProviderOpenAICompatible, "", "jina"},
		{"cohere_catalog", ProviderOpenAICompatible, "cohere", "", "cohere"},
		{"voyage_catalog", ProviderOpenAICompatible, "voyage", "", "voyage"},
		{"unknown_catalog", ProviderOpenAICompatible, "unrecognized-vendor", "", ""},
		{"unsupported_adapter", "anthropic", "", "", ""},
		{"explicit_native", ProviderOpenAICompatible, "", "dashscope", "dashscope"},
		{"explicit_invalid", ProviderOpenAICompatible, "", "invalid", "invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := Provider{Type: tc.kind, Options: map[string]string{"catalog_id": tc.catalog, "rerank_protocol": tc.configured}}
			if got := providerRerankProtocol(p); got != tc.want {
				t.Fatalf("protocol=%q want=%q", got, tc.want)
			}
		})
	}
}
