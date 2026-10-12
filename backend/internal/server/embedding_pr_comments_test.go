package server

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestPRRetrievalTokenizedPatches(t *testing.T) {
	original := EmbeddingsRequest{Model: "m", Input: []int{1, 2, 3}}
	for _, input := range []any{"text", []int{3, 2, 1}} {
		if validateEmbeddingPatch(original, EmbeddingsRequest{Model: "m", Input: input}) == nil {
			t.Fatal("token semantics changed")
		}
	}
	if err := validateEmbeddingPatch(original, EmbeddingsRequest{Model: "m", Input: []any{float64(1), float64(2), float64(3)}}); err != nil {
		t.Fatal(err)
	}
}

func TestPRRetrievalEmbeddingAdmissionRejectsWrongModelAndPrice(t *testing.T) {
	for _, modality := range []string{"chat", "embedding"} {
		store := NewMemoryStore()
		project := store.CreateProject(Project{Name: "p", Status: StatusActive})
		store.AddModel(Model{Name: "m", Modality: modality, Status: StatusActive})
		provider := store.AddProvider(Provider{Type: ProviderMock, Status: StatusActive, Healthy: true})
		store.AddRoute(ModelRoute{ModelName: "m", ProviderID: provider.ID, ProviderModel: "m", Status: StatusActive, Weight: 100})
		_, secret, err := store.CreateAPIKey(project.ID, APIKey{Name: "key", Allowed: []string{"m"}, Status: StatusActive}, "thk_embedding_admission")
		if err != nil {
			t.Fatal(err)
		}
		app := New(store)
		t.Cleanup(func() { _ = app.Shutdown(context.Background()) })
		response := doJSON(t, app.Handler(), http.MethodPost, "/v1/embeddings", map[string]any{"model": "m", "input": "text"}, secret)
		if response.Code != 400 || !strings.Contains(response.Body, "embedding_model_not_configured") {
			t.Fatalf("%s admitted: %d %s", modality, response.Code, response.Body)
		}
	}
}

func configureEmbeddingTestModel(t *testing.T, store Store, name string) {
	t.Helper()
	if _, err := store.UpdateModel(name, Model{Modality: "embedding", EmbeddingPriceUSDPer1M: 1}); err != nil {
		t.Fatal(err)
	}
	for _, route := range store.ListRoutes() {
		if route.ModelName != name {
			continue
		}
		for _, upstream := range store.ListProviderModels() {
			if upstream.ProviderID != route.ProviderID || upstream.UpstreamModel != route.ProviderModel {
				continue
			}
			upstream.Modality = "embedding"
			upstream.Metadata = cloneStringMap(upstream.Metadata)
			if upstream.Metadata == nil {
				upstream.Metadata = map[string]string{}
			}
			if upstream.InputPriceUSDPer1M == 0 {
				upstream.Metadata["retrieval_pricing_confirmed"] = "true"
			}
			if _, err := store.UpdateProviderModel(upstream.ID, upstream); err != nil {
				t.Fatal(err)
			}
		}
	}
}
