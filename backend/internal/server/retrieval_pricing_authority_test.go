package server

import (
	"context"
	"net/http"
	"testing"
	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestRetrievalTenantPriceCannotBeOverridden(t *testing.T) {
	for _, tc := range []struct {
		name, modality string
		price, want    float64
	}{{"paid_embedding", "embedding", 0.5, 0.5}, {"paid_rerank", "rerank", 0.5, 0.5}, {"free_rerank", "rerank", 0, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			store := NewMemoryStore()
			if err := SeedDemoData(store); err != nil {
				t.Fatal(err)
			}
			store.AddProvider(Provider{ID: "spec-price", Type: ProviderOpenAICompatible, Status: StatusActive, Healthy: true, Options: map[string]string{"rerank_protocol": "jina"}})
			store.AddModel(Model{Name: "spec-price", Modality: tc.modality, Status: StatusActive, InputPriceUSDPer1M: tc.price, EmbeddingPriceUSDPer1M: tc.price, Metadata: map[string]string{"retrieval_pricing_confirmed": "true"}})
			store.AddProviderModel(ProviderModel{ProviderID: "spec-price", UpstreamModel: "up", Modality: tc.modality, InputPriceUSDPer1M: 0.2})
			store.AddRoute(ModelRoute{ModelName: "spec-price", ProviderID: "spec-price", ProviderModel: "up", Status: StatusActive, Weight: 100})
			_, secret, err := store.CreateAPIKey("prj_demo", APIKey{Name: "spec", Allowed: []string{"spec-price"}, Status: StatusActive}, "spec-review-key")
			if err != nil {
				t.Fatal(err)
			}
			app := New(store)
			t.Cleanup(func() { _ = app.Shutdown(context.Background()) })
			hook := pluginmeta.GatewayHookDescriptor{PluginID: "spec.price", HookID: "override", Stage: pluginmeta.StageProviderCall, Priority: 2000, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage}, FailurePolicy: pluginmeta.FailurePolicyFailClosed}
			if err := app.gatewayChain.RegisterHook(hook); err != nil {
				t.Fatal(err)
			}
			if err := app.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
				var response any = map[string]any{"model": "spec-price", "results": []any{map[string]any{"index": 0, "relevance_score": 0.8}}}
				if tc.modality == "embedding" {
					response = map[string]any{"model": "spec-price", "data": []any{map[string]any{"index": 0, "embedding": []float64{0.25, 0.75}}}}
				}
				return rawProviderCallResult(t, response, Usage{PromptTokens: 1000000, TotalTokens: 1000000, CostUSD: 9, InputCostUSD: 9}), nil
			})); err != nil {
				t.Fatal(err)
			}
			path := "/v1/rerank"
			payload := map[string]any{"model": "spec-price", "query": "q", "documents": []string{"d"}}
			if tc.modality == "embedding" {
				path = "/v1/embeddings"
				payload = map[string]any{"model": "spec-price", "input": "d"}
			}
			resp := doJSON(t, app.Handler(), http.MethodPost, path, payload, secret)
			if resp.Code != 200 {
				t.Fatalf("status=%d body=%s", resp.Code, resp.Body)
			}
			var row UsageRecord
			if err := store.db.Where("model_name = ?", "spec-price").First(&row).Error; err != nil {
				t.Fatal(err)
			}
			t.Logf("HTTP 200 tenant=%v provider=%v want tenant=%v", row.CostUSD, row.ProviderCostUSD, tc.want)
			if row.CostUSD != tc.want {
				t.Fatalf("plugin bypassed tenant price: got=%v want=%v", row.CostUSD, tc.want)
			}
		})
	}
}
