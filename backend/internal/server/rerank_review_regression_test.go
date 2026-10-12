package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestRerankRoutePatchPreservesCacheBoundInput(t *testing.T) {
	original := RerankRequest{Model: "rank", Query: "q", Documents: []string{"d"}}
	for _, field := range []string{"query", "documents", "instruction", "truncation", "return_documents", "top_n"} {
		t.Run(field, func(t *testing.T) {
			req := original
			patch := map[string]any{"model": "rank", "query": "q", "documents": []string{"d"}}
			switch field {
			case "query", "instruction":
				patch[field] = "changed"
			case "documents":
				patch[field] = []string{"changed"}
			case "truncation", "return_documents":
				patch[field] = true
			case "top_n":
				patch[field] = 1
			}
			data, _ := json.Marshal(patch)
			if err := rerankRoutePatch(&req)(data); err == nil {
				t.Fatal("accepted cache-bound input mutation")
			}
		})
	}
	data, _ := json.Marshal(original)
	if err := rerankRoutePatch(&original)(data); err != nil {
		t.Fatal(err)
	}
	// Privacy hooks may still redact content before the cache contract is established.
	data = []byte(`{"model":"rank","query":"[redacted]","documents":["[redacted]"]}`)
	if err := applyRerankPatch(&original, data); err != nil {
		t.Fatal(err)
	}
}

func TestRerankRejectsUnrequestedDocuments(t *testing.T) {
	for _, document := range []any{nil, map[string]any{"text": "private"}} {
		response := map[string]any{"model": "rank", "results": []any{map[string]any{"index": 0, "relevance_score": 0.8, "document": document}}}
		if _, err := validateRerankResult(response, RerankRequest{Model: "rank", Query: "q", Documents: []string{"private"}}); err == nil {
			t.Fatal("accepted unrequested document field")
		}
	}
}

func TestRerankCacheKeyBindsRouteAndInput(t *testing.T) {
	call := CallContext{}
	req := RerankRequest{Model: "rank", Query: "q", Documents: []string{"d"}}
	route := RouteSelection{Provider: Provider{ID: "a", Type: ProviderOpenAICompatible, Options: map[string]string{"rerank_protocol": "jina"}}, ProviderModel: "rank"}
	key := rerankCacheKey(call, route, req)
	other := route
	other.Provider.Options = map[string]string{"rerank_protocol": "cohere"}
	if key == rerankCacheKey(call, other, req) {
		t.Fatal("protocol change reused cache key")
	}
	other = route
	other.ProviderModel = "different"
	if key == rerankCacheKey(call, other, req) {
		t.Fatal("model change reused cache key")
	}
	req.Query = "changed"
	if key == rerankCacheKey(call, route, req) {
		t.Fatal("input change reused cache key")
	}
}

func TestRerankPluginUnitMustMatchRoute(t *testing.T) {
	for _, stage := range []pluginmeta.GatewayHookStage{pluginmeta.StageProviderCall, pluginmeta.StageCacheLookup} {
		for _, mismatch := range []bool{false, true} {
			name := string(stage)
			if mismatch {
				name += "_mismatch"
			}
			t.Run(name, func(t *testing.T) {
				store := NewMemoryStore()
				if err := SeedDemoData(store); err != nil {
					t.Fatal(err)
				}
				store.AddProvider(Provider{ID: "rank-unit", Type: ProviderOpenAICompatible, Status: StatusActive, Healthy: true, Options: map[string]string{"rerank_protocol": "jina"}})
				store.AddModel(Model{Name: "rank-unit", Modality: "rerank", Status: StatusActive, InputPriceUSDPer1M: 0.5, Metadata: map[string]string{"retrieval_pricing_confirmed": "true"}})
				store.AddProviderModel(ProviderModel{ProviderID: "rank-unit", UpstreamModel: "up", Modality: "rerank", InputPriceUSDPer1M: 0.2})
				store.AddRoute(ModelRoute{ModelName: "rank-unit", ProviderID: "rank-unit", ProviderModel: "up", Status: StatusActive, Weight: 100})
				_, secret, err := store.CreateAPIKey("prj_demo", APIKey{Name: "rank", Allowed: []string{"rank-unit"}, Status: StatusActive}, "test-rank-unit")
				if err != nil {
					t.Fatal(err)
				}
				app := New(store)
				t.Cleanup(func() { _ = app.Shutdown(context.Background()) })
				hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.rank-unit", HookID: "result", Stage: stage, Priority: 2000, Reads: []pluginmeta.GatewayDataClass{pluginmeta.DataCacheKey}, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage, pluginmeta.DataCacheKey}, FailurePolicy: pluginmeta.FailurePolicyFailClosed}

				if stage == pluginmeta.StageCacheLookup {
					hook.FailurePolicy = pluginmeta.FailurePolicyFailOpen
				} else {
					hook.Reads = nil
					hook.Writes = []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage}
				}
				if err := app.gatewayChain.RegisterHook(hook); err != nil {
					t.Fatal(err)
				}
				if err := app.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
					quantity := int64(1000000)
					usage := Usage{PromptTokens: 1000000, TotalTokens: 1000000}
					if mismatch {
						usage = Usage{RetrievalEvidence: &RetrievalUsageEvidence{Unit: "search_unit", Source: "plugin", Quantity: &quantity}}
					}
					result := rawProviderCallResult(t, map[string]any{"model": "rank-unit", "results": []any{map[string]any{"index": 0, "relevance_score": 0.8}}}, usage)
					if stage == pluginmeta.StageCacheLookup {
						result.Writes[pluginmeta.DataCacheKey] = pluginmeta.RawPatch{Value: input.Data[pluginmeta.DataCacheKey]}
					}
					return result, nil
				})); err != nil {
					t.Fatal(err)
				}
				resp := doJSON(t, app.Handler(), http.MethodPost, "/v1/rerank", map[string]any{"model": "rank-unit", "query": "q", "documents": []string{"d"}}, secret)
				want := http.StatusOK
				if mismatch {
					want = http.StatusBadGateway
				}
				if resp.Code != want {
					t.Fatalf("status=%d want=%d body=%s", resp.Code, want, resp.Body)
				}
				if !mismatch {
					var row UsageRecord
					if err := store.db.Where("model_name = ?", "rank-unit").First(&row).Error; err != nil {
						t.Fatal(err)
					}
					if row.CostUSD != 0.5 {
						t.Fatalf("tenant cost=%v", row.CostUSD)
					}
				}
			})
		}
	}
}

func TestRerankCacheRequiresMatchingHostKey(t *testing.T) {
	app := newEmbeddingsCacheHookTestServer(t)
	t.Cleanup(func() { _ = app.Shutdown(context.Background()) })
	echoed := ""
	hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.rank-cache", HookID: "hit", Stage: pluginmeta.StageCacheLookup, Priority: 2000, Reads: []pluginmeta.GatewayDataClass{pluginmeta.DataCacheKey}, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage, pluginmeta.DataCacheKey}, FailurePolicy: pluginmeta.FailurePolicyFailOpen}
	if err := app.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatal(err)
	}
	if err := app.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		if string(input.Data[pluginmeta.DataCacheKey]) != `"rerank:v1:expected"` {
			t.Fatalf("missing host key: %s", input.Data[pluginmeta.DataCacheKey])
		}
		result := rawProviderCallResult(t, map[string]any{"results": []any{}}, Usage{})
		if echoed != "" {
			key, _ := json.Marshal(echoed)
			result.Writes[pluginmeta.DataCacheKey] = pluginmeta.RawPatch{Value: key}
		}
		return result, nil
	})); err != nil {
		t.Fatal(err)
	}
	call := gatewayPluginTestCall()
	call.RerankCacheKey = "rerank:v1:expected"
	for _, key := range []string{"", "rerank:v1:other", "rerank:v1:expected"} {
		echoed = key
		_, _, hit, err := app.runGatewayCacheLookupHooks(context.Background(), call, RerankRequest{Model: "rank", Query: "q", Documents: []string{"d"}})
		if err != nil {
			t.Fatal(err)
		}
		if hit != (key == call.RerankCacheKey) {
			t.Fatalf("key=%q hit=%v", key, hit)
		}
	}
}
