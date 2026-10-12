package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestEmbeddingHooksPreserveClientContract(t *testing.T) {
	original := `{"model":"m","input":["a","b"],"dimensions":3,"encoding_format":"base64","input_type":"query"}`
	for _, field := range []string{"model", "input", "dimensions", "encoding_format", "input_type", "task"} {
		t.Run(field, func(t *testing.T) {
			var req EmbeddingsRequest
			if err := json.Unmarshal([]byte(original), &req); err != nil {
				t.Fatal(err)
			}
			var payload map[string]any
			if err := json.Unmarshal([]byte(original), &payload); err != nil {
				t.Fatal(err)
			}
			switch field {
			case "model":
				payload[field] = "other"
			case "input":
				payload[field] = []string{"a"}
			case "dimensions":
				delete(payload, field)
			case "encoding_format":
				payload[field] = "float"
			case "input_type":
				payload[field] = "document"
			case "task":
				payload[field] = "retrieval.passage"
			}
			data, _ := json.Marshal(payload)
			if err := applyEmbeddingsGatewayRequestPatch(&req, data); err == nil {
				t.Fatalf("accepted contract change %s", field)
			}
		})
	}
	var req EmbeddingsRequest
	if err := json.Unmarshal([]byte(original), &req); err != nil {
		t.Fatal(err)
	}
	if err := applyEmbeddingsGatewayRequestPatch(&req, json.RawMessage(`{"model":"m","input":["[redacted]","b"],"dimensions":3,"encoding_format":"base64","input_type":"query"}`)); err != nil {
		t.Fatalf("safe text rewrite rejected: %v", err)
	}
}

func TestEmbeddingCacheValidatesVectorsAndKey(t *testing.T) {
	for _, echoKey := range []bool{false, true} {
		t.Run(map[bool]string{false: "unbound_hit_is_miss", true: "bound_hit_checks_shape"}[echoKey], func(t *testing.T) {
			app := newEmbeddingsCacheHookTestServer(t)
			hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.cache-contract", HookID: "cache", Stage: pluginmeta.StageCacheLookup, Priority: 1000, Reads: []pluginmeta.GatewayDataClass{pluginmeta.DataCacheKey}, Writes: []pluginmeta.GatewayDataClass{pluginmeta.DataCacheKey, pluginmeta.DataProviderResponse, pluginmeta.DataUsage}}
			if err := app.gatewayChain.RegisterHook(hook); err != nil {
				t.Fatal(err)
			}
			if err := app.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
				result := rawProviderCallResult(t, map[string]any{"object": "list", "data": []any{map[string]any{"index": 0, "embedding": []float64{0.5}}}}, Usage{PromptTokens: 2, TotalTokens: 2})
				if echoKey {
					if len(input.Data[pluginmeta.DataCacheKey]) == 0 {
						t.Fatal("host cache key missing")
					}
					result.Writes[pluginmeta.DataCacheKey] = pluginmeta.RawPatch{Value: input.Data[pluginmeta.DataCacheKey]}
				}
				return result, nil
			})); err != nil {
				t.Fatal(err)
			}
			resp := doJSON(t, app.Handler(), http.MethodPost, "/v1/embeddings", map[string]any{"model": "text-embedding-3-small", "input": []string{"a", "b"}, "dimensions": 3, "encoding_format": "base64"}, "thk_demo_local")
			if echoKey {
				if resp.Code != 502 {
					t.Fatalf("malformed vectors accepted: %d %s", resp.Code, resp.Body)
				}
			} else if resp.Code != 200 {
				t.Fatalf("legacy hit did not fall back: %d %s", resp.Code, resp.Body)
			}
		})
	}
}
func TestEmbeddingCacheKeyBindsSpaceAndDimensions(t *testing.T) {
	call := CallContext{Project: Project{ID: "p"}, Key: APIKey{ID: "k"}}
	req := EmbeddingsRequest{Model: "m", Input: "text"}
	a, err := embeddingCacheKey(call, "space-a", req)
	if err != nil {
		t.Fatal(err)
	}
	b, err := embeddingCacheKey(call, "space-b", req)
	if err != nil {
		t.Fatal(err)
	}
	d := 3
	req.Dimensions = &d
	c, err := embeddingCacheKey(call, "space-a", req)
	if err != nil {
		t.Fatal(err)
	}
	if a == b || a == c {
		t.Fatal("cache key omitted vector contract")
	}
	key, _ := json.Marshal(a)
	call.EmbeddingCacheKey = b
	if embeddingCacheHitMatches(call, pluginmeta.GatewayHookRunResult{Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{pluginmeta.DataCacheKey: {Value: key}}}) {
		t.Fatal("old-space cache value accepted")
	}
}
func TestResourceChangeRevalidatesEmbeddingPublication(t *testing.T) {
	store := NewMemoryStore()
	app := New(store)
	p := store.AddProvider(Provider{ID: "p", Type: ProviderOpenAICompatible, Status: StatusActive, Healthy: true})
	model := store.AddModel(Model{Name: "m", Modality: "embedding", Status: StatusActive, EmbeddingPriceUSDPer1M: 1})
	makeResource := func(name, space string) ProviderResource {
		resource, err := store.AddProviderResource(ProviderResource{ProviderID: p.ID, Name: name, Group: name, ResourceType: "api_key", Status: StatusActive, Healthy: true, Options: map[string]string{"embedding_spaces": `{"m":"` + space + `"}`}})
		if err != nil {
			t.Fatal(err)
		}
		return resource
	}
	a := makeResource("a", "space-a")
	b := makeResource("b", "space-b")
	route := store.AddRoute(ModelRoute{ID: "r", ProviderID: p.ID, ProviderModel: "m", ProviderResourceID: a.ID, ModelName: model.Name, Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "other", ProviderID: p.ID, ProviderModel: "m", ProviderResourceID: a.ID, ModelName: model.Name, Status: StatusActive})
	route.ProviderResourceID = b.ID
	if err := app.validateRetrievalRoute(route, &model, p); err == nil {
		t.Fatal("changed resource skipped space check")
	}
	route.ProviderResourceID = ""
	route.ResourceGroup = "b"
	if err := app.validateRetrievalRoute(route, &model, p); err == nil {
		t.Fatal("changed resource group skipped space check")
	}
}
