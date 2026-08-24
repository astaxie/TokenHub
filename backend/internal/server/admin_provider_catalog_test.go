package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestAdminCreatesProviderModelAndRoute(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	providerResp := doJSON(t, app, http.MethodPost, "/api/admin/providers", map[string]any{
		"name":     "Local vLLM",
		"type":     "local",
		"base_url": "http://localhost:8000/v1",
		"status":   "active",
		"healthy":  true,
		"priority": 2,
	}, "")
	if providerResp.Code != http.StatusCreated {
		t.Fatalf("expected provider created, got %d: %s", providerResp.Code, providerResp.Body)
	}
	var providerPayload struct {
		Provider Provider `json:"provider"`
	}
	if err := json.Unmarshal([]byte(providerResp.Body), &providerPayload); err != nil {
		t.Fatal(err)
	}
	provider := providerPayload.Provider
	for _, upstreamModel := range []string{"qwen2.5-coder", "qwen2.5-coder-backup"} {
		store.AddProviderModel(ProviderModel{
			ProviderID:    provider.ID,
			UpstreamModel: upstreamModel,
			DisplayName:   upstreamModel,
			Status:        StatusActive,
		})
	}

	modelResp := doJSON(t, app, http.MethodPost, "/api/admin/models", map[string]any{
		"name":                    "local-coder",
		"family":                  "qwen",
		"modality":                "chat",
		"context_window":          32768,
		"input_price_usd_per_1m":  0.1,
		"output_price_usd_per_1m": 0.2,
	}, "")
	if modelResp.Code != http.StatusCreated {
		t.Fatalf("expected model created, got %d: %s", modelResp.Code, modelResp.Body)
	}

	routeResp := doJSON(t, app, http.MethodPost, "/api/admin/routing-rules", map[string]any{
		"model_name":     "local-coder",
		"provider_id":    provider.ID,
		"provider_model": "qwen2.5-coder",
		"priority":       1,
		"weight":         100,
		"status":         "active",
	}, "")
	if routeResp.Code != http.StatusCreated {
		t.Fatalf("expected route created, got %d: %s", routeResp.Code, routeResp.Body)
	}

	secondRouteResp := doJSON(t, app, http.MethodPost, "/api/admin/routing-rules", map[string]any{
		"model_name":     "local-coder",
		"provider_id":    provider.ID,
		"provider_model": "qwen2.5-coder-backup",
		"weight":         100,
		"status":         "active",
	}, "")
	if secondRouteResp.Code != http.StatusCreated {
		t.Fatalf("expected second route created, got %d: %s", secondRouteResp.Code, secondRouteResp.Body)
	}
	var secondRoute ModelRoute
	if err := json.Unmarshal([]byte(secondRouteResp.Body), &secondRoute); err != nil {
		t.Fatal(err)
	}
	if secondRoute.Priority != 2 {
		t.Fatalf("expected second route to append with priority 2, got %d: %s", secondRoute.Priority, secondRouteResp.Body)
	}

	routes := doJSON(t, app, http.MethodGet, "/api/admin/routing-rules", nil, "")
	if routes.Code != http.StatusOK {
		t.Fatalf("expected routes list, got %d: %s", routes.Code, routes.Body)
	}
	if !strings.Contains(routes.Body, "local-coder") || !strings.Contains(routes.Body, "qwen2.5-coder") {
		t.Fatalf("expected new route in list: %s", routes.Body)
	}
}

func TestAdminRouteRejectsKnownModelProviderProtocolMismatch(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "prv_native_anthropic", Name: "Native Anthropic", Type: ProviderAnthropic, Status: StatusActive, Healthy: true})
	store.AddProviderModel(ProviderModel{ProviderID: provider.ID, UpstreamModel: "gpt-5.6-luna", Status: StatusActive})
	store.AddModel(Model{Name: "gpt-5.6-luna", Modality: "chat", Status: StatusActive, Metadata: map[string]string{"endpoints": "responses,chat/completions"}})

	response := doJSON(t, New(store).Handler(), http.MethodPost, "/api/admin/routing-rules", map[string]any{
		"model_name": "gpt-5.6-luna", "provider_id": provider.ID, "provider_model": "gpt-5.6-luna", "status": StatusActive,
	}, "")
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body, `"code":"route_protocol_mismatch"`) {
		t.Fatalf("expected protocol mismatch rejection, got %d: %s", response.Code, response.Body)
	}
}

func TestAdminModelCreateRejectsInitialRouteProtocolMismatch(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "prv_initial_anthropic", Name: "Native Anthropic", Type: ProviderAnthropic, Status: StatusActive, Healthy: true})
	store.AddProviderModel(ProviderModel{ProviderID: provider.ID, UpstreamModel: "responses-only", Status: StatusActive})

	response := doJSON(t, New(store).Handler(), http.MethodPost, "/api/admin/models", map[string]any{
		"name": "responses-only", "modality": "chat", "metadata": map[string]string{"endpoints": "responses"},
		"routes": []map[string]any{{"provider_id": provider.ID, "provider_model": "responses-only", "status": StatusActive}},
	}, "")
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body, `"code":"route_protocol_mismatch"`) {
		t.Fatalf("expected initial route protocol mismatch rejection, got %d: %s", response.Code, response.Body)
	}
}

func TestAdminRouteAllowsCatalogModelWithMatchingProviderProtocol(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "prv_protocol_openai", Name: "OpenAI Compatible", Type: ProviderOpenAICompatible, Status: StatusActive, Healthy: true})
	store.AddProviderModel(ProviderModel{ProviderID: provider.ID, UpstreamModel: "gpt-5.6-luna", Status: StatusActive})
	store.AddModel(Model{Name: "gpt-5.6-luna", Modality: "chat", Status: StatusActive, Metadata: map[string]string{"endpoints": "responses,chat/completions"}})

	response := doJSON(t, New(store).Handler(), http.MethodPost, "/api/admin/routing-rules", map[string]any{
		"model_name": "gpt-5.6-luna", "provider_id": provider.ID, "provider_model": "gpt-5.6-luna", "status": StatusActive,
	}, "")
	if response.Code != http.StatusCreated {
		t.Fatalf("expected compatible route creation, got %d: %s", response.Code, response.Body)
	}
}

func TestAdminProviderConfigurationFailsEarlyAndPatchPreservesFields(t *testing.T) {
	app := newTestServer()

	invalid := doJSON(t, app, http.MethodPost, "/api/admin/providers", map[string]any{
		"name":     "Invalid Adapter",
		"type":     "openai-compatible",
		"base_url": "https://example.invalid/v1",
		"healthy":  true,
	}, "")
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body, `"code":"provider_adapter_missing"`) {
		t.Fatalf("expected unknown adapter to fail during creation, got %d: %s", invalid.Code, invalid.Body)
	}

	created := doJSON(t, app, http.MethodPost, "/api/admin/providers", map[string]any{
		"id":       "prv_patch_preserve",
		"name":     "Patch Preserve",
		"type":     ProviderOpenAICompatible,
		"base_url": "https://example.invalid/v1",
		"status":   StatusActive,
		"healthy":  true,
		"priority": 7,
		"headers":  map[string]string{"x-provider": "preserved"},
		"options":  map[string]string{"region": "test"},
	}, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("expected provider creation, got %d: %s", created.Code, created.Body)
	}

	updated := doJSON(t, app, http.MethodPatch, "/api/admin/providers/prv_patch_preserve", map[string]any{
		"type": "deepseek",
	}, "")
	if updated.Code != http.StatusOK {
		t.Fatalf("expected partial provider patch, got %d: %s", updated.Code, updated.Body)
	}
	var result ProviderCreateResult
	if err := json.Unmarshal([]byte(updated.Body), &result); err != nil {
		t.Fatal(err)
	}
	provider := result.Provider
	if provider.Type != "deepseek" ||
		provider.Name != "Patch Preserve" ||
		provider.BaseURL != "https://example.invalid/v1" ||
		provider.Status != StatusActive ||
		!provider.Healthy ||
		provider.Priority != 7 ||
		provider.Headers["x-provider"] != "preserved" ||
		provider.Options["region"] != "test" {
		t.Fatalf("partial patch erased provider fields: %+v", provider)
	}
}

func TestAdminTestsUnsavedProviderConnection(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Errorf("unexpected upstream request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		if got := r.Header.Get("authorization"); got != "Bearer test-secret" {
			t.Errorf("authorization = %q, want Bearer test-secret", got)
			http.Error(w, "unexpected authorization", http.StatusUnauthorized)
			return
		}
		time.Sleep(5 * time.Millisecond)
		writeJSON(w, http.StatusOK, map[string]any{
			"data": []map[string]any{{"id": "deepseek-chat"}},
		})
	}))
	defer upstream.Close()

	app := newTestServer()
	resp := doJSON(t, app, http.MethodPost, "/api/admin/providers/test-connection", map[string]any{
		"name":     "DeepSeek",
		"type":     "deepseek",
		"base_url": upstream.URL + "/v1",
		"api_key":  "test-secret",
	}, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected connection test 200, got %d: %s", resp.Code, resp.Body)
	}
	var result struct {
		Healthy     bool  `json:"healthy"`
		LatencyMS   int64 `json:"latency_ms"`
		ModelsCount int   `json:"models_count"`
	}
	if err := json.Unmarshal([]byte(resp.Body), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Healthy || result.LatencyMS < 1 || result.ModelsCount != 1 {
		t.Fatalf("unexpected connection result: %+v", result)
	}
}

func TestAdminTestsUnsavedAnthropicProviderAuthentication(t *testing.T) {
	for _, testCase := range []struct {
		name              string
		authType          string
		wantAuthorization string
		wantAPIKey        string
	}{
		{name: "default x-api-key", wantAPIKey: "test-secret"},
		{name: "bearer", authType: anthropicAuthTypeBearer, wantAuthorization: "Bearer test-secret"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("authorization"); got != testCase.wantAuthorization {
					t.Errorf("authorization = %q, want %q", got, testCase.wantAuthorization)
				}
				if got := r.Header.Get("x-api-key"); got != testCase.wantAPIKey {
					t.Errorf("x-api-key = %q, want %q", got, testCase.wantAPIKey)
				}
				writeJSON(w, http.StatusOK, map[string]any{
					"data": []map[string]any{{"id": "claude-test"}},
				})
			}))
			defer upstream.Close()

			app := newTestServer()
			resp := doJSON(t, app, http.MethodPost, "/api/admin/providers/test-connection", map[string]any{
				"name":                "Anthropic",
				"type":                ProviderAnthropic,
				"base_url":            upstream.URL + "/v1",
				"api_key":             "test-secret",
				"anthropic_auth_type": testCase.authType,
			}, "")
			if resp.Code != http.StatusOK {
				t.Fatalf("expected connection test 200, got %d: %s", resp.Code, resp.Body)
			}
		})
	}
}

func TestAdminSavedAnthropicProviderCatalogAuthentication(t *testing.T) {
	seenHeaders := make(chan http.Header, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenHeaders <- r.Header.Clone()
		writeJSON(w, http.StatusOK, map[string]any{
			"data": []map[string]any{{"id": "claude-test"}},
		})
	}))
	defer upstream.Close()

	app := newTestServer()
	created := doJSON(t, app, http.MethodPost, "/api/admin/providers", map[string]any{
		"id":                  "prv_anthropic_bearer",
		"name":                "Anthropic Bearer",
		"type":                ProviderAnthropic,
		"base_url":            upstream.URL + "/v1",
		"api_key":             "saved-secret",
		"anthropic_auth_type": anthropicAuthTypeBearer,
	}, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("expected provider creation 201, got %d: %s", created.Code, created.Body)
	}

	for _, testCase := range []struct {
		name              string
		authType          string
		wantAuthorization string
		wantAPIKey        string
	}{
		{name: "inherit saved bearer", wantAuthorization: "Bearer saved-secret"},
		{name: "explicit x-api-key override", authType: anthropicAuthTypeAPIKey, wantAPIKey: "saved-secret"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			body := map[string]any{"provider_id": "prv_anthropic_bearer"}
			if testCase.authType != "" {
				body["anthropic_auth_type"] = testCase.authType
			}
			resp := doJSON(t, app, http.MethodPost, "/api/admin/provider-catalog/custom", body, "")
			if resp.Code != http.StatusOK {
				t.Fatalf("expected saved provider catalog 200, got %d: %s", resp.Code, resp.Body)
			}
			headers := <-seenHeaders
			if got := headers.Get("authorization"); got != testCase.wantAuthorization {
				t.Errorf("authorization = %q, want %q", got, testCase.wantAuthorization)
			}
			if got := headers.Get("x-api-key"); got != testCase.wantAPIKey {
				t.Errorf("x-api-key = %q, want %q", got, testCase.wantAPIKey)
			}
		})
	}
}

func TestAdminValidatesAnthropicProviderAuthentication(t *testing.T) {
	app := newTestServer()
	valid := doJSON(t, app, http.MethodPost, "/api/admin/providers", map[string]any{
		"id":                  "prv_anthropic_bearer",
		"name":                "Anthropic Bearer",
		"type":                ProviderAnthropic,
		"base_url":            "https://example.invalid",
		"anthropic_auth_type": anthropicAuthTypeBearer,
	}, "")
	if valid.Code != http.StatusCreated {
		t.Fatalf("expected provider creation 201, got %d: %s", valid.Code, valid.Body)
	}
	var result ProviderCreateResult
	if err := json.Unmarshal([]byte(valid.Body), &result); err != nil {
		t.Fatal(err)
	}
	if got := result.Provider.Options[anthropicAuthTypeOption]; got != anthropicAuthTypeBearer {
		t.Fatalf("stored authentication type = %q, want %q", got, anthropicAuthTypeBearer)
	}

	for _, body := range []map[string]any{
		{
			"name":                "Invalid Anthropic",
			"type":                ProviderAnthropic,
			"base_url":            "https://example.invalid",
			"anthropic_auth_type": "basic",
		},
		{
			"name":     "Invalid Anthropic Option",
			"type":     ProviderAnthropic,
			"base_url": "https://example.invalid",
			"options":  map[string]string{anthropicAuthTypeOption: "basic"},
		},
	} {
		resp := doJSON(t, app, http.MethodPost, "/api/admin/providers", body, "")
		if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body, `"code":"provider_anthropic_auth_type_invalid"`) {
			t.Fatalf("expected invalid authentication type, got %d: %s", resp.Code, resp.Body)
		}
	}
}

func TestAdminProviderConnectionTestRequiresCredentials(t *testing.T) {
	app := newTestServer()
	for _, testCase := range []struct {
		name string
		body map[string]any
		code string
	}{
		{name: "base URL", body: map[string]any{"api_key": "test-secret"}, code: "provider_base_url_required"},
		{name: "API key", body: map[string]any{"base_url": "https://example.invalid/v1"}, code: "provider_api_key_required"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			resp := doJSON(t, app, http.MethodPost, "/api/admin/providers/test-connection", testCase.body, "")
			if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body, `"code":"`+testCase.code+`"`) {
				t.Fatalf("expected %s, got %d: %s", testCase.code, resp.Code, resp.Body)
			}
		})
	}
}

func TestAdminProviderCatalogAndInventoryImport(t *testing.T) {
	app := newTestServer()

	catalogResp := doJSON(t, app, http.MethodGet, "/api/admin/provider-catalog/openai", nil, "")
	if catalogResp.Code != http.StatusOK {
		t.Fatalf("expected openai catalog, got %d: %s", catalogResp.Code, catalogResp.Body)
	}
	if !strings.Contains(catalogResp.Body, `"gpt-5"`) || !strings.Contains(catalogResp.Body, `"category":"openai"`) {
		t.Fatalf("expected openai model details: %s", catalogResp.Body)
	}

	createResp := doJSON(t, app, http.MethodPost, "/api/admin/providers", map[string]any{
		"catalog_id":      "openai",
		"id":              "prv_openai_test",
		"name":            "OpenAI Test",
		"base_url":        "https://api.openai.com/v1",
		"status":          "active",
		"healthy":         true,
		"selected_models": []string{"gpt-5"},
	}, "")
	if createResp.Code != http.StatusCreated {
		t.Fatalf("expected template provider created, got %d: %s", createResp.Code, createResp.Body)
	}
	var result ProviderCreateResult
	if err := json.Unmarshal([]byte(createResp.Body), &result); err != nil {
		t.Fatal(err)
	}
	if result.Provider.ID != "prv_openai_test" || result.ImportedModels != 1 {
		t.Fatalf("unexpected inventory import result: %s", createResp.Body)
	}

	models := doJSON(t, app, http.MethodGet, "/api/admin/models", nil, "")
	if models.Code != http.StatusOK {
		t.Fatalf("expected models list, got %d: %s", models.Code, models.Body)
	}
	if !strings.Contains(models.Body, `"gpt-5"`) || !strings.Contains(models.Body, `"claude-sonnet-5"`) {
		t.Fatalf("expected default model catalog: %s", models.Body)
	}

	providerModels := doJSON(t, app, http.MethodGet, "/api/admin/provider-models?provider_id=prv_openai_test", nil, "")
	if providerModels.Code != http.StatusOK || !strings.Contains(providerModels.Body, `"upstream_model":"gpt-5"`) {
		t.Fatalf("expected imported Provider model inventory, got %d: %s", providerModels.Code, providerModels.Body)
	}
}

func TestAdminProviderCreationRejectsAutomaticRouteCreation(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	created := doJSON(t, app, http.MethodPost, "/api/admin/providers", map[string]any{
		"catalog_id":      "openai",
		"id":              "prv_no_automatic_routes",
		"name":            "No Automatic Routes",
		"type":            ProviderOpenAI,
		"base_url":        "https://api.openai.com/v1",
		"status":          StatusActive,
		"create_routes":   true,
		"selected_models": []string{"gpt-4.1-mini"},
	}, "")

	if created.Code != http.StatusBadRequest || !strings.Contains(created.Body, "provider_routes_must_be_configured_separately") {
		t.Fatalf("expected automatic route creation to be rejected, got %d: %s", created.Code, created.Body)
	}
	if _, ok := store.GetProvider("prv_no_automatic_routes"); ok {
		t.Fatal("rejected provider creation must not persist a provider")
	}
}

func TestAdminProviderUpdateRejectsAutomaticRouteCreation(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	updated := doJSON(t, app, http.MethodPatch, "/api/admin/providers/prv_mock", map[string]any{
		"name":          "Must Not Change",
		"create_routes": true,
	}, "")

	if updated.Code != http.StatusBadRequest || !strings.Contains(updated.Body, "provider_routes_must_be_configured_separately") {
		t.Fatalf("expected automatic route creation to be rejected, got %d: %s", updated.Code, updated.Body)
	}
	provider, ok := store.GetProvider("prv_mock")
	if !ok || provider.Name == "Must Not Change" {
		t.Fatalf("rejected provider update must not persist changes: %+v", provider)
	}
}

func TestAdminKimiCodingTemplateImportsOfficialModels(t *testing.T) {
	store := NewMemoryStore()
	config := Config{
		AdminToken:             "dev_admin_token",
		BootstrapAdminPassword: "kimi-coding-test-password",
		ModelCatalogFile:       "../../../data/model-catalog.yaml",
		ProviderCatalogFile:    "../../../data/provider-catalog.json",
	}
	if err := BootstrapBaseDataWithConfig(store, config); err != nil {
		t.Fatal(err)
	}
	server := NewWithConfig(store, config)
	if _, err := server.InitializeProviderCatalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	app := server.Handler()

	catalogResp := doJSON(t, app, http.MethodGet, "/api/admin/provider-catalog/kimi-for-coding", nil, "")
	if catalogResp.Code != http.StatusOK {
		t.Fatalf("expected Kimi catalog, got %d: %s", catalogResp.Code, catalogResp.Body)
	}
	var catalogPayload struct {
		Data ProviderCatalogEntry `json:"data"`
	}
	if err := json.Unmarshal([]byte(catalogResp.Body), &catalogPayload); err != nil {
		t.Fatal(err)
	}
	expectedModels := map[string]string{
		"k3":                        "kimi-k3",
		"k3-256k":                   "kimi-k3-256k",
		"kimi-for-coding":           "kimi-k2.7-code",
		"kimi-for-coding-highspeed": "kimi-k2.7-code-highspeed",
	}
	if len(catalogPayload.Data.Models) != len(expectedModels) {
		t.Fatalf("expected %d Kimi models, got %+v", len(expectedModels), catalogPayload.Data.Models)
	}
	for _, model := range catalogPayload.Data.Models {
		if canonical, ok := expectedModels[model.ID]; !ok || canonical != model.CanonicalName {
			t.Fatalf("unexpected Kimi catalog model: %+v", model)
		}
	}

	createResp := doJSON(t, app, http.MethodPost, "/api/admin/providers", map[string]any{
		"catalog_id":      "kimi-for-coding",
		"id":              "prv_kimi_coding",
		"name":            "Kimi Coding",
		"base_url":        "https://api.kimi.com/coding/v1",
		"api_key":         "test-key",
		"status":          "active",
		"healthy":         true,
		"model_category":  "kimi",
		"selected_models": []string{"k3", "k3-256k", "kimi-for-coding", "kimi-for-coding-highspeed"},
	}, "")
	if createResp.Code != http.StatusCreated {
		t.Fatalf("expected Kimi provider creation, got %d: %s", createResp.Code, createResp.Body)
	}
	var result ProviderCreateResult
	if err := json.Unmarshal([]byte(createResp.Body), &result); err != nil {
		t.Fatal(err)
	}
	if result.ImportedModels != len(expectedModels) {
		t.Fatalf("expected all Kimi models to be imported, got %s", createResp.Body)
	}
	for _, model := range store.ListProviderModels() {
		if model.ProviderID != "prv_kimi_coding" {
			continue
		}
		if canonical, ok := expectedModels[model.UpstreamModel]; !ok || canonical != model.CanonicalName {
			t.Fatalf("unexpected Kimi Provider model: %+v", model)
		}
		delete(expectedModels, model.UpstreamModel)
	}
	if len(expectedModels) != 0 {
		t.Fatalf("missing Kimi Provider models: %+v", expectedModels)
	}
	for _, route := range store.ListRoutes() {
		if route.ProviderID == "prv_kimi_coding" {
			t.Fatalf("Provider creation must not publish Kimi routes: %+v", route)
		}
	}
}

func TestAdminCustomProviderCatalogLoadsUpstreamModels(t *testing.T) {
	app := newTestServer()
	seenAuth := ""
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected upstream path %s", r.URL.Path)
		}
		seenAuth = r.Header.Get("authorization")
		writeJSON(w, http.StatusOK, map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"id": "gpt-4.1-mini", "object": "model", "owned_by": "agnes"},
				{"id": "agnes-special", "object": "model", "owned_by": "agnes"},
			},
		})
	}))
	defer upstream.Close()

	catalogResp := doJSON(t, app, http.MethodPost, "/api/admin/provider-catalog/custom", map[string]any{
		"name":     "Agnes",
		"type":     ProviderOpenAICompatible,
		"base_url": upstream.URL + "/v1",
		"api_key":  "upstream-secret",
	}, "")
	if catalogResp.Code != http.StatusOK {
		t.Fatalf("expected custom provider catalog, got %d: %s", catalogResp.Code, catalogResp.Body)
	}
	if seenAuth != "Bearer upstream-secret" {
		t.Fatalf("expected upstream authorization header, got %q", seenAuth)
	}
	var catalogPayload struct {
		Data ProviderCatalogEntry `json:"data"`
	}
	if err := json.Unmarshal([]byte(catalogResp.Body), &catalogPayload); err != nil {
		t.Fatal(err)
	}
	if catalogPayload.Data.Source != "custom-upstream" || catalogPayload.Data.ModelsCount != 2 {
		t.Fatalf("expected upstream custom models, got %+v", catalogPayload.Data)
	}
	if catalogPayload.Data.Models[0].ID == "gpt-5" || !strings.Contains(catalogResp.Body, `"agnes-special"`) {
		t.Fatalf("expected real upstream models instead of standard OpenAI catalog: %s", catalogResp.Body)
	}

	createResp := doJSON(t, app, http.MethodPost, "/api/admin/providers", map[string]any{
		"catalog_id":      "custom",
		"id":              "prv_agnes",
		"name":            "Agnes",
		"type":            ProviderOpenAICompatible,
		"base_url":        upstream.URL + "/v1",
		"api_key":         "upstream-secret",
		"status":          "active",
		"healthy":         true,
		"selected_models": []string{"gpt-4.1-mini"},
		"custom_models":   catalogPayload.Data.Models,
	}, "")
	if createResp.Code != http.StatusCreated {
		t.Fatalf("expected custom provider created, got %d: %s", createResp.Code, createResp.Body)
	}
	var result ProviderCreateResult
	if err := json.Unmarshal([]byte(createResp.Body), &result); err != nil {
		t.Fatal(err)
	}
	if result.ImportedModels != 1 {
		t.Fatalf("expected one custom upstream model import, got %s", createResp.Body)
	}
	routesResp := doJSON(t, app, http.MethodGet, "/api/admin/routing-rules", nil, "")
	if routesResp.Code != http.StatusOK || strings.Contains(routesResp.Body, `"provider_id":"prv_agnes"`) {
		t.Fatalf("custom Provider creation must not publish a route: %d %s", routesResp.Code, routesResp.Body)
	}

	seenAuth = ""
	savedCatalogResp := doJSON(t, app, http.MethodPost, "/api/admin/provider-catalog/custom", map[string]any{
		"provider_id": "prv_agnes",
	}, "")
	if savedCatalogResp.Code != http.StatusOK {
		t.Fatalf("expected saved custom provider catalog, got %d: %s", savedCatalogResp.Code, savedCatalogResp.Body)
	}
	if seenAuth != "Bearer upstream-secret" {
		t.Fatalf("expected saved provider key to be used, got %q", seenAuth)
	}
}

func TestAdminAnthropicProviderCatalogLoadsVersionedUpstreamModels(t *testing.T) {
	app := newTestServer()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/anthropic/v1/models" {
			t.Fatalf("unexpected upstream path %s", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "upstream-secret" {
			t.Fatalf("unexpected upstream API key %q", got)
		}
		if got := r.Header.Get("anthropic-version"); got != "2023-06-01" {
			t.Fatalf("unexpected Anthropic version %q", got)
		}
		if got := r.Header.Get("authorization"); got != "" {
			t.Fatalf("unexpected OpenAI authorization header %q", got)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"data": []map[string]any{{"id": "MiniMax-M2.7", "type": "model"}},
		})
	}))
	defer upstream.Close()

	resp := doJSON(t, app, http.MethodPost, "/api/admin/provider-catalog/custom", map[string]any{
		"name":     "MiniMax",
		"type":     ProviderAnthropic,
		"base_url": upstream.URL + "/anthropic/v1",
		"api_key":  "upstream-secret",
	}, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected Anthropic provider catalog, got %d: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body, `"MiniMax-M2.7"`) {
		t.Fatalf("expected upstream Anthropic model, got %s", resp.Body)
	}
}

func TestProviderCatalogUsesStandardModelCategories(t *testing.T) {
	entries := []ProviderCatalogEntry{
		{
			ID: "mixed",
			Models: []ProviderCatalogModel{
				{ID: "deepseekv4", DisplayName: "DeepSeek V4"},
				{ID: "Phi-4-multimodal-instruct"},
				{ID: "agent-max-preview"},
			},
		},
	}

	categories, counts := catalogCategorySummary(entries[0].Models)
	joined := strings.Join(categories, ",")
	if joined != "custom,deepseek,microsoft" {
		t.Fatalf("expected standard categories, got %s", joined)
	}
	if counts["deepseek"] != 1 || counts["microsoft"] != 1 || counts["custom"] != 1 {
		t.Fatalf("unexpected standard category counts: %+v", counts)
	}
	if counts["agent"] != 0 || counts["phi"] != 0 {
		t.Fatalf("unexpected raw long-tail categories: %+v", counts)
	}
	if normalizeModelLookupName("DeepSeekV4") != "deepseek-v4" || normalizeModelLookupName("openai/gpt5") != "gpt-5" {
		t.Fatalf("expected compact provider model names to normalize")
	}
	if got := normalizeProviderBaseURL("302ai", "https://api.highwayapi.ai/openai"); got != "https://api.highwayapi.ai/openai/v1" {
		t.Fatalf("expected JieKou OpenAI-compatible base URL to include /v1, got %s", got)
	}
	if got := normalizeProviderBaseURL("dmxapi", "https://www.dmxapi.cn"); got != "https://www.dmxapi.cn/v1" {
		t.Fatalf("expected dmxapi OpenAI-compatible base URL to include /v1, got %s", got)
	}
}

func TestAdminImportsProviderModelWithoutPublishing(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	imported := doJSON(t, app, http.MethodPost, "/api/admin/provider-models/import", map[string]any{
		"provider_id": "prv_mock",
		"models": []map[string]any{
			{
				"id":             "vendor/private-alpha",
				"display_name":   "Private Alpha",
				"category":       "custom",
				"type":           "chat",
				"context_window": 131072,
				"capabilities":   []string{"chat", "tools"},
			},
		},
	}, "")
	if imported.Code != http.StatusCreated {
		t.Fatalf("expected provider model import 201, got %d: %s", imported.Code, imported.Body)
	}

	providerModels := doJSON(t, app, http.MethodGet, "/api/admin/provider-models", nil, "")
	if providerModels.Code != http.StatusOK || !strings.Contains(providerModels.Body, `"upstream_model":"vendor/private-alpha"`) {
		t.Fatalf("expected imported provider model inventory: %d %s", providerModels.Code, providerModels.Body)
	}
	if strings.Contains(providerModels.Body, `"published_model":"vendor/private-alpha"`) {
		t.Fatalf("unpublished provider model must not claim an external model: %s", providerModels.Body)
	}
	for _, route := range store.ListRoutes() {
		if route.ProviderModel == "vendor/private-alpha" {
			t.Fatalf("import-only operation must not create a route: %+v", route)
		}
	}
}

func TestAdminRejectsProviderModelImportPublication(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	imported := doJSON(t, app, http.MethodPost, "/api/admin/provider-models/import", map[string]any{
		"provider_id": "prv_mock",
		"publish":     true,
		"models": []map[string]any{{
			"id":                     "vendor/retired-publication",
			"input_price_usd_per_1m": 1,
		}},
	}, "")
	if imported.Code != http.StatusBadRequest || !strings.Contains(imported.Body, "provider_model_publication_must_be_configured_separately") {
		t.Fatalf("expected retired publication workflow to be rejected, got %d: %s", imported.Code, imported.Body)
	}
	for _, model := range store.ListProviderModels() {
		if model.ProviderID == "prv_mock" && model.UpstreamModel == "vendor/retired-publication" {
			t.Fatalf("rejected publication imported Provider inventory: %+v", model)
		}
	}
	for _, route := range store.ListRoutes() {
		if route.ProviderID == "prv_mock" && route.ProviderModel == "vendor/retired-publication" {
			t.Fatalf("rejected publication created a route: %+v", route)
		}
	}
}

func TestAdminProviderCreationImportsSelectedModelsWithoutPublishing(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	created := doJSON(t, app, http.MethodPost, "/api/admin/providers", map[string]any{
		"id":              "prv_inventory_only",
		"catalog_id":      "openai",
		"name":            "OpenAI Inventory Only",
		"type":            ProviderOpenAI,
		"base_url":        "https://api.openai.com/v1",
		"status":          StatusActive,
		"selected_models": []string{"gpt-4.1-mini"},
	}, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("expected provider creation 201, got %d: %s", created.Code, created.Body)
	}
	var result ProviderCreateResult
	if err := json.Unmarshal([]byte(created.Body), &result); err != nil {
		t.Fatal(err)
	}
	if result.ImportedModels != 1 {
		t.Fatalf("expected inventory import without publication, got %+v", result)
	}
	for _, route := range store.ListRoutes() {
		if route.ProviderID == "prv_inventory_only" {
			t.Fatalf("inventory-only provider creation must not publish a route: %+v", route)
		}
	}
	found := false
	for _, model := range store.ListProviderModels() {
		if model.ProviderID == "prv_inventory_only" && model.UpstreamModel == "gpt-4.1-mini" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected selected model in provider inventory")
	}
}

func TestAdminProviderCreationImportsSelectedCodexModels(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	created := doJSON(t, app, http.MethodPost, "/api/admin/providers", map[string]any{
		"id":              "prv_codex_selected",
		"catalog_id":      codexProviderCatalogID,
		"name":            "Selected Codex Models",
		"type":            ProviderOpenAICodex,
		"base_url":        openAICodexBaseURL,
		"status":          StatusActive,
		"selected_models": []string{"gpt-selected-codex"},
		"custom_models": []map[string]any{{
			"id":             "gpt-selected-codex",
			"display_name":   "GPT Selected Codex",
			"category":       "codex",
			"type":           "chat",
			"context_window": 272000,
		}},
	}, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("expected selected Codex Provider creation 201, got %d: %s", created.Code, created.Body)
	}
	var result ProviderCreateResult
	if err := json.Unmarshal([]byte(created.Body), &result); err != nil {
		t.Fatal(err)
	}
	if result.ImportedModels != 1 {
		t.Fatalf("expected one explicitly selected Codex model import, got %+v", result)
	}
	for _, model := range store.ListProviderModels() {
		if model.ProviderID == "prv_codex_selected" && model.UpstreamModel == "gpt-selected-codex" && model.ContextWindow == 272000 {
			return
		}
	}
	t.Fatalf("selected Codex model was not imported: %+v", store.ListProviderModels())
}

func TestAdminRejectsDeletingProviderModelUsedByRoute(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()
	var providerModel ProviderModel
	for _, item := range store.ListProviderModels() {
		if item.ProviderID == "prv_mock" && item.UpstreamModel == "mock-chat" {
			providerModel = item
			break
		}
	}
	if providerModel.ID == "" {
		t.Fatal("expected route backfill to create provider inventory")
	}

	deleted := doJSON(t, app, http.MethodDelete, "/api/admin/provider-models/"+providerModel.ID, nil, "")
	if deleted.Code != http.StatusConflict || !strings.Contains(deleted.Body, "provider_model_in_use") {
		t.Fatalf("expected in-use conflict, got %d: %s", deleted.Code, deleted.Body)
	}
}

func TestAdminUpdatesProviderModelInventory(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	providerModel := store.AddProviderModel(ProviderModel{
		ProviderID:    "prv_mock",
		UpstreamModel: "vendor/editable-model",
		DisplayName:   "Editable Model",
		Modality:      "chat",
		Status:        StatusActive,
	})
	app := New(store).Handler()

	updated := doJSON(t, app, http.MethodPatch, "/api/admin/provider-models/"+providerModel.ID, map[string]any{
		"display_name":   "Edited Provider Model",
		"context_window": 131072,
		"capabilities":   []string{"chat", "tools"},
		"status":         StatusDisabled,
	}, "")
	if updated.Code != http.StatusOK {
		t.Fatalf("expected provider model patch 200, got %d: %s", updated.Code, updated.Body)
	}
	var result ProviderModel
	if err := json.Unmarshal([]byte(updated.Body), &result); err != nil {
		t.Fatal(err)
	}
	if result.ID != providerModel.ID ||
		result.ProviderID != "prv_mock" ||
		result.UpstreamModel != "vendor/editable-model" ||
		result.DisplayName != "Edited Provider Model" ||
		result.ContextWindow != 131072 ||
		result.Status != StatusDisabled ||
		!slices.Equal(result.Capabilities, []string{"chat", "tools"}) {
		t.Fatalf("unexpected updated provider model: %+v", result)
	}
}

func TestAdminDeletesUnusedProviderModelInventory(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	providerModel := store.AddProviderModel(ProviderModel{
		ProviderID:    "prv_mock",
		UpstreamModel: "vendor/unused-model",
		DisplayName:   "Unused Model",
		Status:        StatusActive,
	})
	app := New(store).Handler()

	deleted := doJSON(t, app, http.MethodDelete, "/api/admin/provider-models/"+providerModel.ID, nil, "")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("expected unused provider model delete 204, got %d: %s", deleted.Code, deleted.Body)
	}
	for _, item := range store.ListProviderModels() {
		if item.ID == providerModel.ID {
			t.Fatalf("deleted provider model remains in inventory: %+v", item)
		}
	}
}

func TestAdminDeletingProviderRemovesProviderModelInventory(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{
		ID:      "prv_inventory_cascade",
		Name:    "Inventory Cascade Provider",
		Type:    ProviderMock,
		Status:  StatusActive,
		Healthy: true,
	})
	store.AddProviderModel(ProviderModel{
		ProviderID:    provider.ID,
		UpstreamModel: "vendor/cascade-model",
		DisplayName:   "Cascade Model",
		Status:        StatusActive,
	})
	app := New(store).Handler()

	deleted := doJSON(t, app, http.MethodDelete, "/api/admin/providers/"+provider.ID, nil, "")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("expected provider delete 204, got %d: %s", deleted.Code, deleted.Body)
	}
	for _, item := range store.ListProviderModels() {
		if item.ProviderID == provider.ID {
			t.Fatalf("provider deletion left inventory behind: %+v", item)
		}
	}
}

func TestAdminRouteUpdateRequiresImportedProviderModelInventory(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	var route ModelRoute
	for _, item := range store.ListRoutes() {
		if item.ProviderID == "prv_mock" && item.ProviderModel == "mock-chat" {
			route = item
			break
		}
	}
	if route.ID == "" {
		t.Fatal("expected demo route for provider inventory update")
	}
	app := New(store).Handler()

	updated := doJSON(t, app, http.MethodPatch, "/api/admin/routing-rules/"+route.ID, map[string]any{
		"provider_model": "vendor/changed-upstream",
	}, "")
	if updated.Code != http.StatusConflict || !strings.Contains(updated.Body, "provider_model_not_imported") {
		t.Fatalf("expected unimported provider model conflict, got %d: %s", updated.Code, updated.Body)
	}
	store.AddProviderModel(ProviderModel{
		ProviderID:    "prv_mock",
		UpstreamModel: "vendor/changed-upstream",
		DisplayName:   "Changed Upstream",
		Status:        StatusActive,
	})
	updated = doJSON(t, app, http.MethodPatch, "/api/admin/routing-rules/"+route.ID, map[string]any{
		"provider_model": "vendor/changed-upstream",
	}, "")
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body, `"provider_model":"vendor/changed-upstream"`) {
		t.Fatalf("expected imported provider model patch 200, got %d: %s", updated.Code, updated.Body)
	}
}

func TestAdminCreatesExternalModelWithValidatedImportedRoute(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	invalid := doJSON(t, app, http.MethodPost, "/api/admin/models", map[string]any{
		"name":   "invalid-partial-model",
		"status": StatusActive,
		"routes": []map[string]any{{
			"provider_id":    "missing-provider",
			"provider_model": "gpt-4.5",
			"status":         StatusActive,
		}},
	}, "")
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body, "route_provider_not_found") {
		t.Fatalf("expected nested route validation failure, got %d: %s", invalid.Code, invalid.Body)
	}
	if _, ok := modelByNameForTest(store.ListModels(), "invalid-partial-model"); ok {
		t.Fatal("route validation failure must not leave a partial external model")
	}
	store.AddProviderModel(ProviderModel{
		ProviderID:    "prv_mock",
		UpstreamModel: "gpt-4.5",
		DisplayName:   "GPT 4.5",
		Status:        StatusActive,
	})

	created := doJSON(t, app, http.MethodPost, "/api/admin/models", map[string]any{
		"name":         "DeepSeek",
		"family":       "deepseek",
		"modality":     "chat",
		"status":       StatusActive,
		"capabilities": []string{"chat", "tools"},
		"routes": []map[string]any{{
			"provider_id":    "prv_mock",
			"provider_model": "gpt-4.5",
			"status":         StatusActive,
		}},
	}, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("expected model and route creation 201, got %d: %s", created.Code, created.Body)
	}
	foundRoute := false
	for _, route := range store.ListRoutes() {
		if route.ModelName == "DeepSeek" && route.ProviderID == "prv_mock" && route.ProviderModel == "gpt-4.5" {
			foundRoute = route.Priority > 0
		}
	}
	if !foundRoute {
		t.Fatalf("expected prioritized DeepSeek alias route: %+v", store.ListRoutes())
	}
	foundInventory := false
	for _, model := range store.ListProviderModels() {
		if model.ProviderID == "prv_mock" && model.UpstreamModel == "gpt-4.5" {
			foundInventory = true
		}
	}
	if !foundInventory {
		t.Fatal("expected manual nested route to retain imported Provider inventory")
	}
}

func TestAdminExternalModelCreationRollsBackWhenRouteWriteFails(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	store.AddProviderModel(ProviderModel{
		ProviderID:    "prv_mock",
		UpstreamModel: "atomic-upstream",
		DisplayName:   "Atomic Upstream",
		Status:        StatusActive,
	})
	if err := store.db.Exec(`
		CREATE TRIGGER fail_atomic_route_insert
		BEFORE INSERT ON model_routes
		WHEN NEW.model_name = 'atomic-route-failure'
		BEGIN
			SELECT RAISE(FAIL, 'forced route write failure');
		END;
	`).Error; err != nil {
		t.Fatal(err)
	}

	created := doJSON(t, New(store).Handler(), http.MethodPost, "/api/admin/models", map[string]any{
		"name":     "atomic-route-failure",
		"modality": "chat",
		"status":   StatusActive,
		"routes": []map[string]any{{
			"provider_id":    "prv_mock",
			"provider_model": "atomic-upstream",
			"status":         StatusActive,
		}},
	}, "")
	if created.Code != http.StatusInternalServerError {
		t.Fatalf("expected route write failure 500, got %d: %s", created.Code, created.Body)
	}
	if _, ok := modelByNameForTest(store.ListModels(), "atomic-route-failure"); ok {
		t.Fatal("route write failure must roll back the external model")
	}
	for _, route := range store.ListRoutes() {
		if route.ModelName == "atomic-route-failure" {
			t.Fatalf("route write failure must roll back every route: %+v", route)
		}
	}
}

func TestAdminRejectsUnimportedAndDuplicateProviderModelRoutes(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	unimported := doJSON(t, app, http.MethodPost, "/api/admin/routing-rules", map[string]any{
		"model_name":     "gpt-4.1-mini",
		"provider_id":    "prv_mock",
		"provider_model": "vendor/not-imported",
		"status":         StatusActive,
	}, "")
	if unimported.Code != http.StatusConflict || !strings.Contains(unimported.Body, "provider_model_not_imported") {
		t.Fatalf("expected unimported provider model conflict, got %d: %s", unimported.Code, unimported.Body)
	}

	duplicate := doJSON(t, app, http.MethodPost, "/api/admin/routing-rules", map[string]any{
		"model_name":     "gpt-4.1-mini",
		"provider_id":    "prv_mock",
		"provider_model": "mock-chat",
		"status":         StatusActive,
	}, "")
	if duplicate.Code != http.StatusConflict || !strings.Contains(duplicate.Body, "model_route_conflict") {
		t.Fatalf("expected duplicate model route conflict, got %d: %s", duplicate.Code, duplicate.Body)
	}
}

func TestAdminCodexImageVirtualModelRouteRequiresSupportedProviderInventory(t *testing.T) {
	store := NewMemoryStore()
	store.AddModel(Model{Name: codexImageModelName, Modality: "image", Status: StatusActive})
	codexProvider := store.AddProvider(Provider{
		ID: "prv_codex_image_route", Name: "Codex Image Route", Type: ProviderOpenAICodex, Status: StatusActive, Healthy: true,
	})
	otherProvider := store.AddProvider(Provider{
		ID: "prv_openai_image_route", Name: "OpenAI Image Route", Type: ProviderOpenAI, Status: StatusActive, Healthy: true,
	})
	app := New(store).Handler()

	wrongProvider := doJSON(t, app, http.MethodPost, "/api/admin/routing-rules", map[string]any{
		"model_name": codexImageModelName, "provider_id": otherProvider.ID,
		"provider_model": codexImageUpstreamModel, "status": StatusActive,
	}, "")
	if wrongProvider.Code != http.StatusBadRequest || !strings.Contains(wrongProvider.Body, "codex_image_provider_required") {
		t.Fatalf("expected Codex Provider validation, got %d: %s", wrongProvider.Code, wrongProvider.Body)
	}

	wrongModel := doJSON(t, app, http.MethodPost, "/api/admin/routing-rules", map[string]any{
		"model_name": codexImageModelName, "provider_id": codexProvider.ID,
		"provider_model": "gpt-5.6-luna", "status": StatusActive,
	}, "")
	if wrongModel.Code != http.StatusBadRequest || !strings.Contains(wrongModel.Body, "codex_image_upstream_model_invalid") {
		t.Fatalf("expected Codex image upstream validation, got %d: %s", wrongModel.Code, wrongModel.Body)
	}

	activeWithoutCapability := doJSON(t, app, http.MethodPost, "/api/admin/routing-rules", map[string]any{
		"model_name": codexImageModelName, "provider_id": codexProvider.ID,
		"provider_model": codexImageUpstreamModel, "status": StatusActive,
	}, "")
	if activeWithoutCapability.Code != http.StatusConflict || !strings.Contains(activeWithoutCapability.Body, "codex_image_capability_required") {
		t.Fatalf("expected active route without capability to be rejected, got %d: %s", activeWithoutCapability.Code, activeWithoutCapability.Body)
	}

	disabled := doJSON(t, app, http.MethodPost, "/api/admin/routing-rules", map[string]any{
		"model_name": codexImageModelName, "provider_id": codexProvider.ID,
		"provider_model": codexImageUpstreamModel, "status": StatusDisabled,
	}, "")
	if disabled.Code != http.StatusCreated {
		t.Fatalf("expected disabled route to be created for later activation, got %d: %s", disabled.Code, disabled.Body)
	}
	var route ModelRoute
	if err := json.Unmarshal([]byte(disabled.Body), &route); err != nil {
		t.Fatal(err)
	}
	activateWithoutCapability := doJSON(t, app, http.MethodPatch, "/api/admin/routing-rules/"+route.ID, map[string]any{"status": StatusActive}, "")
	if activateWithoutCapability.Code != http.StatusConflict || !strings.Contains(activateWithoutCapability.Body, "codex_image_capability_required") {
		t.Fatalf("expected activation without capability to be rejected, got %d: %s", activateWithoutCapability.Code, activateWithoutCapability.Body)
	}

	resource, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_codex_image_route_supported", ProviderID: codexProvider.ID,
		Name: "Codex Image Route Supported Account", ResourceType: ProviderResourceOpenAISubscription,
		Status: StatusActive, Healthy: true,
		Options:     map[string]string{codexImageCapabilityOption: codexImageCapabilitySupported},
		Credentials: &ProviderResourceCredentials{AccessToken: "route-supported-access", RefreshToken: "route-supported-refresh", AccountID: "route-supported-account"},
	})
	if err != nil {
		t.Fatal(err)
	}
	activateWithCapability := doJSON(t, app, http.MethodPatch, "/api/admin/routing-rules/"+route.ID, map[string]any{
		"status": StatusActive, "provider_resource_id": resource.ID,
	}, "")
	if activateWithCapability.Code != http.StatusOK {
		t.Fatalf("expected activation with supported capability to succeed, got %d: %s", activateWithCapability.Code, activateWithCapability.Body)
	}
}

func TestExternalModelRoleSurvivesCandidateCatalogRefresh(t *testing.T) {
	store := NewMemoryStore()
	external := store.AddModel(Model{
		Name:     "catalog-backed-external",
		Modality: "chat",
		Metadata: map[string]string{
			"source":              "tokenhub-standard-catalog",
			modelDirectoryRoleKey: modelDirectoryRoleExternal,
		},
		Status: StatusDisabled,
	})
	store.AddModel(Model{
		Name:     external.Name,
		Modality: "chat",
		Metadata: map[string]string{"source": "tokenhub-standard-catalog"},
		Status:   StatusActive,
	})

	model, ok := modelByNameForTest(store.ListModels(), external.Name)
	if !ok || model.Metadata[modelDirectoryRoleKey] != modelDirectoryRoleExternal || model.Status != StatusDisabled {
		t.Fatalf("candidate refresh must preserve external role and publication state: %+v", model)
	}
}

// TestAdminProviderCatalogItemDecodesEscapedID guards the escaped-path
// parsing in the provider-catalog item handler: a catalog ID containing "/"
// arrives as %2F and must be looked up under its decoded value, while
// malformed escapes are rejected.
func TestAdminProviderCatalogItemDecodesEscapedID(t *testing.T) {
	store := NewMemoryStore()
	entry := ProviderCatalogEntry{
		ID:          "vendor/model-with-slash",
		Name:        "Vendor Model",
		DisplayName: "Vendor Model",
		Type:        ProviderOpenAICompatible,
		BaseURL:     "https://example.invalid/v1",
		Categories:  []string{"openai"},
		ModelsCount: 1,
		Models:      []ProviderCatalogModel{{ID: "vendor/model-with-slash", Name: "vendor-model"}},
	}
	if err := store.SaveProviderCatalogSnapshot([]ProviderCatalogEntry{entry}, "test", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	resp := doJSON(t, app, http.MethodGet, "/api/admin/provider-catalog/vendor%2Fmodel-with-slash", nil, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected escaped catalog id to resolve, got %d: %s", resp.Code, resp.Body)
	}
	if !strings.Contains(resp.Body, `"id":"vendor/model-with-slash"`) {
		t.Fatalf("expected decoded catalog entry in body: %s", resp.Body)
	}
}

// TestAdminRouteCreationDoesNotMarkExternalModelWhenRouteWriteFails guards
// route create ordering: when the route write itself fails, the model
// directory must not be marked external as if the route had been created.
func TestAdminRouteCreationDoesNotMarkExternalModelWhenRouteWriteFails(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "atomic-route-model", Family: "atomic", Modality: "chat", Status: StatusActive})
	store.AddProviderModel(ProviderModel{
		ProviderID:    "prv_mock",
		UpstreamModel: "atomic-upstream",
		DisplayName:   "Atomic Upstream",
		Status:        StatusActive,
	})
	if err := store.db.Exec(`
		CREATE TRIGGER fail_atomic_route_insert
		BEFORE INSERT ON model_routes
		WHEN NEW.model_name = 'atomic-route-model'
		BEGIN
			SELECT RAISE(FAIL, 'forced route write failure');
		END;
	`).Error; err != nil {
		t.Fatal(err)
	}

	created := doJSON(t, New(store).Handler(), http.MethodPost, "/api/admin/routing-rules", map[string]any{
		"model_name":     "atomic-route-model",
		"provider_id":    "prv_mock",
		"provider_model": "atomic-upstream",
		"status":         StatusActive,
	}, "")
	if created.Code != http.StatusInternalServerError {
		t.Fatalf("expected route write failure 500, got %d: %s", created.Code, created.Body)
	}
	for _, route := range store.ListRoutes() {
		if route.ModelName == "atomic-route-model" {
			t.Fatalf("route write failure must not persist a route: %+v", route)
		}
	}
	model, ok := modelByNameForTest(store.ListModels(), "atomic-route-model")
	if !ok {
		t.Fatal("expected the model to exist")
	}
	if model.Metadata[modelDirectoryRoleKey] == modelDirectoryRoleExternal {
		t.Fatal("route write failure must not mark the model external")
	}
}
