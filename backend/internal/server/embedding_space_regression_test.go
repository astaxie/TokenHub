package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEmbeddingSpaceIsStableAcrossRequestsAndHealthChanges(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		writeJSON(w, 200, map[string]any{"data": []any{map[string]any{"index": 0, "embedding": []float64{1, 2}}}, "usage": map[string]any{"total_tokens": 1}})
	}))
	defer upstream.Close()
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "stable-embedding", Modality: "embedding", Status: StatusActive, EmbeddingPriceUSDPer1M: 1})
	for _, id := range []string{"a", "b"} {
		store.AddProvider(Provider{ID: id, Type: ProviderOpenAICompatible, BaseURL: upstream.URL, Healthy: true, Status: StatusActive, Options: map[string]string{"embedding_spaces": `{"m":"space-` + id + `"}`}})
		store.AddProviderModel(ProviderModel{ProviderID: id, UpstreamModel: "m", Modality: "embedding", InputPriceUSDPer1M: 1})
		store.AddRoute(ModelRoute{ID: "route-" + id, ProviderID: id, ProviderModel: "m", ModelName: "stable-embedding", Status: StatusActive, Weight: 100})
	}
	_, secret, err := store.CreateAPIKey("prj_demo", APIKey{Name: "space-test", Status: StatusActive, Allowed: []string{"stable-embedding"}}, "space-test-key")
	if err != nil {
		t.Fatal(err)
	}
	app := New(store)
	for i := 0; i < 4; i++ {
		if i == 2 {
			if err := store.db.Model(&Provider{}).Where("id = ?", "a").Update("healthy", false).Error; err != nil {
				t.Fatal(err)
			}
		}
		response := doJSON(t, app.Handler(), http.MethodPost, "/v1/embeddings", map[string]any{"model": "stable-embedding", "input": "text"}, secret)
		if response.Code != 409 {
			t.Fatalf("mixed space admitted: %d %s", response.Code, response.Body)
		}
	}
	if calls != 0 {
		t.Fatalf("incompatible upstream called %d times", calls)
	}
	providerB, _ := store.GetProvider("b")
	providerB.Options = map[string]string{"embedding_spaces": `{"m":"space-a"}`}
	if _, err := store.UpdateProvider("b", providerB); err != nil {
		t.Fatal(err)
	}
	response := doJSON(t, app.Handler(), http.MethodPost, "/v1/embeddings", map[string]any{"model": "stable-embedding", "input": "text"}, secret)
	if response.Code != 200 || calls != 1 {
		t.Fatalf("compatible fallback failed: %d %s calls=%d", response.Code, response.Body, calls)
	}
	provider, _ := store.GetProvider("b")
	candidate := ModelRoute{ID: "different-model", ProviderID: "b", ProviderModel: "other", ModelName: "stable-embedding", Status: StatusActive}
	if err := app.validateRetrievalRoute(candidate, nil, provider); err == nil {
		t.Fatal("mixed-space publication accepted")
	}
}

func TestEmbeddingSpaceIncludesResourceOverridesAndProviderFallback(t *testing.T) {
	store := NewMemoryStore()
	store.AddProvider(Provider{ID: "p", Type: ProviderOpenAICompatible, Options: map[string]string{"embedding_spaces": `{"m":"space-a"}`}})
	store.AddRoute(ModelRoute{ModelName: "m", ProviderID: "p", ProviderModel: "m", Status: StatusActive})
	resource, err := store.AddProviderResource(ProviderResource{ProviderID: "p", Name: "r", ResourceType: "api_key", Status: StatusActive, Healthy: false, Options: map[string]string{"embedding_spaces": `{"m":"space-b"}`}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.EmbeddingSpaceContract(context.Background(), "m", nil); err == nil {
		t.Fatal("unhealthy resource override escaped space validation")
	}
	encoded, _ := json.Marshal(map[string]string{"embedding_spaces": `{"m":"space-a"}`})
	if err := store.db.Model(&ProviderResource{}).Where("id = ?", resource.ID).Update("options", string(encoded)).Error; err != nil {
		t.Fatal(err)
	}
	if space, err := store.EmbeddingSpaceContract(context.Background(), "m", nil); err != nil || space != "verified:space-a" {
		t.Fatalf("space=%q err=%v", space, err)
	}
}

func TestVoyageEmbeddingUsesDocumentedEncodingParameters(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["encoding_format"] != nil || body["output_dtype"] != "float" {
			writeJSON(w, 400, map[string]string{"error": "invalid encoding"})
			return
		}
		writeJSON(w, 200, map[string]any{"data": []any{map[string]any{"index": 0, "embedding": []float64{1, 2}}}})
	}))
	defer upstream.Close()
	_, _, err := (OpenAICompatibleAdapter{Client: upstream.Client()}).Embeddings(context.Background(), Provider{BaseURL: upstream.URL, Options: map[string]string{"embedding_protocol": "voyage"}}, "voyage-model", EmbeddingsRequest{Model: "public", Input: "text", EncodingFormat: "base64"})
	if err != nil {
		t.Fatal(err)
	}
}
