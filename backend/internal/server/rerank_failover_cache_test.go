package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestRerankFailoverCacheProbesEligibleRoutesInOrder(t *testing.T) {
	var firstCalls, secondCalls atomic.Int32
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstCalls.Add(1)
		writeJSON(w, 503, map[string]any{"error": map[string]any{"message": "fixture unavailable"}})
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondCalls.Add(1)
		writeJSON(w, 200, map[string]any{"results": []any{map[string]any{"index": 0, "relevance_score": 0.8}}, "meta": map[string]any{"billed_units": map[string]any{"search_units": 1}}})
	}))
	defer second.Close()
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "cached-rank", Modality: "rerank", Status: StatusActive, InputPriceUSDPer1M: 0.5, Metadata: map[string]string{retrievalSearchUnitPriceKey: "0.003"}})
	for i, p := range []Provider{
		{ID: "rank-first", Type: ProviderOpenAICompatible, BaseURL: first.URL, Status: StatusActive, Healthy: true, Options: map[string]string{"rerank_protocol": "jina"}},
		{ID: "rank-second", Type: ProviderOpenAICompatible, BaseURL: second.URL, Status: StatusActive, Healthy: true, Options: map[string]string{"rerank_protocol": "cohere"}},
	} {
		store.AddProvider(p)
		store.AddProviderModel(ProviderModel{ProviderID: p.ID, UpstreamModel: "up", Modality: "rerank", InputPriceUSDPer1M: 0.2, Metadata: map[string]string{retrievalSearchUnitPriceKey: "0.001"}})
		store.AddRoute(ModelRoute{ID: p.ID, ModelName: "cached-rank", ProviderID: p.ID, ProviderModel: "up", Status: StatusActive, Priority: i, Weight: 100})
	}
	_, secret, err := store.CreateAPIKey("prj_demo", APIKey{Name: "cache", Allowed: []string{"cached-rank"}, Status: StatusActive}, "test-rerank-cache")
	if err != nil {
		t.Fatal(err)
	}
	app := New(store)
	t.Cleanup(func() { _ = app.Shutdown(context.Background()) })
	cache := map[string]pluginmeta.GatewayHookResult{}
	var probes []string
	register := func(hook pluginmeta.GatewayHookDescriptor, handler pluginmeta.GatewayHookHandlerFunc) {
		t.Helper()
		if err := app.gatewayChain.RegisterHook(hook); err != nil {
			t.Fatal(err)
		}
		if err := app.gatewayHooks.RegisterHandler(hook, handler); err != nil {
			t.Fatal(err)
		}
	}
	register(pluginmeta.GatewayHookDescriptor{PluginID: "test.rerank-cache", HookID: "lookup", Stage: pluginmeta.StageCacheLookup, Priority: 2000, Reads: []pluginmeta.GatewayDataClass{pluginmeta.DataCacheKey}, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataCacheKey, pluginmeta.DataProviderResponse, pluginmeta.DataUsage}, FailurePolicy: pluginmeta.FailurePolicyFailOpen}, func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		key := string(input.Data[pluginmeta.DataCacheKey])
		probes = append(probes, key)
		if cached, ok := cache[key]; ok {
			return cached, nil
		}
		return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue}, nil
	})
	register(pluginmeta.GatewayHookDescriptor{PluginID: "test.rerank-cache", HookID: "write", Stage: pluginmeta.StageCacheWrite, Priority: 2000, Reads: []pluginmeta.GatewayDataClass{pluginmeta.DataCacheKey, pluginmeta.DataProviderResponse, pluginmeta.DataUsage}, FailurePolicy: pluginmeta.FailurePolicyFailOpen}, func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		cache[string(input.Data[pluginmeta.DataCacheKey])] = pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionShortCircuit, Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{
			pluginmeta.DataCacheKey: {Value: input.Data[pluginmeta.DataCacheKey]}, pluginmeta.DataProviderResponse: {Value: input.Data[pluginmeta.DataProviderResponse]}, pluginmeta.DataUsage: {Value: input.Data[pluginmeta.DataUsage]},
		}}
		return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue}, nil
	})
	payload := map[string]any{"model": "cached-rank", "query": "q", "documents": []string{"d"}}
	for n := 0; n < 2; n++ {
		response := doJSON(t, app.Handler(), http.MethodPost, "/v1/rerank", payload, secret)
		if response.Code != 200 {
			t.Fatalf("request %d: status=%d body=%s", n, response.Code, response.Body)
		}

		if firstCalls.Load() != 1 || secondCalls.Load() != 1 {
			t.Fatalf("request %d repeated upstream attempts: first=%d second=%d", n, firstCalls.Load(), secondCalls.Load())
		}
	}
	if len(probes) != 4 || probes[0] != probes[2] || probes[1] != probes[3] || probes[0] == probes[1] {
		t.Fatalf("route cache probes not in stable plan order: %v", probes)
	}
	var rows []UsageRecord
	if err := store.db.Where("model_name = ?", "cached-rank").Order("created_at").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].CostUSD != 0.003 || rows[1].CostUSD != 0.003 || rows[1].ProviderCostUSD != 0 {
		t.Fatalf("cache hit must use winning route units without a new provider charge: %+v", rows)
	}
	// Disabled candidates must not regain eligibility merely because a cache entry exists.
	if _, err := store.UpdateRoute("rank-second", ModelRoute{Status: StatusDisabled}); err != nil {
		t.Fatal(err)
	}
	probes = nil
	response := doJSON(t, app.Handler(), http.MethodPost, "/v1/rerank", payload, secret)
	if response.Code == 200 || firstCalls.Load() != 2 || secondCalls.Load() != 1 || len(probes) != 1 {
		t.Fatalf("disabled route reused: status=%d first=%d second=%d probes=%d", response.Code, firstCalls.Load(), secondCalls.Load(), len(probes))
	}

}
