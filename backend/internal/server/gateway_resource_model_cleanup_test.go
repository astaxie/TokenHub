package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

type unsupportedModelResponsesAdapter struct{}

func (unsupportedModelResponsesAdapter) Responses(context.Context, Provider, string, ResponsesRequest) (any, Usage, error) {
	return nil, Usage{}, &ProviderInvocationError{
		Err:         NewHTTPError(http.StatusBadGateway, "provider_model_not_found", "Provider model is not available for this account"),
		Disposition: ProviderErrorModelUnsupported,
	}
}

func TestProviderResourceModelUnsupportedErrorRemovesPluginAccountModel(t *testing.T) {
	store := NewMemoryStore()
	server := New(store)
	providerType := "plugin_unsupported_model"
	if err := server.adapterRegistry.RegisterPlugin(pluginmeta.Descriptor{
		ID:      "tokenhub.provider.unsupported-model-cleanup",
		Name:    "Unsupported Model Cleanup",
		Version: "1.0.0",
		Source:  pluginmeta.SourceLocalFile,
		Kinds:   []pluginmeta.Kind{pluginmeta.KindProvider},
		Placements: []pluginmeta.Placement{
			pluginmeta.PlacementGatewayChain,
		},
		Capabilities: []pluginmeta.CapabilityDescriptor{
			{Kind: "provider", Name: string(AdapterCapabilityResponses), Subject: providerType},
		},
	}, AdapterRegistration{
		Type:         providerType,
		Adapter:      unsupportedModelResponsesAdapter{},
		Capabilities: []AdapterCapability{AdapterCapabilityResponses},
	}); err != nil {
		t.Fatalf("register plugin adapter: %v", err)
	}
	provider := store.AddProvider(Provider{
		ID: "prv_plugin_unsupported_model", Name: "Plugin Unsupported Model", Type: providerType,
		Status: StatusActive, Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_plugin_unsupported_model", ProviderID: provider.ID, Name: "Plugin Account",
		ResourceType: "plugin_unsupported_model_account", Status: StatusActive, Healthy: true,
		Options: codexCapabilityOptionsForTest("plugin-missing-model", "plugin-other-model"),
	})
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "plugin-missing-model", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{
		ID: "route_plugin_unsupported_model", ModelName: "plugin-missing-model",
		ProviderID: provider.ID, ProviderResourceID: resource.ID,
		ProviderModel: "plugin-missing-model", Status: StatusActive,
	})
	routes, err := store.SelectRouteCandidates("plugin-missing-model")
	if err != nil {
		t.Fatal(err)
	}

	_, _, _, attempts, err := server.executeRoutedResponses(httptest.NewRequest(http.MethodPost, "/v1/responses", nil), RoutedCall{
		Call:   CallContext{RequestID: "req_plugin_unsupported_model", Model: Model{Name: "plugin-missing-model", Status: StatusActive}},
		Routes: routes,
	}, ResponsesRequest{Model: "plugin-missing-model", Input: "hi"})
	if err == nil {
		t.Fatal("expected unsupported model error")
	}
	if len(attempts) != 1 || attempts[0].ErrorCode != "provider_model_not_found" {
		t.Fatalf("attempts = %+v", attempts)
	}
	updated, ok := store.GetProviderResource(resource.ID)
	if !ok {
		t.Fatal("plugin account resource disappeared")
	}
	models, _, cached := providerResourceCachedModels(&updated)
	if !cached || codexModelInList("plugin-missing-model", models) || !codexModelInList("plugin-other-model", models) {
		t.Fatalf("unsupported plugin model was not removed from resource cache: cached=%v models=%v", cached, models)
	}
	if updated.FailureCount != 0 {
		t.Fatalf("unsupported model must not degrade plugin account health: %+v", updated)
	}
}
