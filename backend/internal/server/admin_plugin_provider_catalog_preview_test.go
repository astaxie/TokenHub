package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestAdminPluginProviderCatalogPostUsesModelsPreviewAction(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "plugin-catalog-admin"})
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
	var observed ProviderResourceCredentials
	var observedPayload map[string]any
	if err := server.pluginActions.Register(pluginmeta.ActionDescriptor{
		PluginID:   pluginID,
		ActionID:   "preview_catalog.models",
		Kind:       pluginmeta.ActionKindRead,
		Capability: "models.preview",
		Subject:    providerType,
	}, pluginmeta.ActionHandlerFunc(func(_ context.Context, invocation pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
		if err := json.Unmarshal(invocation.Payload, &observedPayload); err != nil {
			t.Fatalf("decode preview catalog raw payload: %v", err)
		}
		if err := json.Unmarshal(invocation.Payload, &observed); err != nil {
			t.Fatalf("decode preview catalog payload: %v", err)
		}
		return pluginmeta.ActionResult{Data: ProviderCatalogEntry{
			ID: providerType, Name: "Preview Catalog Provider", DisplayName: "Preview Catalog Provider",
			Type: providerType, ModelsCount: 1, Source: "plugin-preview",
			Models: []ProviderCatalogModel{{ID: "preview-model", Name: "preview-model"}},
		}}, nil
	})); err != nil {
		t.Fatalf("register preview catalog action: %v", err)
	}

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/provider-catalog/"+providerType, ProviderResourceCredentials{
		AccessToken: "plugin-preview-token",
		AccountID:   "plugin-preview-account",
	}, "plugin-catalog-admin")
	if response.Code != http.StatusOK {
		t.Fatalf("POST plugin provider catalog: expected 200, got %d: %s", response.Code, response.Body)
	}
	if observed.AccessToken != "plugin-preview-token" || observed.AccountID != "plugin-preview-account" {
		t.Fatalf("preview credentials = %+v", observed)
	}
	if observedPayload["type"] != providerType || observedPayload["catalog_id"] != providerType {
		t.Fatalf("preview payload plugin metadata = %+v", observedPayload)
	}
	if !strings.Contains(response.Body, `"source":"plugin-preview"`) || !strings.Contains(response.Body, `"id":"preview-model"`) {
		t.Fatalf("plugin catalog response = %s", response.Body)
	}
}

func TestAdminProviderTestConnectionAllowsPluginOptionalAPIKey(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "plugin-test-admin"})
	providerType := "optional_key_provider"
	pluginID := "tokenhub.provider.optional-key"
	if err := server.adapterRegistry.RegisterPlugin(pluginmeta.Descriptor{
		ID:      pluginID,
		Name:    "Optional Key Provider",
		Version: "1.0.0",
		Source:  pluginmeta.SourceLocalFile,
		Kinds:   []pluginmeta.Kind{pluginmeta.KindProvider},
		Capabilities: []pluginmeta.CapabilityDescriptor{
			{Kind: "provider", Name: string(AdapterCapabilityModels), Subject: providerType},
			{Kind: "provider_policy", Name: providerAPIKeyRequiredOption, Subject: providerType, Value: "false"},
		},
	}, AdapterRegistration{Type: providerType, Adapter: struct{}{}, Capabilities: []AdapterCapability{AdapterCapabilityModels}}); err != nil {
		t.Fatalf("register optional-key provider: %v", err)
	}
	var observed ProviderCreateRequest
	if err := server.pluginActions.Register(pluginmeta.ActionDescriptor{
		PluginID: pluginID, ActionID: "optional_key.models", Kind: pluginmeta.ActionKindRead,
		Capability: "models.preview", Subject: providerType,
	}, pluginmeta.ActionHandlerFunc(func(_ context.Context, invocation pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
		if err := json.Unmarshal(invocation.Payload, &observed); err != nil {
			t.Fatalf("decode test connection payload: %v", err)
		}
		return pluginmeta.ActionResult{Data: ProviderCatalogEntry{
			ID: providerType, Name: "Optional Key Provider", DisplayName: "Optional Key Provider",
			Type: providerType, ModelsCount: 1, Source: "plugin-preview",
			Models: []ProviderCatalogModel{{ID: "optional-model", Name: "optional-model"}},
		}}, nil
	})); err != nil {
		t.Fatalf("register optional-key models action: %v", err)
	}

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/providers/test-connection", map[string]any{
		"name": "Optional Key Provider", "type": providerType, "base_url": "https://optional.example/v1",
	}, "plugin-test-admin")
	if response.Code != http.StatusOK {
		t.Fatalf("POST provider test connection: expected 200, got %d: %s", response.Code, response.Body)
	}
	if observed.Type != providerType || observed.APIKey != "" {
		t.Fatalf("test connection payload = %+v", observed)
	}
	if !strings.Contains(response.Body, `"models_count":1`) {
		t.Fatalf("test connection response = %s", response.Body)
	}
}

func TestAdminProviderTestConnectionUsesPluginHealthProber(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "plugin-test-admin"})
	providerType := "health_preview_provider"
	adapter := &pluginHealthProbeAdapter{}
	pluginID := "tokenhub.provider.health-preview"
	if err := server.adapterRegistry.RegisterPlugin(pluginmeta.Descriptor{
		ID:      pluginID,
		Name:    "Health Preview Provider",
		Version: "1.0.0",
		Source:  pluginmeta.SourceLocalFile,
		Kinds:   []pluginmeta.Kind{pluginmeta.KindProvider},
		Capabilities: []pluginmeta.CapabilityDescriptor{
			{Kind: "provider", Name: string(AdapterCapabilityModels), Subject: providerType},
			{Kind: "provider", Name: string(AdapterCapabilityProbe), Subject: providerType},
			{Kind: "provider_policy", Name: providerAPIKeyRequiredOption, Subject: providerType, Value: "false"},
		},
	}, AdapterRegistration{
		Type: providerType, Adapter: adapter,
		Capabilities: []AdapterCapability{AdapterCapabilityModels, AdapterCapabilityProbe},
	}); err != nil {
		t.Fatalf("register health preview provider: %v", err)
	}
	if err := server.pluginActions.Register(pluginmeta.ActionDescriptor{
		PluginID: pluginID, ActionID: "health_preview.models", Kind: pluginmeta.ActionKindRead,
		Capability: "models.preview", Subject: providerType,
	}, pluginmeta.ActionHandlerFunc(func(context.Context, pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
		return pluginmeta.ActionResult{Data: ProviderCatalogEntry{
			ID: providerType, Name: "Health Preview Provider", DisplayName: "Health Preview Provider",
			Type: providerType, ModelsCount: 1, Source: "plugin-preview",
			Models: []ProviderCatalogModel{{ID: "health-model", Name: "health-model"}},
		}}, nil
	})); err != nil {
		t.Fatalf("register health preview models action: %v", err)
	}

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/providers/test-connection", map[string]any{
		"name": "Health Preview Provider", "type": providerType, "base_url": "https://health.example/v1",
	}, "plugin-test-admin")
	if response.Code != http.StatusOK {
		t.Fatalf("POST provider test connection: expected 200, got %d: %s", response.Code, response.Body)
	}
	if adapter.calls != 1 {
		t.Fatalf("health probe calls = %d, want 1", adapter.calls)
	}
	if !strings.Contains(response.Body, `"health":{"ok":true}`) {
		t.Fatalf("test connection response = %s", response.Body)
	}
}

func TestProviderCatalogMultiMethodRouteIDsComeFromModelsPreviewPlugins(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "plugin-catalog-admin"})
	providerType := "preview_route_provider"
	pluginID := "tokenhub.provider.preview-route"
	if err := server.adapterRegistry.RegisterPlugin(pluginmeta.Descriptor{
		ID:      pluginID,
		Name:    "Preview Route Provider",
		Version: "1.0.0",
		Source:  pluginmeta.SourceLocalFile,
		Kinds:   []pluginmeta.Kind{pluginmeta.KindProvider},
		Capabilities: []pluginmeta.CapabilityDescriptor{
			{Kind: "provider", Name: string(AdapterCapabilityModels), Subject: providerType},
			{Kind: "provider_catalog", Name: "entry", Subject: providerType, Value: `{"id":"preview-route","name":"Preview Route","type":"preview_route_provider"}`},
		},
	}, AdapterRegistration{
		Type:         providerType,
		Adapter:      struct{}{},
		Capabilities: []AdapterCapability{AdapterCapabilityModels},
	}); err != nil {
		t.Fatalf("register preview route provider: %v", err)
	}
	if err := server.pluginActions.Register(pluginmeta.ActionDescriptor{
		PluginID:   pluginID,
		ActionID:   "preview_route.models",
		Kind:       pluginmeta.ActionKindRead,
		Capability: "models.preview",
		Subject:    providerType,
	}, pluginmeta.ActionHandlerFunc(func(context.Context, pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
		return pluginmeta.ActionResult{}, nil
	})); err != nil {
		t.Fatalf("register preview route action: %v", err)
	}

	ids := server.providerCatalogMultiMethodRouteIDs()
	if !stringInList("custom", ids) || !stringInList("preview-route", ids) {
		t.Fatalf("multi-method catalog route IDs = %v", ids)
	}
	if stringInList(providerType, ids) {
		t.Fatalf("multi-method catalog route IDs used provider type instead of catalog ID: %v", ids)
	}
}
