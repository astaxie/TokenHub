package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

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
