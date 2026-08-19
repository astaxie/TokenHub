package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestResponsesRoutePreservesLegacyUsageTotals(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Usage compatibility project", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{Name: "Usage compatibility key", Status: StatusActive}, "thk_usage_compatibility")
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "usage-compatible-model", Modality: "chat", Status: StatusActive})
	provider := store.AddProvider(Provider{Name: "Usage compatibility provider", Type: ProviderMock, Status: StatusActive, Healthy: true})
	store.AddRoute(ModelRoute{
		ModelName: "usage-compatible-model", ProviderID: provider.ID, ProviderModel: "usage-compatible-model",
		Priority: 1, Weight: 100, Status: StatusActive,
	})

	resp := doJSON(t, New(store).Handler(), http.MethodPost, "/v1/responses", map[string]any{
		"model": "usage-compatible-model",
		"input": "preserve existing response usage fields",
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(resp.Body), &body); err != nil {
		t.Fatal(err)
	}
	usage, _ := body["usage"].(map[string]any)
	if usage["input_tokens"] == nil || usage["output_tokens"] == nil ||
		usage["prompt_tokens"] != usage["input_tokens"] ||
		usage["completion_tokens"] != usage["output_tokens"] {
		t.Fatalf("Responses usage totals = %#v", usage)
	}
}
