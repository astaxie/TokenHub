package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestForeignTemplateRetirementDoesNotBlockAzurePublication(t *testing.T) {
	models, err := defaultModelCatalog("../../../data/model-catalog.yaml")
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryStore()
	const upstream = "gpt-5.2-chat-latest"
	found := false
	for _, model := range models {
		if model.Name == upstream {
			if model.Metadata["provider_catalog_id"] == "" || model.Metadata["provider_catalog_id"] == "azure-openai" || !catalogModelRetired(model.Metadata, time.Now()) {
				t.Fatal("fixture must carry the actual OpenAI retirement annotation")
			}
			store.AddModel(model)
			found = true
		}
	}
	if !found {
		t.Fatal("standard template not found")
	}
	store.AddModel(Model{Name: "independent-deployment", Modality: "chat", Status: StatusActive})
	app := New(store)
	t.Cleanup(func() { _ = app.Shutdown(context.Background()) })
	response := doJSON(t, app.Handler(), http.MethodPost, "/api/admin/providers", map[string]any{
		"id": "azure-independent", "catalog_id": "azure-openai", "selected_models": []string{upstream},
		"base_url": "https://synthetic-resource.openai.azure.com", "api_key": "synthetic-key", "healthy": true,
	}, "")
	if response.Code != http.StatusCreated {
		t.Fatalf("create Azure provider: %d %s", response.Code, response.Body)
	}
	var result ProviderCreateResult
	if err := json.Unmarshal([]byte(response.Body), &result); err != nil {
		t.Fatal(err)
	}
	if result.ImportedModels != 1 {
		t.Fatalf("standard template was not imported: %+v", result)
	}
	response = doJSON(t, app.Handler(), http.MethodPost, "/api/admin/routing-rules", ModelRoute{
		ModelName: "independent-deployment", ProviderID: result.Provider.ID, ProviderModel: upstream,
		Status: StatusActive, Weight: 100,
	}, "")
	if response.Code != http.StatusCreated {
		t.Fatalf("foreign retirement blocked independent deployment: %d %s", response.Code, response.Body)
	}
	for _, saved := range store.ListProviderModels() {
		if saved.ProviderID == result.Provider.ID && saved.Metadata["lifecycle_status"] != "retired" {
			t.Fatal("publication rewrote stored inventory annotations")
		}
	}
}

func TestCatalogLifecycleAnnotationsRequireMatchingTemplateProvenance(t *testing.T) {
	index := catalogAdvisoryIndex{"openai": {"model": {"lifecycle_status": "retired"}}}
	for _, test := range []struct {
		name, targetCatalog, sourceModel string
		wantRetired                      bool
	}{
		{"same offer", "openai", "model", true},
		{"Azure offer", "azure-openai", "model", false},
		{"custom offer", "", "model", false},
		{"different model", "openai", "other-model", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := ProviderModel{UpstreamModel: test.sourceModel, Metadata: map[string]string{
				"provider_catalog_id": "openai", "provider_model_id": "model", "lifecycle_status": "retired",
				"shutdown_at": "2020-01-01T00:00:00Z", "replacement_model": "next", "lifecycle_source": "https://example.com/retired",
				"pricing_status": "configured", "call_support": "unsupported", "custom": "keep",
			}}
			effective := index.apply(Provider{Options: map[string]string{"catalog_id": test.targetCatalog}}, model)
			if catalogModelRetired(effective.Metadata, time.Now()) != test.wantRetired {
				t.Fatalf("incorrect lifecycle scope: %+v", effective.Metadata)
			}
			if !test.wantRetired && (effective.Metadata["shutdown_at"] != "" || effective.Metadata["replacement_model"] != "" || effective.Metadata["lifecycle_source"] != "") {
				t.Fatal("foreign lifecycle annotations remained in the effective offer")
			}
			if effective.Metadata["pricing_status"] != "configured" || effective.Metadata["custom"] != "keep" || effective.Metadata["call_support"] != "unsupported" || model.Metadata["lifecycle_status"] != "retired" {
				t.Fatal("scope correction changed costs, call support, custom metadata, or stored inventory")
			}
		})
	}
	foreign := ProviderModel{UpstreamModel: "model", Metadata: map[string]string{
		"provider_catalog_id": "openai", "provider_model_id": "model", "lifecycle_status": "retired",
	}}
	for _, status := range []string{"active", "retired"} {
		index := catalogAdvisoryIndex{"azure-openai": {"model": {"lifecycle_status": status}}}
		effective := index.apply(Provider{Options: map[string]string{"catalog_id": "azure-openai"}}, foreign)
		if effective.Metadata["lifecycle_status"] != status {
			t.Fatalf("foreign template displaced the target catalog's %s policy", status)
		}
	}
}
