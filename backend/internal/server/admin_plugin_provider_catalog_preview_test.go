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
