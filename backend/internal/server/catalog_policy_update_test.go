package server

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestImportedCatalogLifecycleDoesNotFollowProviderCatalogSwitch(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "new inventory", true: "legacy inventory"}[legacy], func(t *testing.T) {
			store := NewMemoryStore()
			app := NewWithConfig(store, Config{AdminToken: "dev_admin_token", ProviderCatalogFile: "../../../data/provider-catalog.json"})
			t.Cleanup(func() { _ = app.Shutdown(context.Background()) })
			if _, err := app.InitializeProviderCatalog(context.Background()); err != nil {
				t.Fatal(err)
			}
			const id, upstream = "catalog-switch", "gpt-5.2-chat-latest"
			response := doJSON(t, app.Handler(), http.MethodPost, "/api/admin/providers", map[string]any{
				"id": id, "catalog_id": "openai", "selected_models": []string{upstream},
				"base_url": "https://api.openai.com/v1", "api_key": "synthetic-key", "healthy": true,
			}, "")
			if response.Code != http.StatusCreated {
				t.Fatalf("import official inventory: %d %s", response.Code, response.Body)
			}
			for _, model := range store.ListProviderModels() {
				if model.ProviderID == id && !legacy && (model.Metadata["provider_catalog_id"] != "openai" || model.Metadata["provider_model_id"] != upstream) {
					t.Fatal("ordinary catalog import lost its exact provenance")
				}
				if model.ProviderID == id && legacy {
					metadata := cloneStringMap(model.Metadata)
					delete(metadata, "provider_catalog_id")
					delete(metadata, "provider_model_id")
					response = doJSON(t, app.Handler(), http.MethodPatch, "/api/admin/provider-models/"+model.ID, map[string]any{"metadata": metadata}, "")
					if response.Code != http.StatusOK {
						t.Fatal("legacy inventory fixture could not be saved")
					}
				}
			}
			store.AddModel(Model{Name: "independent-chat", Modality: "chat", Status: StatusActive})
			route := ModelRoute{ModelName: "independent-chat", ProviderID: id, ProviderModel: upstream, Status: StatusActive, Weight: 100}
			response = doJSON(t, app.Handler(), http.MethodPost, "/api/admin/routing-rules", route, "")
			if response.Code != http.StatusBadRequest || AsHTTPError(app.validateCatalogModelPublication(Provider{ID: id, Options: map[string]string{"catalog_id": "openai"}}, route)).Code != "provider_model_retired" {
				t.Fatalf("official retirement was not enforced: %d %s", response.Code, response.Body)
			}
			response = doJSON(t, app.Handler(), http.MethodPatch, "/api/admin/providers/"+id, map[string]any{
				"catalog_id": "azure-openai", "type": ProviderAzureOpenAI, "healthy": true,
				"base_url": "https://synthetic-resource.openai.azure.com", "api_key": "synthetic-key",
			}, "")
			if response.Code != http.StatusOK {
				t.Fatalf("switch provider catalog: %d %s", response.Code, response.Body)
			}
			response = doJSON(t, app.Handler(), http.MethodPost, "/api/admin/routing-rules", route, "")
			if response.Code != http.StatusCreated {
				t.Fatalf("old official retirement followed independent catalog: %d %s", response.Code, response.Body)
			}
		})
	}
}

func TestCurrentCatalogCanWithdrawPublicationRestrictions(t *testing.T) {
	for _, test := range []struct {
		name         string
		old, current map[string]string
	}{
		{"call support", map[string]string{"call_support": "unsupported"}, map[string]string{"lifecycle_status": "active"}},
		{"retirement", map[string]string{"lifecycle_status": "retired"}, map[string]string{}},
		{"shutdown cutoff", map[string]string{"lifecycle_status": "deprecated", "shutdown_at": "2020-01-01T00:00:00Z"}, map[string]string{"lifecycle_status": "deprecated"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := NewMemoryStore()
			provider := store.AddProvider(Provider{ID: "catalog-update", Type: ProviderOpenAICompatible, Status: StatusActive, Healthy: true, Options: map[string]string{"catalog_id": "official"}})
			store.AddModel(Model{Name: "current-chat", Modality: "chat", Status: StatusActive})
			metadata := cloneStringMap(test.old)
			metadata["provider_catalog_id"], metadata["provider_model_id"] = "official", "model"
			metadata["pricing_status"], metadata["custom"] = "configured", "keep"
			store.AddProviderModel(ProviderModel{ProviderID: provider.ID, UpstreamModel: "model", Modality: "chat", Status: StatusActive, InputPriceUSDPer1M: 2, Metadata: metadata})
			app := New(store)
			t.Cleanup(func() { _ = app.Shutdown(context.Background()) })
			save := func(policy map[string]string) {
				t.Helper()
				if err := store.SaveProviderCatalogSnapshot([]ProviderCatalogEntry{{ID: "official", Models: []ProviderCatalogModel{{ID: "model", Metadata: policy}}}}, "test", time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			route := ModelRoute{ModelName: "current-chat", ProviderID: provider.ID, ProviderModel: "model", Status: StatusActive, Weight: 100}
			save(test.old)
			response := doJSON(t, app.Handler(), http.MethodPost, "/api/admin/routing-rules", route, "")
			if response.Code != http.StatusBadRequest {
				t.Fatalf("initial restriction was not enforced: %d %s", response.Code, response.Body)
			}
			save(test.current)
			response = doJSON(t, app.Handler(), http.MethodPost, "/api/admin/routing-rules", route, "")
			if response.Code != http.StatusCreated {
				t.Fatalf("withdrawn restriction remained effective: %d %s", response.Code, response.Body)
			}
			for _, model := range store.ListProviderModels() {
				if model.InputPriceUSDPer1M != 2 || model.Metadata["custom"] != "keep" || model.Metadata["pricing_status"] != "configured" || model.Metadata["lifecycle_status"] != metadata["lifecycle_status"] || model.Metadata["call_support"] != metadata["call_support"] {
					t.Fatal("current policy changed persisted costs or annotations")
				}
			}
		})
	}
}
