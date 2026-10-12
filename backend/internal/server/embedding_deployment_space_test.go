package server

import (
	"context"
	"testing"
)

func TestEmbeddingDefaultSpaceIncludesEffectiveDeployment(t *testing.T) {
	for _, change := range []string{"base_url", "protocol", "path", "type"} {
		t.Run(change, func(t *testing.T) {
			provider := Provider{ID: "p", Type: ProviderOpenAICompatible, BaseURL: "https://a.example/v1", Options: map[string]string{}}
			base := RouteSelection{Provider: provider, ProviderModel: "default"}
			other := base
			other.Provider.Options = map[string]string{}
			switch change {
			case "base_url":
				other.Provider.BaseURL = "https://b.example/v1"
			case "protocol":
				other.Provider.Options["embedding_protocol"] = "jina"
			case "path":
				other.Provider.Options["embedding_path"] = "/different"
			case "type":
				other.Provider.Type = "gemini"
			}
			if embeddingSpaceKey(base) == embeddingSpaceKey(other) {
				t.Fatal("different deployments share a default space")
			}
			req := EmbeddingsRequest{Model: "public", Input: "text"}
			a, _ := embeddingCacheKey(CallContext{}, embeddingSpaceKey(base), req)
			b, _ := embeddingCacheKey(CallContext{}, embeddingSpaceKey(other), req)
			if a == b {
				t.Fatal("cache reused across deployments")
			}
			base.Provider.Options = map[string]string{"embedding_spaces": `{"default":"verified-shared"}`}
			other.Provider.Options["embedding_spaces"] = `{"default":"verified-shared"}`
			if embeddingSpaceKey(base) != embeddingSpaceKey(other) {
				t.Fatal("explicit compatibility ignored")
			}
		})
	}
}

func TestEmbeddingResourceEndpointsRequireExplicitCompatibility(t *testing.T) {
	store := NewMemoryStore()
	store.AddProvider(Provider{ID: "p", Type: ProviderOpenAICompatible, BaseURL: "https://a.example/v1", Status: StatusActive, Healthy: true})
	store.AddRoute(ModelRoute{ModelName: "public", ProviderID: "p", ProviderModel: "default", Status: StatusActive})
	for _, endpoint := range []string{"https://a.example/v1", "https://b.example/v1"} {
		if _, err := store.AddProviderResource(ProviderResource{ProviderID: "p", Name: endpoint, ResourceType: "api_key", BaseURL: endpoint, Status: StatusActive, Healthy: true}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.EmbeddingSpaceContract(context.Background(), "public", nil); err == nil {
		t.Fatal("unconfirmed resource deployment admitted")
	}
	provider, _ := store.GetProvider("p")
	provider.Options = map[string]string{"embedding_spaces": `{"default":"shared"}`}
	if _, err := store.UpdateProvider("p", provider); err != nil {
		t.Fatal(err)
	}
	if space, err := store.EmbeddingSpaceContract(context.Background(), "public", nil); err != nil || space != "verified:shared" {
		t.Fatalf("confirmed resources rejected: %s %v", space, err)
	}
}

func TestEmbeddingEquivalentEndpointSettingsKeepTheirSpace(t *testing.T) {
	base := RouteSelection{Provider: Provider{ID: "p", Type: ProviderOpenAICompatible, BaseURL: "https://a.example/v1"}, ProviderModel: "default"}
	explicit := base
	explicit.Provider.BaseURL = "https://a.example/v1/"
	explicit.Provider.APIKey = "rotated-credential"
	explicit.Provider.Options = map[string]string{"embedding_protocol": "openai", "embedding_path": "/embeddings"}
	if embeddingSpaceKey(base) != embeddingSpaceKey(explicit) {
		t.Fatal("equivalent endpoints or credential rotation split the space")
	}
}
