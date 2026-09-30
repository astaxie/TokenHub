package server

import (
	"context"
	"net/http"
	"testing"
)

// A PATCH can carry runtime metadata without looking up its catalog entry.
// The discovery template must not replace the runtime catalog or its protocol.
func TestRetrievalCatalogSurvivesProviderSettingsPatch(t *testing.T) {
	for _, tc := range []struct {
		name, catalog, configured, embedding, rerank string
	}{
		{"cohere_auto", "cohere", "", "cohere", "cohere"},
		{"voyage_auto", "voyage", "", "voyage", "voyage"},
		{"unknown_auto", "retired-vendor", "", "openai", ""},
		{"custom_auto", "custom", "", "openai", "jina"},
		{"legacy_no_catalog", "", "", "openai", "jina"},
		{"explicit_overrides_catalog", "cohere", "jina", "jina", "jina"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := NewMemoryStore()
			options := map[string]string{
				"catalog_source": "saved-source", "doc_url": "https://docs.example/retrieval",
				"embedding_protocol": tc.configured, "rerank_protocol": tc.configured,
			}
			if tc.catalog != "" {
				options["catalog_id"] = tc.catalog
			}
			provider := store.AddProvider(Provider{ID: "preserved-catalog", Name: "Original", Type: ProviderOpenAICompatible, BaseURL: "https://upstream.example/v1", APIKey: "synthetic-key", Status: StatusActive, Healthy: true, Options: options})
			app := New(store)
			defer func() { _ = app.Shutdown(context.Background()) }()
			resp := doJSON(t, app.Handler(), http.MethodPatch, "/api/admin/providers/"+provider.ID, map[string]any{
				"name": "Updated", "type": ProviderOpenAICompatible,
				"base_url": provider.BaseURL, "catalog_id": "",
				"selected_models": []string{}, "custom_models": []ProviderCatalogModel{},
				"options": options,
			}, "")
			if resp.Code != http.StatusOK {
				t.Fatalf("metadata-preserving patch failed: %d %s", resp.Code, resp.Body)
			}
			saved, ok := store.GetProvider(provider.ID)
			if !ok || saved.Name != "Updated" {
				t.Fatalf("provider edit was not persisted: %+v", saved)
			}
			for _, key := range []string{"catalog_id", "catalog_source", "doc_url"} {
				if saved.Options[key] != options[key] {
					t.Errorf("metadata %s changed: got %q, want %q", key, saved.Options[key], options[key])
				}
			}
			if got := providerEmbeddingProtocol(saved); got != tc.embedding {
				t.Errorf("embedding protocol changed: got %q, want %q", got, tc.embedding)
			}
			if got := providerRerankProtocol(saved); got != tc.rerank {
				t.Errorf("rerank protocol changed: got %q, want %q", got, tc.rerank)
			}
			if got := app.providerRetrievalSupport(saved, "rerank"); got != (tc.rerank != "") {
				t.Errorf("rerank eligibility changed: %v", got)
			}
		})
	}
}

func TestRetrievalCatalogPreservationWithDiscoveryImports(t *testing.T) {
	for _, tc := range []struct {
		name, originalCatalog, targetType  string
		preserve, importModel, omitOptions bool
	}{
		{"cohere_import", "cohere", ProviderOpenAICompatible, true, true, false},
		{"voyage_import", "voyage", ProviderOpenAICompatible, true, true, false},
		{"removed_catalog_import", "retired-vendor", ProviderOpenAICompatible, true, true, false},
		{"cohere_settings_only", "cohere", ProviderOpenAICompatible, true, false, false},
		{"aliased_options", "cohere", ProviderOpenAICompatible, true, true, true},
		{"absent_catalog_keys", "", ProviderOpenAICompatible, true, true, true},
		{"explicit_catalog_change", "cohere", ProviderOpenAICompatible, false, true, false},
		{"explicit_type_change", "cohere", "local", true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := NewMemoryStore()
			original := map[string]string{}
			if tc.originalCatalog != "" {
				original = map[string]string{"catalog_id": tc.originalCatalog, "catalog_source": "saved-source", "doc_url": "https://saved.example/docs"}
			}
			provider := store.AddProvider(Provider{ID: "catalog-import", Name: "Original", Type: ProviderOpenAICompatible, BaseURL: "https://upstream.example/v1", APIKey: "synthetic-key", Status: StatusActive, Healthy: true, Options: cloneStringMap(original)})
			app := New(store)
			defer func() { _ = app.Shutdown(context.Background()) }()
			payload := map[string]any{
				"name": "Updated", "type": tc.targetType, "catalog_id": "custom", "preserve_catalog": tc.preserve,
			}
			if !tc.omitOptions {
				payload["options"] = map[string]string{"embedding_protocol": "", "rerank_protocol": "", "rerank_path": "/rerank"}
			}
			if tc.importModel {
				payload["selected_models"] = []string{"new-private-reranker"}
				payload["custom_models"] = []ProviderCatalogModel{{ID: "new-private-reranker", Type: "rerank", InputPriceUSDPer1M: 1}}
			}
			resp := doJSON(t, app.Handler(), http.MethodPatch, "/api/admin/providers/"+provider.ID, payload, "")
			if resp.Code != http.StatusOK {
				t.Fatalf("discovery patch failed: %d %s", resp.Code, resp.Body)
			}
			saved, ok := store.GetProvider(provider.ID)
			if !ok || saved.Name != "Updated" || saved.Type != tc.targetType {
				t.Fatalf("provider edit was not persisted: %+v", saved)
			}
			if tc.preserve && tc.targetType == provider.Type {
				for _, key := range []string{"catalog_id", "catalog_source", "doc_url"} {
					value, exists := saved.Options[key]
					want, wantExists := original[key]
					if value != want || exists != wantExists {
						t.Errorf("runtime metadata %s changed: got %q/%v, want %q/%v", key, value, exists, want, wantExists)
					}
				}
				if providerEmbeddingProtocol(saved) != providerEmbeddingProtocol(provider) || providerRerankProtocol(saved) != providerRerankProtocol(provider) {
					t.Fatal("discovery import changed the automatic retrieval protocol")
				}
			} else if saved.Options["catalog_id"] != "custom" {
				t.Fatalf("explicit catalog/type change was ignored: %+v", saved.Options)
			}
			models := store.ListProviderModels()
			if tc.importModel {
				if len(models) != 1 || models[0].UpstreamModel != "new-private-reranker" || models[0].ProviderID != provider.ID {
					t.Fatalf("live-discovered model was not imported: %+v", models)
				}
			} else if len(models) != 0 {
				t.Fatalf("settings-only patch imported models: %+v", models)
			}
		})
	}
}

func TestRetrievalCatalogPreservationDoesNotAffectCreate(t *testing.T) {
	store := NewMemoryStore()
	app := New(store)
	defer func() { _ = app.Shutdown(context.Background()) }()
	resp := doJSON(t, app.Handler(), http.MethodPost, "/api/admin/providers", map[string]any{
		"id": "new-catalog-provider", "name": "New", "type": ProviderOpenAICompatible,
		"base_url": "https://upstream.example/v1", "api_key": "synthetic-key",
		"catalog_id": "custom", "preserve_catalog": true,
		"options": map[string]string{"catalog_id": "cohere"},
	}, "")
	if resp.Code != http.StatusCreated {
		t.Fatalf("provider creation failed: %d %s", resp.Code, resp.Body)
	}
	saved, ok := store.GetProvider("new-catalog-provider")
	if !ok || saved.Options["catalog_id"] != "custom" {
		t.Fatalf("patch-only flag affected creation: %+v", saved)
	}
}
