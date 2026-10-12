package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestTokenizedEmbeddingAdmissionUsesExactTokenCount(t *testing.T) {
	for _, input := range []any{[]int{0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, [][]int{{0, 0, 0, 0, 0}, {0, 0, 0, 0, 0}}} {
		raw, _ := json.Marshal(map[string]any{"model": "m", "input": input})
		var request EmbeddingsRequest
		if err := json.Unmarshal(raw, &request); err != nil {
			t.Fatal(err)
		}
		if got := requestTokenReservation(request); got != 10 {
			t.Fatalf("reserved=%d want10", got)
		}
		store := NewMemoryStore()
		project := store.CreateProject(Project{Name: "tokens", Status: StatusActive})
		limit := int64(9)
		store.AddModel(Model{Name: "m", Modality: "embedding", EmbeddingPriceUSDPer1M: 1, Status: StatusActive})
		_, key, err := store.CreateAPIKey(project.ID, APIKey{Name: "limit", Allowed: []string{"m"}, TokenLimitTPM: &limit, Status: StatusActive}, "thk_tokenized_limit")
		if err != nil {
			t.Fatal(err)
		}
		app := New(store)
		t.Cleanup(func() { _ = app.Shutdown(context.Background()) })
		response := doJSON(t, app.Handler(), http.MethodPost, "/v1/embeddings", request, key)
		if response.Code != 429 {
			t.Fatalf("tokenized admission=%d %s", response.Code, response.Body)
		}
	}
}

func TestEmbeddingRouteTransformCannotChangeCachedInput(t *testing.T) {
	app := New(NewMemoryStore())
	t.Cleanup(func() { _ = app.Shutdown(context.Background()) })
	hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.embedding-transform", HookID: "rewrite", Stage: pluginmeta.StageRequestTransform, Priority: 2000, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderRequest}, FailurePolicy: pluginmeta.FailurePolicyFailClosed}
	if err := app.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatal(err)
	}
	if err := app.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return rawProviderRequestPatch(t, map[string]any{"model": "gpt-test", "input": "route-specific rewrite"}), nil
	})); err != nil {
		t.Fatal(err)
	}
	request := EmbeddingsRequest{Model: "gpt-test", Input: "original"}
	if err := app.runGatewayEmbeddingsRequestTransformHooks(context.Background(), gatewayPluginTestCall(), RouteSelection{}, &request); err == nil {
		t.Fatal("route input rewrite accepted after cache binding")
	}
	if request.Input != "original" {
		t.Fatal("rejected patch changed request")
	}
}

func TestRetrievalRuntimeRejectsChangedInventory(t *testing.T) {
	for _, modality := range []string{"embedding", "rerank"} {
		store := NewMemoryStore()
		provider := store.AddProvider(Provider{ID: "p", Type: ProviderOpenAICompatible, Status: StatusActive, Options: map[string]string{"rerank_protocol": "jina"}})
		upstream := store.AddProviderModel(ProviderModel{ProviderID: "p", UpstreamModel: "u", Modality: modality, InputPriceUSDPer1M: 1})
		app := New(store)
		t.Cleanup(func() { _ = app.Shutdown(context.Background()) })
		call := CallContext{Model: Model{Modality: modality, InputPriceUSDPer1M: 1, EmbeddingPriceUSDPer1M: 1}}
		routes := []RouteSelection{{Provider: provider, ProviderModel: "u"}}
		filter := func() []RouteSelection { return app.pricedRetrievalRoutes(call, routes, modality, modality) }
		if len(filter()) != 1 {
			t.Fatal("valid inventory blocked")
		}
		upstream.Modality = "chat"
		if _, err := store.UpdateProviderModel(upstream.ID, upstream); err != nil {
			t.Fatal(err)
		}
		if len(filter()) != 0 {
			t.Fatal("changed modality admitted")
		}
		upstream.Modality = modality
		upstream.InputPriceUSDPer1M = 0
		if _, err := store.UpdateProviderModel(upstream.ID, upstream); err != nil {
			t.Fatal(err)
		}
		if len(filter()) != 1 {
			t.Fatal("unknown provider cost blocked a published route")
		}
		upstream.Metadata = map[string]string{"retrieval_pricing_confirmed": "true"}
		if _, err := store.UpdateProviderModel(upstream.ID, upstream); err != nil {
			t.Fatal(err)
		}
		if len(filter()) != 1 {
			t.Fatal("confirmed free procurement blocked")
		}
	}
}
