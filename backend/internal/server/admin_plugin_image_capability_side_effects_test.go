package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestProviderImageCapabilityActionMetadataAppliesRouteSideEffects(t *testing.T) {
	store := NewMemoryStore()
	providerType := "image_capability_side_effect_plugin"
	publicModel := "plugin-public-image"
	upstreamModel := "plugin-upstream-image"
	resourceType := "plugin_subscription_account"
	capabilityOption := "plugin_image_capability"
	checkedAtOption := "plugin_image_capability_checked_at"
	backfillOption := "plugin_image_capability_route_backfill_v1"
	supportedValue := "available"
	store.AddModel(Model{Name: publicModel, Modality: "image", Status: StatusActive})
	provider := store.AddProvider(Provider{
		ID: "prv_image_capability_side_effect", Name: "Image Capability Side Effect", Type: providerType,
		Status: StatusActive, Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_image_capability_side_effect", ProviderID: provider.ID, Name: "Image Capability Side Effect Resource",
		ResourceType: resourceType, Status: StatusActive, Healthy: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := New(store)
	pluginID := "tokenhub.provider.image-capability-side-effect"
	if err := server.adapterRegistry.RegisterPlugin(pluginmeta.BuiltInProvider(pluginID, "Image Capability Side Effect", []string{providerType}, []string{string(AdapterCapabilityImageGenerate)}), AdapterRegistration{
		Type:         providerType,
		Adapter:      MockAdapter{},
		Capabilities: []AdapterCapability{AdapterCapabilityImageGenerate},
	}); err != nil {
		t.Fatalf("register provider plugin: %v", err)
	}
	if err := server.pluginActions.Register(pluginmeta.ActionDescriptor{
		PluginID:   pluginID,
		ActionID:   "image_capability.configure",
		Kind:       pluginmeta.ActionKindMutate,
		Capability: "image.capability.configure",
		Subject:    providerType,
		Metadata: map[string]string{
			"provider_resource_type":       resourceType,
			"public_model":                 publicModel,
			"upstream_model":               upstreamModel,
			"capability_option":            capabilityOption,
			"capability_checked_at_option": checkedAtOption,
			"capability_supported_value":   supportedValue,
			"route_backfill_option":        backfillOption,
		},
	}, pluginmeta.ActionHandlerFunc(func(_ context.Context, invocation pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
		var payload struct {
			ResourceID string `json:"resource_id"`
			Enabled    bool   `json:"enabled"`
		}
		if err := json.Unmarshal(invocation.Payload, &payload); err != nil {
			t.Fatalf("decode image capability payload: %v", err)
		}
		if payload.ResourceID != resource.ID || !payload.Enabled {
			t.Fatalf("unexpected image capability payload: %+v", payload)
		}
		return pluginmeta.ActionResult{Data: map[string]any{
			"enabled":     true,
			"tested":      true,
			"capability":  supportedValue,
			"resource_id": resource.ID,
		}}, nil
	})); err != nil {
		t.Fatalf("register image capability action: %v", err)
	}

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/provider-resources/"+resource.ID+"/image-capability", map[string]any{
		"enabled": true,
	}, "dev_admin_token")
	if response.Code != http.StatusOK {
		t.Fatalf("POST plugin image capability: expected 200, got %d: %s", response.Code, response.Body)
	}
	updated, ok := store.GetProviderResource(resource.ID)
	if !ok || updated.Options[capabilityOption] != supportedValue || updated.Options[checkedAtOption] == "" || updated.Options[backfillOption] != "completed" {
		t.Fatalf("image capability side effect did not update resource options: %+v", updated.Options)
	}
	if updated.Options[codexImageCapabilityOption] != "" || updated.Options[codexImageCapabilityCheckedAtOption] != "" {
		t.Fatalf("third-party image capability side effect wrote Codex option keys: %+v", updated.Options)
	}
	routes := store.ListRoutes()
	if len(routes) != 1 || routes[0].ModelName != publicModel || routes[0].ProviderID != provider.ID || routes[0].ProviderModel != upstreamModel || routes[0].Status != StatusActive {
		t.Fatalf("image capability side effect routes = %+v", routes)
	}
	if !strings.Contains(response.Body, `"route_id":"`+routes[0].ID+`"`) {
		t.Fatalf("image capability response did not include side-effect route: %s", response.Body)
	}
}
