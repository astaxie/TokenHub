package server

import (
	"net/http"
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
	if strings.Contains(pro.Metadata["endpoints"], "responses") {
		t.Fatalf("native DeepSeek V4 Pro must not advertise Responses yet: %+v", pro.Metadata)
	}
}

func TestGatewayRejectsDeepSeekV4ProResponsesRoute(t *testing.T) {
	store := NewMemoryStore()
	store.AddModel(Model{Name: "deepseek-pro-public", Modality: "chat", Status: StatusActive})
	provider := store.AddProvider(Provider{
		Name: "DeepSeek", Type: "deepseek", BaseURL: "https://api.deepseek.invalid",
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
	if response.Code != http.StatusNotImplemented || !strings.Contains(response.Body, "provider_capability_not_supported") {
		t.Fatalf("expected model-scoped Responses rejection, got %d: %s", response.Code, response.Body)
	}
}
