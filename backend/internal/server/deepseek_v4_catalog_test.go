package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestStandardCatalogIncludesNativeDeepSeekV4Models(t *testing.T) {
	models, err := defaultModelCatalog("../../../data/model-catalog.yaml")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Model{}
	for _, model := range models {
		byName[strings.ToLower(model.Name)] = model
	}
	flash, ok := byName["deepseek-v4-flash"]
	if !ok {
		t.Fatal("expected native DeepSeek V4 Flash in standard catalog")
	}
	if flash.ContextWindow != 1048576 || flash.InputPriceUSDPer1M != 0.14 ||
		flash.CacheReadPriceUSDPer1M != 0.0028 || flash.OutputPriceUSDPer1M != 0.28 ||
		flash.Metadata["endpoints"] != "responses,chat/completions,anthropic" {
		t.Fatalf("unexpected native DeepSeek V4 Flash metadata: %+v", flash)
	}
	pro, ok := byName["deepseek-v4-pro"]
	if !ok {
		t.Fatal("expected native DeepSeek V4 Pro in standard catalog")
	}
	if pro.Metadata["endpoints"] != "responses,chat/completions,anthropic" {
		t.Fatalf("unexpected native DeepSeek V4 Pro protocol metadata: %+v", pro.Metadata)
	}
	for _, model := range []Model{flash, pro} {
		if !slices.Contains(model.SupportedParameters, "top_logprobs") ||
			model.Metadata["features"] != "function-calling,structured-outputs,reasoning,apply-patch,web-search" ||
			model.Metadata["top_logprobs_range"] != "0,20" || model.Metadata["responses_stateful"] != "false" ||
			model.Metadata["prompt_cache_mode"] != "automatic" || model.Metadata["custom_tool_names"] != "apply_patch" {
			t.Fatalf("incomplete native DeepSeek Responses metadata for %s: %+v", model.Name, model)
		}
	}
}

func TestGatewayForwardsDeepSeekV4ProResponsesRoute(t *testing.T) {
	var upstreamRequest map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/responses" {
			t.Errorf("unexpected upstream request: %s %s", r.Method, r.URL.Path)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&upstreamRequest); err != nil {
			t.Errorf("decode upstream request: %v", err)
			return
		}
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_deepseek_pro","object":"response","status":"completed","model":"deepseek-v4-pro","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
	}))
	t.Cleanup(upstream.Close)

	store := NewMemoryStore()
	store.AddModel(Model{Name: "deepseek-pro-public", Modality: "chat", Status: StatusActive})
	provider := store.AddProvider(Provider{
		Name: "DeepSeek", Type: "deepseek", BaseURL: upstream.URL, APIKey: "deepseek-test-key",
		Status: StatusActive, Healthy: true,
	})
	store.AddRoute(ModelRoute{
		ModelName: "deepseek-pro-public", ProviderID: provider.ID, ProviderModel: "deepseek-v4-pro",
		Priority: 1, Weight: 100, Status: StatusActive,
	})
	project := store.CreateProject(Project{Name: "DeepSeek Responses", Status: StatusActive})
	if _, _, err := store.CreateAPIKey(project.ID, APIKey{Name: "DeepSeek Responses"}, "thk_deepseek_responses"); err != nil {
		t.Fatal(err)
	}

	response := doJSON(t, New(store).Handler(), http.MethodPost, "/v1/responses", map[string]any{
		"model": "deepseek-pro-public",
		"input": "hello",
	}, "thk_deepseek_responses")
	if response.Code != http.StatusOK || !strings.Contains(response.Body, "resp_deepseek_pro") {
		t.Fatalf("expected successful Responses forwarding, got %d: %s", response.Code, response.Body)
	}
	if upstreamRequest["model"] != "deepseek-v4-pro" || upstreamRequest["input"] != "hello" {
		t.Fatalf("unexpected upstream request body: %#v", upstreamRequest)
	}
}
