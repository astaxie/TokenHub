package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

type providerResourceModelsCatalogAdapter struct {
	calls        int
	providerType string
	resourceID   string
	etag         string
	status       int
}

func (adapter *providerResourceModelsCatalogAdapter) ResourceModels(_ context.Context, provider Provider, resource ProviderResource, etag string) (ProviderCatalogEntry, int, error) {
	adapter.calls++
	adapter.providerType = provider.Type
	adapter.resourceID = resource.ID
	adapter.etag = etag
	status := adapter.status
	if status == 0 {
		status = http.StatusOK
	}
	return ProviderCatalogEntry{
		ID:          provider.Type,
		Name:        "Adapter Catalog Provider",
		DisplayName: "Adapter Catalog Provider",
		Type:        provider.Type,
		ModelsCount: 1,
		Source:      "plugin-resource-adapter",
		Models: []ProviderCatalogModel{{
			ID:   "adapter-model",
			Name: "adapter-model",
		}},
	}, status, nil
}

func TestAdminPluginProviderCatalogGetUsesModelsReadAction(t *testing.T) {
	store := NewMemoryStore()
	server := NewWithConfig(store, Config{AdminToken: "plugin-catalog-admin"})
	providerType := "resource_catalog_provider"
	pluginID := "tokenhub.provider.resource-catalog"
	if err := server.adapterRegistry.RegisterPlugin(pluginmeta.Descriptor{
		ID:      pluginID,
		Name:    "Resource Catalog Provider",
		Version: "1.0.0",
		Source:  pluginmeta.SourceLocalFile,
		Kinds:   []pluginmeta.Kind{pluginmeta.KindProvider},
		Placements: []pluginmeta.Placement{
			pluginmeta.PlacementGatewayChain,
			pluginmeta.PlacementManagementAction,
		},
		Capabilities: []pluginmeta.CapabilityDescriptor{
			{Kind: "provider", Name: string(AdapterCapabilityModels), Subject: providerType},
		},
	}, AdapterRegistration{
		Type:         providerType,
		Adapter:      struct{}{},
		Capabilities: []AdapterCapability{AdapterCapabilityModels},
	}); err != nil {
		t.Fatalf("register resource catalog provider: %v", err)
	}
	provider := store.AddProvider(Provider{
		ID: "prv_resource_catalog", Name: "Resource Catalog Provider", Type: providerType,
		Status: StatusActive, Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_resource_catalog", ProviderID: provider.ID, Name: "Resource Catalog Account",
		ResourceType: "resource_catalog_account", Status: StatusActive, Healthy: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	actionCalls := 0
	if err := server.pluginActions.Register(pluginmeta.ActionDescriptor{
		PluginID:   pluginID,
		ActionID:   "resource_catalog.models",
		Kind:       pluginmeta.ActionKindRead,
		Capability: "models.read",
		Subject:    providerType,
	}, pluginmeta.ActionHandlerFunc(func(_ context.Context, invocation pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
		actionCalls++
		var observed struct {
			ResourceID string `json:"resource_id"`
		}
		if err := json.Unmarshal(invocation.Payload, &observed); err != nil {
			t.Fatalf("decode resource catalog payload: %v", err)
		}
		if observed.ResourceID != resource.ID || invocation.Actor.ID != "dev_admin" {
			t.Fatalf("unexpected resource catalog invocation: payload=%+v actor=%+v", observed, invocation.Actor)
		}
		return pluginmeta.ActionResult{Data: ProviderCatalogEntry{
			ID: providerType, Name: "Resource Catalog Provider", DisplayName: "Resource Catalog Provider",
			Type: providerType, ModelsCount: 1, Source: "plugin-resource-live",
			Models: []ProviderCatalogModel{{ID: "resource-model", Name: "resource-model"}},
		}}, nil
	})); err != nil {
		t.Fatalf("register resource catalog action: %v", err)
	}

	response := doJSON(t, server.Handler(), http.MethodGet, "/api/admin/provider-catalog/"+providerType+"?resource_id="+resource.ID, nil, "plugin-catalog-admin")
	if response.Code != http.StatusOK {
		t.Fatalf("GET plugin provider catalog: expected 200, got %d: %s", response.Code, response.Body)
	}
	if actionCalls != 1 || !strings.Contains(response.Body, `"source":"plugin-resource-live"`) || !strings.Contains(response.Body, `"id":"resource-model"`) {
		t.Fatalf("plugin resource catalog response/calls: calls=%d body=%s", actionCalls, response.Body)
	}
	stored, ok := store.GetProviderResource(resource.ID)
	if !ok {
		t.Fatal("expected resource to be stored")
	}
	if !strings.Contains(stored.Options[providerResourceSupportedModelsOption], "resource-model") ||
		!strings.Contains(stored.Options[providerResourceModelCatalogOption], "resource-model") {
		t.Fatalf("plugin models.read did not persist resource model cache: %+v", stored.Options)
	}
	otherProvider := store.AddProvider(Provider{
		ID: "prv_other_catalog", Name: "Other Catalog Provider", Type: "other_catalog_provider",
		Status: StatusActive, Healthy: true,
	})
	otherResource, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_other_catalog", ProviderID: otherProvider.ID, Name: "Other Catalog Account",
		ResourceType: "other_catalog_account", Status: StatusActive, Healthy: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	mismatch := doJSON(t, server.Handler(), http.MethodGet, "/api/admin/provider-catalog/"+providerType+"?resource_id="+otherResource.ID, nil, "plugin-catalog-admin")
	if mismatch.Code != http.StatusBadRequest || !strings.Contains(mismatch.Body, "provider_resource_catalog_mismatch") {
		t.Fatalf("expected provider catalog/resource mismatch, got %d: %s", mismatch.Code, mismatch.Body)
	}
	if actionCalls != 1 {
		t.Fatalf("mismatched provider catalog reached plugin action: %d", actionCalls)
	}
}

func TestAdminPluginProviderCatalogGetUsesDeclaredAccountResource(t *testing.T) {
	store := NewMemoryStore()
	server := NewWithConfig(store, Config{AdminToken: "plugin-catalog-admin"})
	providerType := "auto_catalog_provider"
	resourceType := "auto_catalog_account"
	pluginID := "tokenhub.provider.auto-catalog"
	if err := server.adapterRegistry.RegisterPlugin(pluginmeta.Descriptor{
		ID:      pluginID,
		Name:    "Auto Catalog Provider",
		Version: "1.0.0",
		Source:  pluginmeta.SourceLocalFile,
		Kinds:   []pluginmeta.Kind{pluginmeta.KindProvider},
		Placements: []pluginmeta.Placement{
			pluginmeta.PlacementGatewayChain,
			pluginmeta.PlacementManagementAction,
		},
		Capabilities: []pluginmeta.CapabilityDescriptor{
			{Kind: "provider", Name: string(AdapterCapabilityModels), Subject: providerType},
			{Kind: "provider_resource_type", Name: resourceType, Subject: providerType, Value: pluginmeta.ManifestProviderResourceType{
				Type:        resourceType,
				DisplayName: "Auto Catalog Account",
				Default:     true,
			}.CapabilityValue()},
		},
	}, AdapterRegistration{
		Type:         providerType,
		Adapter:      struct{}{},
		Capabilities: []AdapterCapability{AdapterCapabilityModels},
	}); err != nil {
		t.Fatalf("register auto catalog provider: %v", err)
	}
	provider := store.AddProvider(Provider{
		ID: "prv_auto_catalog", Name: "Auto Catalog Provider", Type: providerType,
		Status: StatusActive, Healthy: true,
	})
	_, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_auto_catalog_undeclared", ProviderID: provider.ID, Name: "Undeclared Account",
		ResourceType: "undeclared_account", Status: StatusActive, Healthy: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	resource, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_auto_catalog_declared", ProviderID: provider.ID, Name: "Declared Account",
		ResourceType: resourceType, Status: StatusActive, Healthy: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	actionCalls := 0
	if err := server.pluginActions.Register(pluginmeta.ActionDescriptor{
		PluginID:   pluginID,
		ActionID:   "auto_catalog.models.read",
		Kind:       pluginmeta.ActionKindRead,
		Capability: "models.read",
		Subject:    providerType,
	}, pluginmeta.ActionHandlerFunc(func(_ context.Context, invocation pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
		actionCalls++
		var observed struct {
			ResourceID string `json:"resource_id"`
		}
		if err := json.Unmarshal(invocation.Payload, &observed); err != nil {
			t.Fatalf("decode auto catalog payload: %v", err)
		}
		if observed.ResourceID != resource.ID {
			t.Fatalf("auto catalog selected resource %q, want %q", observed.ResourceID, resource.ID)
		}
		return pluginmeta.ActionResult{Data: ProviderCatalogEntry{
			ID: providerType, Name: "Auto Catalog Provider", DisplayName: "Auto Catalog Provider",
			Type: providerType, ModelsCount: 1, Source: "plugin-auto-resource-live",
			Models: []ProviderCatalogModel{{ID: "auto-resource-model", Name: "auto-resource-model"}},
		}}, nil
	})); err != nil {
		t.Fatalf("register auto catalog action: %v", err)
	}

	response := doJSON(t, server.Handler(), http.MethodGet, "/api/admin/provider-catalog/"+providerType, nil, "plugin-catalog-admin")
	if response.Code != http.StatusOK {
		t.Fatalf("GET auto plugin provider catalog: expected 200, got %d: %s", response.Code, response.Body)
	}
	if actionCalls != 1 || !strings.Contains(response.Body, `"source":"plugin-auto-resource-live"`) ||
		!strings.Contains(response.Body, `"id":"auto-resource-model"`) {
		t.Fatalf("auto plugin provider catalog response/calls: calls=%d body=%s", actionCalls, response.Body)
	}
}

func TestAdminPluginProviderCatalogGetUsesDeclaredAccountRequiredError(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "plugin-catalog-admin"})
	providerType := "account_required_catalog_provider"
	pluginID := "tokenhub.provider.account-required-catalog"
	entry := pluginProviderCatalogEntry{
		ID:                                "account-required-catalog",
		Name:                              "Account Required Catalog Provider",
		DisplayName:                       "Account Required Catalog Provider",
		Type:                              providerType,
		Source:                            "plugin-account-required",
		ModelsAccountRequiredErrorCode:    "plugin_account_required",
		ModelsAccountRequiredErrorMessage: "Connect a plugin account before loading models",
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("encode provider catalog entry: %v", err)
	}
	if err := server.adapterRegistry.RegisterPlugin(pluginmeta.Descriptor{
		ID:      pluginID,
		Name:    "Account Required Catalog Provider",
		Version: "1.0.0",
		Source:  pluginmeta.SourceLocalFile,
		Kinds:   []pluginmeta.Kind{pluginmeta.KindProvider},
		Capabilities: []pluginmeta.CapabilityDescriptor{
			{Kind: "provider", Name: string(AdapterCapabilityModels), Subject: providerType},
			{Kind: "provider_catalog", Name: "entry", Subject: providerType, Value: string(encoded)},
		},
	}, AdapterRegistration{
		Type:         providerType,
		Adapter:      struct{}{},
		Capabilities: []AdapterCapability{AdapterCapabilityModels},
	}); err != nil {
		t.Fatalf("register account-required catalog provider: %v", err)
	}

	response := doJSON(t, server.Handler(), http.MethodGet, "/api/admin/provider-catalog/account-required-catalog", nil, "plugin-catalog-admin")
	if response.Code != http.StatusConflict || !strings.Contains(response.Body, `"code":"plugin_account_required"`) {
		t.Fatalf("GET account-required plugin provider catalog: expected declared conflict, got %d: %s", response.Code, response.Body)
	}
}

func TestAdminProviderCreationImportsSelectedPluginCatalogModelsFromStandardCatalog(t *testing.T) {
	store := NewMemoryStore()
	store.AddModel(Model{
		Name:                    "plugin-standard-model",
		Family:                  "plugin",
		Category:                "chat",
		Modality:                "chat",
		ContextWindow:           64000,
		InputPriceUSDPer1M:      1.25,
		CacheWritePriceUSDPer1M: 0.75,
		CacheWritePriceConfiguration: CacheWritePriceConfiguration{
			CacheWritePriceConfigured: true,
		},
		Metadata: map[string]string{"display_name": "Plugin Standard Model"},
		Status:   StatusActive,
	})
	server := NewWithConfig(store, Config{AdminToken: "plugin-catalog-admin"})
	providerType := "selected_standard_catalog_provider"
	pluginID := "tokenhub.provider.selected-standard-catalog"
	entry := pluginProviderCatalogEntry{
		ID:          "selected-standard-catalog",
		Name:        "Selected Standard Catalog Provider",
		DisplayName: "Selected Standard Catalog Provider",
		Type:        providerType,
		BaseURL:     "https://selected-standard.example/v1",
		Categories:  []string{"custom"},
		Source:      "plugin-catalog",
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("encode selected standard catalog entry: %v", err)
	}
	if err := server.adapterRegistry.RegisterPlugin(pluginmeta.Descriptor{
		ID:      pluginID,
		Name:    "Selected Standard Catalog Provider",
		Version: "1.0.0",
		Source:  pluginmeta.SourceLocalFile,
		Kinds:   []pluginmeta.Kind{pluginmeta.KindProvider},
		Capabilities: []pluginmeta.CapabilityDescriptor{
			{Kind: "provider", Name: string(AdapterCapabilityModels), Subject: providerType},
			{Kind: "provider_policy", Name: providerAPIKeyRequiredOption, Subject: providerType, Value: "false"},
			{Kind: "provider_catalog", Name: "entry", Subject: providerType, Value: string(encoded)},
		},
	}, AdapterRegistration{
		Type:         providerType,
		Adapter:      struct{}{},
		Capabilities: []AdapterCapability{AdapterCapabilityModels},
	}); err != nil {
		t.Fatalf("register selected standard catalog provider: %v", err)
	}

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/providers", map[string]any{
		"id":              "prv_selected_standard_catalog",
		"catalog_id":      "selected-standard-catalog",
		"name":            "Selected Standard Catalog Provider",
		"selected_models": []string{"plugin-standard-model"},
	}, "plugin-catalog-admin")
	if response.Code != http.StatusCreated {
		t.Fatalf("POST provider from selected standard plugin catalog: expected 201, got %d: %s", response.Code, response.Body)
	}
	var result ProviderCreateResult
	if err := json.Unmarshal([]byte(response.Body), &result); err != nil {
		t.Fatal(err)
	}
	if result.ImportedModels != 1 || result.Provider.Type != providerType {
		t.Fatalf("provider creation result = %+v", result)
	}
	for _, model := range store.ListProviderModels() {
		if model.ProviderID == "prv_selected_standard_catalog" && model.UpstreamModel == "plugin-standard-model" &&
			model.Category == "custom" && model.ContextWindow == 64000 && model.CacheWritePriceConfigured {
			return
		}
	}
	t.Fatalf("selected standard plugin catalog model was not imported: %+v", store.ListProviderModels())
}

func TestAdminCustomProviderCatalogPostUsesModelsPreviewAction(t *testing.T) {
	store := NewMemoryStore()
	server := NewWithConfig(store, Config{AdminToken: "plugin-catalog-admin"})
	providerType := "preview_catalog_provider"
	pluginID := "tokenhub.provider.preview-catalog"
	if err := server.adapterRegistry.RegisterPlugin(pluginmeta.Descriptor{
		ID:      pluginID,
		Name:    "Preview Catalog Provider",
		Version: "1.0.0",
		Source:  pluginmeta.SourceLocalFile,
		Kinds:   []pluginmeta.Kind{pluginmeta.KindProvider},
		Placements: []pluginmeta.Placement{
			pluginmeta.PlacementGatewayChain,
			pluginmeta.PlacementManagementAction,
		},
		Capabilities: []pluginmeta.CapabilityDescriptor{
			{Kind: "provider", Name: string(AdapterCapabilityModels), Subject: providerType},
		},
	}, AdapterRegistration{
		Type:         providerType,
		Adapter:      struct{}{},
		Capabilities: []AdapterCapability{AdapterCapabilityModels},
	}); err != nil {
		t.Fatalf("register preview catalog provider: %v", err)
	}
	actionCalls := 0
	if err := server.pluginActions.Register(pluginmeta.ActionDescriptor{
		PluginID:   pluginID,
		ActionID:   "preview_catalog.models.preview",
		Kind:       pluginmeta.ActionKindRead,
		Capability: "models.preview",
		Subject:    providerType,
	}, pluginmeta.ActionHandlerFunc(func(_ context.Context, invocation pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
		actionCalls++
		var observed ProviderCreateRequest
		if err := json.Unmarshal(invocation.Payload, &observed); err != nil {
			t.Fatalf("decode preview catalog payload: %v", err)
		}
		if observed.Type != providerType || observed.BaseURL != "https://preview.example/v1" || observed.APIKey != "preview-secret" || invocation.Actor.ID != "dev_admin" {
			t.Fatalf("unexpected preview catalog invocation: payload=%+v actor=%+v", observed, invocation.Actor)
		}
		return pluginmeta.ActionResult{Data: ProviderCatalogEntry{
			ID: providerType, Name: "Preview Catalog Provider", DisplayName: "Preview Catalog Provider",
			Type: providerType, ModelsCount: 1, Source: "plugin-preview-live",
			Models: []ProviderCatalogModel{{ID: "preview-action-model", Name: "preview-action-model"}},
		}}, nil
	})); err != nil {
		t.Fatalf("register preview catalog action: %v", err)
	}

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/provider-catalog/custom", map[string]any{
		"name":     "Preview Catalog Provider",
		"type":     providerType,
		"base_url": "https://preview.example/v1",
		"api_key":  "preview-secret",
	}, "plugin-catalog-admin")
	if response.Code != http.StatusOK {
		t.Fatalf("POST plugin preview catalog: expected 200, got %d: %s", response.Code, response.Body)
	}
	if actionCalls != 1 || !strings.Contains(response.Body, `"source":"plugin-preview-live"`) || !strings.Contains(response.Body, `"id":"preview-action-model"`) {
		t.Fatalf("plugin preview catalog response/calls: calls=%d body=%s", actionCalls, response.Body)
	}
}

func TestAdminCreatePluginProviderPersistsRouteResourcePolicy(t *testing.T) {
	store := NewMemoryStore()
	server := NewWithConfig(store, Config{AdminToken: "plugin-catalog-admin"})
	providerType := "policy_subscription_provider"
	if err := server.adapterRegistry.RegisterPlugin(pluginmeta.Descriptor{
		ID:      "tokenhub.provider.policy-subscription",
		Name:    "Policy Subscription Provider",
		Version: "1.0.0",
		Source:  pluginmeta.SourceLocalFile,
		Kinds:   []pluginmeta.Kind{pluginmeta.KindProvider},
		Placements: []pluginmeta.Placement{
			pluginmeta.PlacementGatewayChain,
		},
		Capabilities: []pluginmeta.CapabilityDescriptor{
			{Kind: "provider_policy", Name: "route_requires_resource", Subject: providerType, Value: "true"},
			{Kind: "provider_policy", Name: "credentials_scope", Subject: providerType, Value: providerCredentialsScopeResource},
			{Kind: "provider_policy", Name: providerCredentialRefreshProfileOption, Subject: providerType, Value: providerCredentialRefreshProfileOpenAIAccountOAuth},
			{Kind: "provider_resource_type", Name: "policy_subscription_account", Subject: providerType, Value: pluginmeta.ManifestProviderResourceType{
				Type:        "policy_subscription_account",
				DisplayName: "Policy Subscription Account",
				Default:     true,
				Defaults: map[string]string{
					"base_url": "https://policy-subscription.example/v1",
				},
			}.CapabilityValue()},
		},
	}, AdapterRegistration{
		Type:    providerType,
		Adapter: struct{}{},
	}); err != nil {
		t.Fatalf("register policy provider: %v", err)
	}

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/providers", map[string]any{
		"name":    "Policy Subscription",
		"type":    providerType,
		"api_key": "provider-level-secret",
	}, "plugin-catalog-admin")
	if response.Code != http.StatusCreated {
		t.Fatalf("create plugin provider: expected 201, got %d: %s", response.Code, response.Body)
	}
	var payload ProviderCreateResult
	if err := json.Unmarshal([]byte(response.Body), &payload); err != nil {
		t.Fatalf("decode create provider response: %v", err)
	}
	if payload.Provider.Options[providerRouteRequiresResourceOption] != "true" {
		t.Fatalf("provider route resource policy options = %+v", payload.Provider.Options)
	}
	if payload.Provider.Options[providerCredentialsScopeOption] != providerCredentialsScopeResource {
		t.Fatalf("provider credentials scope options = %+v", payload.Provider.Options)
	}
	if payload.Provider.Options[providerCredentialRefreshProfileOption] != providerCredentialRefreshProfileOpenAIAccountOAuth {
		t.Fatalf("provider credential refresh profile options = %+v", payload.Provider.Options)
	}
	if payload.Provider.BaseURL != "https://policy-subscription.example/v1" {
		t.Fatalf("provider base URL = %q, want plugin resource default", payload.Provider.BaseURL)
	}
	stored, ok := store.GetProvider(payload.Provider.ID)
	if !ok {
		t.Fatal("created provider was not persisted")
	}
	if stored.BaseURL != "https://policy-subscription.example/v1" {
		t.Fatalf("stored provider base URL = %q, want plugin resource default", stored.BaseURL)
	}
	if !providerRouteRequiresResource(stored) {
		t.Fatalf("stored provider policy = %+v", stored.Options)
	}
	if !providerUsesResourceCredentials(stored) || stored.APIKey != "" {
		t.Fatalf("stored provider credentials policy/api key = options:%+v api_key:%q", stored.Options, stored.APIKey)
	}
}

func TestAdminPluginProviderCatalogGetFallsBackToResourceModelAdapter(t *testing.T) {
	store := NewMemoryStore()
	server := NewWithConfig(store, Config{AdminToken: "plugin-catalog-admin"})
	providerType := "adapter_catalog_provider"
	pluginID := "tokenhub.provider.adapter-catalog"
	adapter := &providerResourceModelsCatalogAdapter{}
	if err := server.adapterRegistry.RegisterPlugin(pluginmeta.Descriptor{
		ID:      pluginID,
		Name:    "Adapter Catalog Provider",
		Version: "1.0.0",
		Source:  pluginmeta.SourceLocalFile,
		Kinds:   []pluginmeta.Kind{pluginmeta.KindProvider},
		Placements: []pluginmeta.Placement{
			pluginmeta.PlacementGatewayChain,
			pluginmeta.PlacementManagementAction,
		},
		Capabilities: []pluginmeta.CapabilityDescriptor{
			{Kind: "provider", Name: string(AdapterCapabilityModels), Subject: providerType},
		},
	}, AdapterRegistration{
		Type:         providerType,
		Adapter:      adapter,
		Capabilities: []AdapterCapability{AdapterCapabilityModels},
	}); err != nil {
		t.Fatalf("register adapter catalog provider: %v", err)
	}
	provider := store.AddProvider(Provider{
		ID: "prv_adapter_catalog", Name: "Adapter Catalog Provider", Type: providerType,
		Status: StatusActive, Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_adapter_catalog", ProviderID: provider.ID, Name: "Adapter Catalog Account",
		ResourceType: "adapter_catalog_account", Status: StatusActive, Healthy: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	response := doJSON(t, server.Handler(), http.MethodGet, "/api/admin/provider-catalog/"+providerType+"?resource_id="+resource.ID, nil, "plugin-catalog-admin")
	if response.Code != http.StatusOK {
		t.Fatalf("GET plugin adapter catalog: expected 200, got %d: %s", response.Code, response.Body)
	}
	if adapter.calls != 1 || adapter.providerType != providerType || adapter.resourceID != resource.ID {
		t.Fatalf("adapter catalog invocation: calls=%d provider=%q resource=%q", adapter.calls, adapter.providerType, adapter.resourceID)
	}
	if !strings.Contains(response.Body, `"source":"plugin-resource-adapter"`) || !strings.Contains(response.Body, `"id":"adapter-model"`) {
		t.Fatalf("plugin adapter catalog response = %s", response.Body)
	}
	stored, ok := store.GetProviderResource(resource.ID)
	if !ok || !strings.Contains(stored.Options[providerResourceSupportedModelsOption], "adapter-model") {
		t.Fatalf("adapter catalog models were not persisted: ok=%v options=%v", ok, stored.Options)
	}
	if _, ok := stored.Options[codexResourceSupportedModelsOption]; ok {
		t.Fatalf("adapter catalog unexpectedly persisted legacy Codex model cache key: %v", stored.Options)
	}
}

func TestAdminPluginProviderCatalogGetUsesGenericResourceModelCache(t *testing.T) {
	store := NewMemoryStore()
	server := NewWithConfig(store, Config{AdminToken: "plugin-catalog-admin"})
	providerType := "adapter_cached_catalog_provider"
	pluginID := "tokenhub.provider.adapter-cached-catalog"
	adapter := &providerResourceModelsCatalogAdapter{status: http.StatusNotModified}
	if err := server.adapterRegistry.RegisterPlugin(pluginmeta.Descriptor{
		ID:      pluginID,
		Name:    "Adapter Cached Catalog Provider",
		Version: "1.0.0",
		Source:  pluginmeta.SourceLocalFile,
		Kinds:   []pluginmeta.Kind{pluginmeta.KindProvider},
		Placements: []pluginmeta.Placement{
			pluginmeta.PlacementGatewayChain,
			pluginmeta.PlacementManagementAction,
		},
		Capabilities: []pluginmeta.CapabilityDescriptor{
			{Kind: "provider", Name: string(AdapterCapabilityModels), Subject: providerType},
		},
	}, AdapterRegistration{
		Type:         providerType,
		Adapter:      adapter,
		Capabilities: []AdapterCapability{AdapterCapabilityModels},
	}); err != nil {
		t.Fatalf("register adapter cached catalog provider: %v", err)
	}
	provider := store.AddProvider(Provider{
		ID: "prv_adapter_cached_catalog", Name: "Adapter Cached Catalog Provider", Type: providerType,
		BaseURL: "https://cached.example/v1", Status: StatusActive, Healthy: true,
	})
	catalog, err := json.Marshal([]ProviderCatalogModel{{ID: "cached-adapter-model", Name: "cached-adapter-model"}})
	if err != nil {
		t.Fatal(err)
	}
	resource, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_adapter_cached_catalog", ProviderID: provider.ID, Name: "Adapter Cached Catalog Account",
		ResourceType: "adapter_cached_catalog_account", Status: StatusActive, Healthy: true,
		Options: map[string]string{
			providerResourceModelsETagOption:   "etag-cached",
			providerResourceModelCatalogOption: string(catalog),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	response := doJSON(t, server.Handler(), http.MethodGet, "/api/admin/provider-catalog/"+providerType+"?resource_id="+resource.ID, nil, "plugin-catalog-admin")
	if response.Code != http.StatusOK {
		t.Fatalf("GET cached plugin adapter catalog: expected 200, got %d: %s", response.Code, response.Body)
	}
	if adapter.calls != 1 || adapter.etag != "etag-cached" {
		t.Fatalf("adapter cached catalog invocation: calls=%d etag=%q", adapter.calls, adapter.etag)
	}
	if !strings.Contains(response.Body, `"source":"provider-resource-cache"`) ||
		!strings.Contains(response.Body, `"type":"`+providerType+`"`) ||
		!strings.Contains(response.Body, `"id":"cached-adapter-model"`) ||
		strings.Contains(response.Body, `"OpenAI Codex"`) {
		t.Fatalf("cached plugin adapter catalog response = %s", response.Body)
	}
}

func TestAdminPluginProviderCatalogGetUsesPluginCatalogIdentityForCachedResourceModels(t *testing.T) {
	store := NewMemoryStore()
	server := NewWithConfig(store, Config{AdminToken: "plugin-catalog-admin"})
	providerType := "adapter_identity_cached_catalog_provider"
	catalogValue, ok := pluginmeta.ManifestProviderCatalog{
		DisplayName: "Identity Cached Catalog",
		BaseURL:     "https://identity-catalog.example/v1",
		Categories:  []string{"identity"},
	}.CapabilityValue(providerType)
	if !ok {
		t.Fatal("build provider catalog capability")
	}
	adapter := &providerResourceModelsCatalogAdapter{status: http.StatusNotModified}
	if err := server.adapterRegistry.RegisterPlugin(pluginmeta.Descriptor{
		ID:      "tokenhub.provider.adapter-identity-cached-catalog",
		Name:    "Adapter Identity Cached Catalog Provider",
		Version: "1.0.0",
		Source:  pluginmeta.SourceLocalFile,
		Kinds:   []pluginmeta.Kind{pluginmeta.KindProvider},
		Placements: []pluginmeta.Placement{
			pluginmeta.PlacementGatewayChain,
			pluginmeta.PlacementManagementAction,
		},
		Capabilities: []pluginmeta.CapabilityDescriptor{
			{Kind: "provider", Name: string(AdapterCapabilityModels), Subject: providerType},
			{Kind: "provider_catalog", Name: "entry", Subject: providerType, Value: catalogValue},
		},
	}, AdapterRegistration{
		Type:         providerType,
		Adapter:      adapter,
		Capabilities: []AdapterCapability{AdapterCapabilityModels},
	}); err != nil {
		t.Fatalf("register adapter identity cached catalog provider: %v", err)
	}
	provider := store.AddProvider(Provider{
		ID: "prv_adapter_identity_cached_catalog", Name: "Fallback Provider Name", Type: providerType,
		BaseURL: "https://fallback.example/v1", Status: StatusActive, Healthy: true,
	})
	catalog, err := json.Marshal([]ProviderCatalogModel{{ID: "identity-cached-model", Name: "identity-cached-model"}})
	if err != nil {
		t.Fatal(err)
	}
	resource, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_adapter_identity_cached_catalog", ProviderID: provider.ID, Name: "Adapter Identity Cached Catalog Account",
		ResourceType: "adapter_identity_cached_catalog_account", Status: StatusActive, Healthy: true,
		Options: map[string]string{
			providerResourceModelsETagOption:   "etag-identity-cached",
			providerResourceModelCatalogOption: string(catalog),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	response := doJSON(t, server.Handler(), http.MethodGet, "/api/admin/provider-catalog/"+providerType+"?resource_id="+resource.ID, nil, "plugin-catalog-admin")
	if response.Code != http.StatusOK {
		t.Fatalf("GET identity cached plugin adapter catalog: expected 200, got %d: %s", response.Code, response.Body)
	}
	if !strings.Contains(response.Body, `"display_name":"Identity Cached Catalog"`) ||
		!strings.Contains(response.Body, `"base_url":"https://identity-catalog.example/v1"`) ||
		!strings.Contains(response.Body, `"id":"identity-cached-model"`) ||
		strings.Contains(response.Body, `"Fallback Provider Name"`) {
		t.Fatalf("cached plugin adapter catalog response = %s", response.Body)
	}
}

func TestAdminPluginProviderCatalogGetUsesLegacyCodexResourceModelCache(t *testing.T) {
	store := NewMemoryStore()
	server := NewWithConfig(store, Config{AdminToken: "plugin-catalog-admin"})
	providerType := "adapter_legacy_cached_catalog_provider"
	adapter := &providerResourceModelsCatalogAdapter{status: http.StatusNotModified}
	if err := server.adapterRegistry.RegisterPlugin(pluginmeta.Descriptor{
		ID:      "tokenhub.provider.adapter-legacy-cached-catalog",
		Name:    "Adapter Legacy Cached Catalog Provider",
		Version: "1.0.0",
		Source:  pluginmeta.SourceLocalFile,
		Kinds:   []pluginmeta.Kind{pluginmeta.KindProvider},
		Placements: []pluginmeta.Placement{
			pluginmeta.PlacementGatewayChain,
			pluginmeta.PlacementManagementAction,
		},
		Capabilities: []pluginmeta.CapabilityDescriptor{
			{Kind: "provider", Name: string(AdapterCapabilityModels), Subject: providerType},
		},
	}, AdapterRegistration{
		Type:         providerType,
		Adapter:      adapter,
		Capabilities: []AdapterCapability{AdapterCapabilityModels},
	}); err != nil {
		t.Fatalf("register adapter legacy cached catalog provider: %v", err)
	}
	provider := store.AddProvider(Provider{
		ID: "prv_adapter_legacy_cached_catalog", Name: "Adapter Legacy Cached Catalog Provider", Type: providerType,
		BaseURL: "https://legacy-cached.example/v1", Status: StatusActive, Healthy: true,
	})
	catalog, err := json.Marshal([]ProviderCatalogModel{{ID: "legacy-cached-adapter-model", Name: "legacy-cached-adapter-model"}})
	if err != nil {
		t.Fatal(err)
	}
	resource, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_adapter_legacy_cached_catalog", ProviderID: provider.ID, Name: "Adapter Legacy Cached Catalog Account",
		ResourceType: "adapter_legacy_cached_catalog_account", Status: StatusActive, Healthy: true,
		Options: map[string]string{
			codexResourceModelsETagOption:   "etag-legacy-cached",
			codexResourceModelCatalogOption: string(catalog),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	response := doJSON(t, server.Handler(), http.MethodGet, "/api/admin/provider-catalog/"+providerType+"?resource_id="+resource.ID, nil, "plugin-catalog-admin")
	if response.Code != http.StatusOK {
		t.Fatalf("GET legacy cached plugin adapter catalog: expected 200, got %d: %s", response.Code, response.Body)
	}
	if adapter.calls != 1 || adapter.etag != "etag-legacy-cached" {
		t.Fatalf("adapter legacy cached catalog invocation: calls=%d etag=%q", adapter.calls, adapter.etag)
	}
	if !strings.Contains(response.Body, `"source":"provider-resource-cache"`) ||
		!strings.Contains(response.Body, `"type":"`+providerType+`"`) ||
		!strings.Contains(response.Body, `"id":"legacy-cached-adapter-model"`) ||
		strings.Contains(response.Body, `"OpenAI Codex"`) {
		t.Fatalf("legacy cached plugin adapter catalog response = %s", response.Body)
	}
}

func TestAdminPluginProviderCatalogGetKeepsStaticEntryWhenResourceModelsUnsupported(t *testing.T) {
	store := NewMemoryStore()
	server := NewWithConfig(store, Config{AdminToken: "plugin-catalog-admin"})
	providerType := "static_catalog_provider"
	pluginID := "tokenhub.provider.static-catalog"
	if err := server.adapterRegistry.RegisterPlugin(pluginmeta.Descriptor{
		ID:      pluginID,
		Name:    "Static Catalog Provider",
		Version: "1.0.0",
		Source:  pluginmeta.SourceLocalFile,
		Kinds:   []pluginmeta.Kind{pluginmeta.KindProvider},
		Placements: []pluginmeta.Placement{
			pluginmeta.PlacementGatewayChain,
			pluginmeta.PlacementManagementAction,
		},
		Capabilities: []pluginmeta.CapabilityDescriptor{
			{Kind: "provider", Name: string(AdapterCapabilityModels), Subject: providerType},
		},
	}, AdapterRegistration{
		Type:         providerType,
		Adapter:      struct{}{},
		Capabilities: []AdapterCapability{AdapterCapabilityModels},
	}); err != nil {
		t.Fatalf("register static catalog provider: %v", err)
	}
	provider := store.AddProvider(Provider{
		ID: "prv_static_catalog", Name: "Static Catalog Provider", Type: providerType,
		Status: StatusActive, Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_static_catalog", ProviderID: provider.ID, Name: "Static Catalog Account",
		ResourceType: "static_catalog_account", Status: StatusActive, Healthy: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	response := doJSON(t, server.Handler(), http.MethodGet, "/api/admin/provider-catalog/"+providerType+"?resource_id="+resource.ID, nil, "plugin-catalog-admin")
	if response.Code != http.StatusOK {
		t.Fatalf("GET static plugin catalog: expected 200, got %d: %s", response.Code, response.Body)
	}
	if !strings.Contains(response.Body, `"source":"plugin:local_file"`) || !strings.Contains(response.Body, `"id":"`+providerType+`"`) {
		t.Fatalf("static plugin catalog response = %s", response.Body)
	}
}

func TestProviderCatalogIncludesBuiltInPluginCatalogEntries(t *testing.T) {
	store := NewMemoryStore()
	server := New(store)
	providerType := "builtin_catalog_plugin"
	pluginID := "tokenhub.provider.builtin-catalog-plugin"
	entry := pluginProviderCatalogEntry{
		ID:          "builtin-catalog-plugin",
		Name:        "Built-in Catalog Plugin",
		DisplayName: "Built-in Catalog Plugin",
		Type:        providerType,
		BaseURL:     "https://builtin-plugin.example/v1",
		Source:      "plugin:built_in",
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.adapterRegistry.RegisterPlugin(pluginmeta.Descriptor{
		ID:      pluginID,
		Name:    "Built-in Catalog Plugin",
		Version: "built-in",
		Source:  pluginmeta.SourceBuiltIn,
		Kinds:   []pluginmeta.Kind{pluginmeta.KindProvider},
		Placements: []pluginmeta.Placement{
			pluginmeta.PlacementGatewayChain,
		},
		Capabilities: []pluginmeta.CapabilityDescriptor{
			{Kind: "provider", Name: string(AdapterCapabilityChat), Subject: providerType},
			{Kind: "provider_catalog", Name: "entry", Subject: providerType, Value: string(encoded)},
		},
	}, AdapterRegistration{
		Type:         providerType,
		Adapter:      MockAdapter{},
		Capabilities: []AdapterCapability{AdapterCapabilityChat},
	}); err != nil {
		t.Fatalf("register built-in catalog plugin: %v", err)
	}

	merged, changed := server.providerCatalogEntriesWithPlugins(nil)
	if !changed {
		t.Fatalf("built-in plugin catalog merge changed=%t entries=%+v", changed, merged)
	}
	var found bool
	for _, candidate := range merged {
		if candidate.ID != entry.ID {
			continue
		}
		found = true
		if candidate.Type != providerType || candidate.Source != "plugin:built_in" {
			t.Fatalf("built-in plugin catalog entry = %+v", candidate)
		}
	}
	if !found {
		t.Fatalf("built-in plugin catalog entry missing from %+v", merged)
	}
}

func TestProviderCatalogIncludesCatalogOnlyPluginEntries(t *testing.T) {
	server := New(NewMemoryStore())
	providerType := "catalog_only_subscription"
	entry := pluginProviderCatalogEntry{
		ID:          "catalog-only",
		Name:        "Catalog Only",
		DisplayName: "Catalog Only Subscription",
		Type:        providerType,
		BaseURL:     "https://catalog-only.example/v1",
		Source:      "plugin:local_file",
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.pluginRegistry.Register(pluginmeta.Descriptor{
		ID:      "tokenhub.provider.catalog-only",
		Name:    "Catalog Only Provider",
		Version: "1.0.0",
		Source:  pluginmeta.SourceLocalFile,
		Kinds:   []pluginmeta.Kind{pluginmeta.KindProvider},
		Placements: []pluginmeta.Placement{
			pluginmeta.PlacementGatewayChain,
		},
		Capabilities: []pluginmeta.CapabilityDescriptor{
			{Kind: "provider_catalog", Name: "entry", Subject: providerType, Value: string(encoded)},
		},
	}); err != nil {
		t.Fatalf("register catalog-only plugin: %v", err)
	}

	merged, changed := server.providerCatalogEntriesWithPlugins(nil)
	if !changed {
		t.Fatalf("catalog-only plugin catalog merge changed=%t entries=%+v", changed, merged)
	}
	var found ProviderCatalogEntry
	for _, candidate := range merged {
		if candidate.ID == entry.ID {
			found = candidate
			break
		}
	}
	if found.Type != providerType || found.DisplayName != entry.DisplayName || found.BaseURL != entry.BaseURL {
		t.Fatalf("catalog-only plugin entry = %+v", found)
	}
}

func TestProviderCatalogMergesBuiltInPluginMetadataWithCatalogModels(t *testing.T) {
	server := New(NewMemoryStore())
	entries := []ProviderCatalogEntry{{
		ID:          "openai",
		Name:        "Catalog OpenAI",
		DisplayName: "Catalog OpenAI",
		Type:        ProviderOpenAICompatible,
		BaseURL:     "https://catalog.example/v1",
		Source:      "local-provider-catalog",
		ModelsCount: 1,
		Models: []ProviderCatalogModel{{
			ID:   "catalog-model",
			Name: "catalog-model",
		}},
	}}

	merged, changed := server.providerCatalogEntriesWithPlugins(entries)
	if !changed {
		t.Fatalf("expected plugin metadata merge")
	}
	var openAI ProviderCatalogEntry
	for _, entry := range merged {
		if entry.ID == "openai" {
			openAI = entry
			break
		}
	}
	if openAI.Name != "OpenAI" || openAI.Type != ProviderOpenAI || openAI.BaseURL != "https://api.openai.com/v1" || openAI.Source != "plugin:built_in" {
		t.Fatalf("OpenAI catalog metadata was not plugin-backed: %+v", openAI)
	}
	if len(openAI.Models) != 1 || openAI.Models[0].ID != "catalog-model" || openAI.ModelsCount != 1 {
		t.Fatalf("OpenAI catalog models were not preserved: %+v", openAI.Models)
	}
}

func TestAdminProviderCatalogItemMergesBuiltInPluginMetadataWithModels(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "plugin-catalog-admin"})

	response := doJSON(t, server.Handler(), http.MethodGet, "/api/admin/provider-catalog/openai", nil, "plugin-catalog-admin")
	if response.Code != http.StatusOK {
		t.Fatalf("GET OpenAI catalog: expected 200, got %d: %s", response.Code, response.Body)
	}
	if !strings.Contains(response.Body, `"source":"builtin"`) || !strings.Contains(response.Body, `"source":"plugin:built_in"`) {
		t.Fatalf("OpenAI catalog did not expose plugin-backed seed metadata: %s", response.Body)
	}
	if !strings.Contains(response.Body, `"id":"gpt-5"`) {
		t.Fatalf("OpenAI catalog did not preserve builtin models: %s", response.Body)
	}
}
