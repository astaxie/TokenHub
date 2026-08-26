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
	if !ok || !strings.Contains(stored.Options[codexResourceSupportedModelsOption], "adapter-model") {
		t.Fatalf("adapter catalog models were not persisted: ok=%v options=%v", ok, stored.Options)
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
			codexResourceModelsETagOption:   "etag-cached",
			codexResourceModelCatalogOption: string(catalog),
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
