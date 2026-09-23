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

func TestAdminPluginImageVirtualModelRouteUsesActionMetadata(t *testing.T) {
	store := NewMemoryStore()
	store.AddModel(Model{Name: "kimi-image", Modality: "image", Status: StatusActive})
	providerType := "kimi_subscription"
	provider := store.AddProvider(Provider{
		ID: "prv_kimi_image_route", Name: "Kimi Image Route", Type: providerType, Status: StatusActive, Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_kimi_image_route_supported", ProviderID: provider.ID,
		Name:         "Kimi Image Route Supported Account",
		ResourceType: "kimi_subscription_account",
		Status:       StatusActive,
		Healthy:      true,
		Options:      map[string]string{"kimi_image_capability": "available"},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := New(store)
	pluginID := "tokenhub.provider.kimi-image"
	if err := server.adapterRegistry.RegisterPlugin(pluginmeta.BuiltInProvider(pluginID, "Kimi Image", []string{providerType}, []string{string(AdapterCapabilityImageGenerate)}), AdapterRegistration{
		Type:         providerType,
		Adapter:      MockAdapter{},
		Capabilities: []AdapterCapability{AdapterCapabilityImageGenerate},
	}); err != nil {
		t.Fatalf("register Kimi image plugin: %v", err)
	}
	if err := server.pluginActions.Register(pluginmeta.ActionDescriptor{
		PluginID:   pluginID,
		ActionID:   "kimi.image_capability.configure",
		Kind:       pluginmeta.ActionKindMutate,
		Capability: "image.capability.configure",
		Subject:    providerType,
		Metadata: map[string]string{
			"provider_resource_type":     "kimi_subscription_account",
			"public_model":               "kimi-image",
			"upstream_model":             "moonshot-image",
			"capability_option":          "kimi_image_capability",
			"capability_supported_value": "available",
		},
	}, pluginmeta.ActionHandlerFunc(func(context.Context, pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
		return pluginmeta.ActionResult{}, nil
	})); err != nil {
		t.Fatalf("register Kimi image action: %v", err)
	}
	app := server.Handler()

	activeWithoutCapability := doJSON(t, app, http.MethodPost, "/api/admin/routing-rules", map[string]any{
		"model_name": "kimi-image", "provider_id": provider.ID,
		"provider_model": "moonshot-image", "status": StatusActive, "resource_group": "missing",
	}, "")
	if activeWithoutCapability.Code != http.StatusConflict || !strings.Contains(activeWithoutCapability.Body, "provider_image_capability_required") {
		t.Fatalf("expected plugin image capability validation, got %d: %s", activeWithoutCapability.Code, activeWithoutCapability.Body)
	}

	activeWithCapability := doJSON(t, app, http.MethodPost, "/api/admin/routing-rules", map[string]any{
		"model_name": "kimi-image", "provider_id": provider.ID,
		"provider_model": "moonshot-image", "status": StatusActive, "provider_resource_id": resource.ID,
	}, "")
	if activeWithCapability.Code != http.StatusCreated {
		t.Fatalf("expected plugin image route to be created, got %d: %s", activeWithCapability.Code, activeWithCapability.Body)
	}
}

func TestProviderImageCapabilityResourceUpdateCleansMetadataKeys(t *testing.T) {
	store := NewMemoryStore()
	providerType := "kimi_subscription"
	resourceType := "kimi_subscription_account"
	publicModel := "kimi-image"
	upstreamModel := "moonshot-image"
	capabilityOption := "kimi_image_capability"
	checkedAtOption := "kimi_image_capability_checked_at"
	backfillOption := "kimi_image_route_backfill_v1"
	supportedValue := "available"
	store.AddModel(Model{Name: publicModel, Modality: "image", Status: StatusActive})
	provider := store.AddProvider(Provider{
		ID: "prv_kimi_image_cleanup", Name: "Kimi Image Cleanup", Type: providerType,
		Status: StatusActive, Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_kimi_image_cleanup", ProviderID: provider.ID, Name: "Kimi Image Cleanup Account",
		ResourceType: resourceType, Status: StatusActive, Healthy: true,
		Options: map[string]string{
			capabilityOption:            supportedValue,
			checkedAtOption:             "2026-08-26T00:00:00Z",
			backfillOption:              "completed",
			"operator_visible_metadata": "retained",
		},
		Credentials: &ProviderResourceCredentials{
			AccessToken: "old-access", RefreshToken: "old-refresh", AccountID: "old-account",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	route := store.AddRoute(ModelRoute{
		ID: "route_kimi_image_cleanup", ModelName: publicModel, ProviderID: provider.ID,
		ProviderResourceID: resource.ID, ProviderModel: upstreamModel, Status: StatusActive,
	})
	server := New(store)
	pluginID := "tokenhub.provider.kimi-cleanup"
	if err := server.pluginActions.Register(pluginmeta.ActionDescriptor{
		PluginID:   pluginID,
		ActionID:   "kimi.image_capability.configure",
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
	}, pluginmeta.ActionHandlerFunc(func(context.Context, pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
		return pluginmeta.ActionResult{}, nil
	})); err != nil {
		t.Fatalf("register Kimi image cleanup action: %v", err)
	}

	response := doJSON(t, server.Handler(), http.MethodPatch, "/api/admin/provider-resources/"+resource.ID, ProviderResource{
		ProviderID: provider.ID, Name: resource.Name, ResourceType: resource.ResourceType,
		Status: StatusActive, Healthy: true, Weight: resource.Weight,
		Options: map[string]string{"operator_visible_metadata": "retained"},
		Credentials: &ProviderResourceCredentials{
			AccessToken: "new-access", RefreshToken: "new-refresh", AccountID: "new-account",
		},
	}, "")
	if response.Code != http.StatusOK {
		t.Fatalf("replace plugin account credentials: status=%d body=%s", response.Code, response.Body)
	}
	updated, ok := store.GetProviderResource(resource.ID)
	if !ok {
		t.Fatal("plugin account disappeared after credential replacement")
	}
	if updated.Options[capabilityOption] != "" || updated.Options[checkedAtOption] != "" || updated.Options[backfillOption] != "" {
		t.Fatalf("replacement credentials inherited plugin image capability state: %+v", updated.Options)
	}
	if updated.Options[codexImageCapabilityOption] != "" || updated.Options["operator_visible_metadata"] != "retained" {
		t.Fatalf("resource update touched unrelated options: %+v", updated.Options)
	}
	var disabled ModelRoute
	if err := store.db.First(&disabled, "id = ?", route.ID).Error; err != nil {
		t.Fatal(err)
	}
	if disabled.Status != StatusDisabled {
		t.Fatalf("plugin image route status = %q, want %q", disabled.Status, StatusDisabled)
	}
}

func TestProviderImageCapabilityResourceDeleteDisablesMetadataRoute(t *testing.T) {
	store := NewMemoryStore()
	providerType := "gemini_subscription"
	resourceType := "gemini_subscription_account"
	publicModel := "gemini-image"
	upstreamModel := "gemini-upstream-image"
	capabilityOption := "gemini_image_capability"
	supportedValue := "ready"
	provider := store.AddProvider(Provider{
		ID: "prv_gemini_image_delete", Name: "Gemini Image Delete", Type: providerType,
		Status: StatusActive, Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_gemini_image_delete", ProviderID: provider.ID, Name: "Gemini Image Delete Account",
		ResourceType: resourceType, Status: StatusActive, Healthy: true,
		Options: map[string]string{
			capabilityOption: supportedValue,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	route := store.AddRoute(ModelRoute{
		ID: "route_gemini_image_delete", ModelName: publicModel, ProviderID: provider.ID,
		ProviderResourceID: resource.ID, ProviderModel: upstreamModel, Status: StatusActive,
	})
	server := New(store)
	if err := server.pluginActions.Register(pluginmeta.ActionDescriptor{
		PluginID:   "tokenhub.provider.gemini-delete",
		ActionID:   "gemini.image_capability.configure",
		Kind:       pluginmeta.ActionKindMutate,
		Capability: "image.capability.configure",
		Subject:    providerType,
		Metadata: map[string]string{
			"provider_resource_type":     resourceType,
			"public_model":               publicModel,
			"upstream_model":             upstreamModel,
			"capability_option":          capabilityOption,
			"capability_supported_value": supportedValue,
		},
	}, pluginmeta.ActionHandlerFunc(func(context.Context, pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
		return pluginmeta.ActionResult{}, nil
	})); err != nil {
		t.Fatalf("register Gemini image delete action: %v", err)
	}

	response := doJSON(t, server.Handler(), http.MethodDelete, "/api/admin/provider-resources/"+resource.ID, nil, "")
	if response.Code != http.StatusNoContent {
		t.Fatalf("delete plugin image resource: status=%d body=%s", response.Code, response.Body)
	}
	var disabled ModelRoute
	if err := store.db.First(&disabled, "id = ?", route.ID).Error; err != nil {
		t.Fatal(err)
	}
	if disabled.Status != StatusDisabled || disabled.ProviderResourceID != "" {
		t.Fatalf("plugin image route after resource delete = %+v", disabled)
	}
}
