package server

import (
	"context"
	"net/http"
	"testing"
)

func TestGeminiArgonStandardTemplatePublishesThroughAdminAPI(t *testing.T) {
	models, err := defaultModelCatalog("../../../data/model-catalog.yaml")
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryStore()
	found := false
	for _, model := range models {
		if model.Name == "gemini-4-argon" {
			store.AddModel(model)
			found = true
		}
	}
	if !found {
		t.Fatal("standard Argon template is missing")
	}
	app := NewWithConfig(store, Config{AdminToken: "dev_admin_token", ProviderCatalogFile: "../../../data/provider-catalog.json"})
	t.Cleanup(func() { _ = app.Shutdown(context.Background()) })
	if _, err := app.InitializeProviderCatalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	const providerID = "argon-publication"
	response := doJSON(t, app.Handler(), http.MethodPost, "/api/admin/providers", map[string]any{
		"id": providerID, "catalog_id": "google", "selected_models": []string{"gemini-4-argon"},
		"base_url": "https://generativelanguage.googleapis.com/v1beta", "api_key": "synthetic-key", "healthy": true,
	}, "")
	if response.Code != http.StatusCreated {
		t.Fatalf("import Argon inventory: %d %s", response.Code, response.Body)
	}
	for _, model := range store.ListProviderModels() {
		if model.ProviderID != providerID {
			continue
		}
		metadata := cloneStringMap(model.Metadata)
		metadata["pricing_status"] = "configured"
		response = doJSON(t, app.Handler(), http.MethodPatch, "/api/admin/provider-models/"+model.ID, map[string]any{
			"input_price_usd_per_1m": 2, "output_price_usd_per_1m": 10, "metadata": metadata,
		}, "")
		if response.Code != http.StatusOK {
			t.Fatalf("confirm Argon costs: %d %s", response.Code, response.Body)
		}
	}
	response = doJSON(t, app.Handler(), http.MethodPost, "/api/admin/routing-rules", ModelRoute{
		ModelName: "gemini-4-argon", ProviderID: providerID, ProviderModel: "gemini-4-argon", Status: StatusActive, Weight: 100,
	}, "")
	if response.Code != http.StatusCreated {
		t.Fatalf("publish standard Argon route through native Gemini provider: %d %s", response.Code, response.Body)
	}
}
