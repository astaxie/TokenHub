package server

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProviderCreationImportsModelsWithoutImplicitRoutes(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	resp := doJSON(t, app, http.MethodPost, "/api/admin/providers", map[string]any{
		"id":              "prv_inventory_default",
		"catalog_id":      "openai",
		"name":            "Inventory Default",
		"type":            ProviderOpenAI,
		"base_url":        "https://api.openai.com/v1",
		"status":          StatusActive,
		"selected_models": []string{"gpt-4.1-mini"},
	}, "")
	if resp.Code != http.StatusCreated {
		t.Fatalf("expected provider creation 201, got %d: %s", resp.Code, resp.Body)
	}
	var result ProviderCreateResult
	if err := json.Unmarshal([]byte(resp.Body), &result); err != nil {
		t.Fatal(err)
	}
	if result.ImportedModels != 1 {
		t.Fatalf("provider creation should import inventory without publishing routes: %+v", result)
	}
	for _, route := range store.ListRoutes() {
		if route.ProviderID == "prv_inventory_default" {
			t.Fatalf("provider creation implicitly published route: %+v", route)
		}
	}
}

func TestProviderCreationRequiresValidCatalogModelSelection(t *testing.T) {
	for _, selectedModels := range [][]string{nil, {"not-in-catalog"}} {
		store := NewMemoryStore()
		if err := SeedDemoData(store); err != nil {
			t.Fatal(err)
		}
		app := New(store).Handler()

		resp := doJSON(t, app, http.MethodPost, "/api/admin/providers", map[string]any{
			"id":              "prv_inventory_required",
			"catalog_id":      "openai",
			"name":            "Inventory Required",
			"type":            ProviderOpenAI,
			"base_url":        "https://api.openai.com/v1",
			"status":          StatusActive,
			"selected_models": selectedModels,
		}, "")
		if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body, `"code":"provider_models_required"`) {
			t.Fatalf("expected provider model selection error, got %d: %s", resp.Code, resp.Body)
		}
		if _, ok := store.GetProvider("prv_inventory_required"); ok {
			t.Fatal("provider was created before model selection validation")
		}
	}
}

func TestCustomProviderCreationRequiresDiscoveredModelSelection(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	resp := doJSON(t, app, http.MethodPost, "/api/admin/providers", map[string]any{
		"id":         "prv_custom_inventory_required",
		"catalog_id": "custom",
		"name":       "Custom Inventory Required",
		"type":       ProviderOpenAICompatible,
		"base_url":   "https://custom.example/v1",
		"status":     StatusActive,
		"custom_models": []map[string]any{{
			"id": "custom-chat-model",
		}},
	}, "")
	if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body, `"code":"provider_models_required"`) {
		t.Fatalf("expected custom Provider model selection error, got %d: %s", resp.Code, resp.Body)
	}
	if _, ok := store.GetProvider("prv_custom_inventory_required"); ok {
		t.Fatal("custom Provider was created before discovered model selection validation")
	}
}

func TestUsageRecordsExternalChargeAndProviderCostSeparately(t *testing.T) {
	store := NewMemoryStore()
	external := store.AddModel(Model{
		Name:                   "public-model",
		Modality:               "chat",
		InputPriceUSDPer1M:     10,
		CacheReadPriceUSDPer1M: 2,
		OutputPriceUSDPer1M:    30,
		Status:                 StatusActive,
	})
	provider := store.AddProvider(Provider{
		ID: "prv_cost_audit", Name: "Cost Audit", Type: ProviderMock, Status: StatusActive, Healthy: true,
	})
	store.AddProviderModel(ProviderModel{
		ProviderID:             provider.ID,
		UpstreamModel:          "upstream-model",
		Modality:               "chat",
		InputPriceUSDPer1M:     1,
		CacheReadPriceUSDPer1M: 0.2,
		OutputPriceUSDPer1M:    3,
		Status:                 StatusActive,
	})

	store.FinishCall(
		CallContext{RequestID: "req_dual_price", Model: external, StartedAt: time.Now()},
		RouteSelection{Provider: provider, ProviderModel: "upstream-model"},
		Usage{PromptTokens: 1_000_000, CachedInputTokens: 250_000, CompletionTokens: 1_000_000},
		http.StatusOK,
		"",
		"127.0.0.1",
		"provider-cost-test",
	)

	records := store.ListUsageRecords()
	if len(records) != 1 {
		t.Fatalf("expected one usage record, got %d", len(records))
	}
	if math.Abs(records[0].CostUSD-38) > 1e-12 {
		t.Fatalf("external charge = %.12f, want 38", records[0].CostUSD)
	}
	if math.Abs(records[0].ProviderCostUSD-3.8) > 1e-12 {
		t.Fatalf("provider cost = %.12f, want 3.8", records[0].ProviderCostUSD)
	}
}

func TestProviderCostDoesNotEstimateAnUnconfiguredCachePrice(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{ID: "prv_exact_cost", Name: "Exact Cost", Type: ProviderMock, Status: StatusActive})
	store.AddProviderModel(ProviderModel{
		ProviderID: provider.ID, UpstreamModel: "exact-model", Modality: "chat", Status: StatusActive,
		InputPriceUSDPer1M: 1, CacheReadPriceUSDPer1M: 0, OutputPriceUSDPer1M: 2,
	})
	cost := store.providerCostUSD(
		RouteSelection{Provider: provider, ProviderModel: "exact-model"},
		Usage{PromptTokens: 1_000_000, CachedInputTokens: 1_000_000, TotalTokens: 1_000_000},
	)
	if cost != 0 {
		t.Fatalf("unconfigured Provider cache cost = %.12f, want 0 without estimation", cost)
	}
}

func TestProviderModelCostPatchCanClearPrices(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{ID: "prv_clear_cost", Name: "Clear Cost", Type: ProviderMock, Status: StatusActive})
	model := store.AddProviderModel(ProviderModel{
		ProviderID: provider.ID, UpstreamModel: "priced-model", Status: StatusActive,
		InputPriceUSDPer1M: 1, CacheReadPriceUSDPer1M: 0.5, OutputPriceUSDPer1M: 2,
	})

	resp := doJSON(t, New(store).Handler(), http.MethodPatch, "/api/admin/provider-models/"+model.ID, map[string]any{
		"input_price_usd_per_1m":      0,
		"cache_read_price_usd_per_1m": 0,
		"output_price_usd_per_1m":     0,
	}, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected provider cost update 200, got %d: %s", resp.Code, resp.Body)
	}
	updated := store.ListProviderModels()[0]
	if updated.InputPriceUSDPer1M != 0 || updated.CacheReadPriceUSDPer1M != 0 || updated.OutputPriceUSDPer1M != 0 {
		t.Fatalf("provider prices were not cleared: %+v", updated)
	}
}

func TestProviderCostsAreRedactedOutsideGlobalOperations(t *testing.T) {
	server := New(NewMemoryStore())
	userDetail := map[string]any{"usage": []UsageRecord{{CostUSD: 12, ProviderCostUSD: 4}}}
	server.redactProviderCostsForUser(AdminUser{Role: "user"}, userDetail)
	userUsage := userDetail["usage"].([]UsageRecord)
	if userUsage[0].CostUSD != 12 || userUsage[0].ProviderCostUSD != 0 {
		t.Fatalf("non-global request detail leaked provider cost: %+v", userUsage[0])
	}

	adminDetail := map[string]any{"usage": []UsageRecord{{CostUSD: 12, ProviderCostUSD: 4}}}
	server.redactProviderCostsForUser(AdminUser{Role: "admin"}, adminDetail)
	adminUsage := adminDetail["usage"].([]UsageRecord)
	if adminUsage[0].ProviderCostUSD != 4 {
		t.Fatalf("global operations user lost provider cost: %+v", adminUsage[0])
	}
}

func TestProviderCostIsNeverSerializedInPublicUsage(t *testing.T) {
	payload, err := json.Marshal(Usage{CostUSD: 12, ProviderCostUSD: 4})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "provider_cost") {
		t.Fatalf("public usage leaked Provider cost: %s", payload)
	}
}

func TestAdminRejectsNegativeProviderModelCostWithoutPersistingIt(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	providerModel := store.AddProviderModel(ProviderModel{
		ProviderID:             "prv_mock",
		UpstreamModel:          "cost-validation-model",
		InputPriceUSDPer1M:     1,
		CacheReadPriceUSDPer1M: 0.5,
		OutputPriceUSDPer1M:    2,
		Status:                 StatusActive,
	})
	app := New(store).Handler()

	updated := doJSON(t, app, http.MethodPatch, "/api/admin/provider-models/"+providerModel.ID, map[string]any{
		"input_price_usd_per_1m":      -1,
		"cache_read_price_usd_per_1m": 0.5,
		"output_price_usd_per_1m":     2,
	}, "")
	if updated.Code != http.StatusBadRequest || !strings.Contains(updated.Body, "invalid_provider_model_cost") {
		t.Fatalf("expected invalid Provider cost response, got %d: %s", updated.Code, updated.Body)
	}

	models := doJSON(t, app, http.MethodGet, "/api/admin/provider-models?provider_id=prv_mock", nil, "")
	if models.Code != http.StatusOK {
		t.Fatalf("expected Provider model list, got %d: %s", models.Code, models.Body)
	}
	var payload struct {
		Data []ProviderModel `json:"data"`
	}
	if err := json.Unmarshal([]byte(models.Body), &payload); err != nil {
		t.Fatal(err)
	}
	for _, model := range payload.Data {
		if model.ID == providerModel.ID && model.InputPriceUSDPer1M != 1 {
			t.Fatalf("invalid Provider cost was persisted: %+v", model)
		}
	}
}

func TestProviderCreationRejectsNegativeImportedModelCost(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	created := doJSON(t, app, http.MethodPost, "/api/admin/providers", map[string]any{
		"id":              "prv_invalid_cost",
		"catalog_id":      "custom",
		"name":            "Invalid Cost Provider",
		"type":            ProviderOpenAICompatible,
		"base_url":        "https://invalid.example/v1",
		"status":          StatusActive,
		"selected_models": []string{"negative-cost-model"},
		"custom_models": []map[string]any{{
			"id":                          "negative-cost-model",
			"input_price_usd_per_1m":      -0.01,
			"output_price_usd_per_1m":     1,
			"cache_read_price_usd_per_1m": 0,
		}},
	}, "")
	if created.Code != http.StatusBadRequest || !strings.Contains(created.Body, "invalid_provider_model_cost") {
		t.Fatalf("expected invalid imported Provider cost response, got %d: %s", created.Code, created.Body)
	}
	if _, ok := store.GetProvider("prv_invalid_cost"); ok {
		t.Fatal("invalid imported Provider cost must not persist a Provider")
	}
	for _, model := range store.ListProviderModels() {
		if model.ProviderID == "prv_invalid_cost" {
			t.Fatalf("invalid imported Provider cost was persisted: %+v", model)
		}
	}
}

func TestProviderModelImportRejectsNegativeCost(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	imported := doJSON(t, app, http.MethodPost, "/api/admin/provider-models/import", map[string]any{
		"provider_id": "prv_mock",
		"models": []map[string]any{{
			"id":                          "invalid-import-cost",
			"input_price_usd_per_1m":      1,
			"output_price_usd_per_1m":     -2,
			"cache_read_price_usd_per_1m": 0,
		}},
	}, "")
	if imported.Code != http.StatusBadRequest || !strings.Contains(imported.Body, "invalid_provider_model_cost") {
		t.Fatalf("expected invalid imported Provider cost response, got %d: %s", imported.Code, imported.Body)
	}
	for _, model := range store.ListProviderModels() {
		if model.ProviderID == "prv_mock" && model.UpstreamModel == "invalid-import-cost" {
			t.Fatalf("invalid imported Provider cost was persisted: %+v", model)
		}
	}
}

func TestAdminRejectsNonFiniteProviderModelCostWithoutPersistingIt(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	providerModel := store.AddProviderModel(ProviderModel{
		ProviderID:             "prv_mock",
		UpstreamModel:          "non-finite-cost-model",
		InputPriceUSDPer1M:     1,
		CacheReadPriceUSDPer1M: 0.5,
		OutputPriceUSDPer1M:    2,
		Status:                 StatusActive,
	})
	app := New(store).Handler()

	req := httptest.NewRequest(
		http.MethodPatch,
		"/api/admin/provider-models/"+providerModel.ID,
		strings.NewReader(`{"input_price_usd_per_1m":1e309,"cache_read_price_usd_per_1m":0.5,"output_price_usd_per_1m":2}`),
	)
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer dev_admin_token")
	resp := httptest.NewRecorder()
	app.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "invalid_provider_model_cost") {
		t.Fatalf("expected non-finite Provider cost response, got %d: %s", resp.Code, resp.Body)
	}
	for _, model := range store.ListProviderModels() {
		if model.ID == providerModel.ID && model.InputPriceUSDPer1M != 1 {
			t.Fatalf("non-finite Provider cost was persisted: %+v", model)
		}
	}
}

func TestAdminProviderModelPartialPatchPreservesConfiguredCosts(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	providerModel := store.AddProviderModel(ProviderModel{
		ProviderID:             "prv_mock",
		UpstreamModel:          "partial-cost-model",
		InputPriceUSDPer1M:     1.25,
		CacheReadPriceUSDPer1M: 0.25,
		OutputPriceUSDPer1M:    3.5,
		Status:                 StatusActive,
	})
	app := New(store).Handler()

	updated := doJSON(t, app, http.MethodPatch, "/api/admin/provider-models/"+providerModel.ID, map[string]any{
		"status": StatusDisabled,
	}, "")
	if updated.Code != http.StatusOK {
		t.Fatalf("expected partial Provider model patch 200, got %d: %s", updated.Code, updated.Body)
	}
	var result ProviderModel
	if err := json.Unmarshal([]byte(updated.Body), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusDisabled || result.InputPriceUSDPer1M != 1.25 ||
		result.CacheReadPriceUSDPer1M != 0.25 || result.OutputPriceUSDPer1M != 3.5 {
		t.Fatalf("partial Provider model patch erased configured costs: %+v", result)
	}
}
