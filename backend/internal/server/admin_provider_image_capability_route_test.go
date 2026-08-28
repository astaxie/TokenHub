package server

import (
	"net/http"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestAdminPluginImageVirtualModelRouteUsesPluginDeclaredValidationErrors(t *testing.T) {
	store := NewMemoryStore()
	const (
		providerType  = "plugin_image_route"
		publicModel   = "plugin-image-model"
		upstreamModel = "plugin-image-upstream"
	)
	store.AddModel(Model{Name: publicModel, Modality: "image", Status: StatusActive})
	imageProvider := store.AddProvider(Provider{
		ID: "prv_plugin_image_route", Name: "Plugin Image Route", Type: providerType,
		Status: StatusActive, Healthy: true,
	})
	otherProvider := store.AddProvider(Provider{
		ID: "prv_plugin_image_other", Name: "Plugin Image Other", Type: ProviderOpenAI,
		Status: StatusActive, Healthy: true,
	})
	server := New(store)
	pluginID := "tokenhub.provider.plugin-image-route"
	if err := server.adapterRegistry.RegisterPlugin(pluginmeta.BuiltInProvider(pluginID, "Plugin Image Route", []string{providerType}, []string{string(AdapterCapabilityImageGenerate)}), AdapterRegistration{
		Type:         providerType,
		Adapter:      MockAdapter{},
		Capabilities: []AdapterCapability{AdapterCapabilityImageGenerate},
	}); err != nil {
		t.Fatalf("register plugin image provider: %v", err)
	}
	if err := server.pluginActions.RegisterDescriptor(pluginmeta.ActionDescriptor{
		PluginID:   pluginID,
		ActionID:   "plugin_image.image_capability.configure",
		Kind:       pluginmeta.ActionKindMutate,
		Capability: "image.capability.configure",
		Subject:    providerType,
		Metadata: map[string]string{
			"public_model":                    publicModel,
			"upstream_model":                  upstreamModel,
			"route_error.provider.code":       "plugin_image_provider_required",
			"route_error.upstream_model.code": "plugin_image_upstream_model_invalid",
			"route_error.capability.code":     "plugin_image_capability_required",
			"route_error.capability.message":  "Plugin image capability must be tested before activation",
			"capability_option":               "plugin_image_capability",
			"capability_checked_at_option":    "plugin_image_capability_checked_at",
			"capability_supported_value":      "yes",
			"capability_unsupported_value":    "no",
			"provider_route_backfill_option":  "plugin_image_route_backfilled",
			"provider_route_backfill_value":   "yes",
		},
	}); err != nil {
		t.Fatalf("register plugin image action descriptor: %v", err)
	}
	app := server.Handler()

	wrongProvider := doJSON(t, app, http.MethodPost, "/api/admin/routing-rules", map[string]any{
		"model_name": publicModel, "provider_id": otherProvider.ID,
		"provider_model": upstreamModel, "status": StatusActive,
	}, "")
	if wrongProvider.Code != http.StatusBadRequest || !strings.Contains(wrongProvider.Body, "plugin_image_provider_required") {
		t.Fatalf("expected plugin-declared provider validation, got %d: %s", wrongProvider.Code, wrongProvider.Body)
	}

	wrongModel := doJSON(t, app, http.MethodPost, "/api/admin/routing-rules", map[string]any{
		"model_name": publicModel, "provider_id": imageProvider.ID,
		"provider_model": "different-upstream", "status": StatusActive,
	}, "")
	if wrongModel.Code != http.StatusBadRequest || !strings.Contains(wrongModel.Body, "plugin_image_upstream_model_invalid") {
		t.Fatalf("expected plugin-declared upstream model validation, got %d: %s", wrongModel.Code, wrongModel.Body)
	}

	activeWithoutCapability := doJSON(t, app, http.MethodPost, "/api/admin/routing-rules", map[string]any{
		"model_name": publicModel, "provider_id": imageProvider.ID,
		"provider_model": upstreamModel, "status": StatusActive,
	}, "")
	if activeWithoutCapability.Code != http.StatusConflict || !strings.Contains(activeWithoutCapability.Body, "plugin_image_capability_required") {
		t.Fatalf("expected plugin-declared capability validation, got %d: %s", activeWithoutCapability.Code, activeWithoutCapability.Body)
	}
}

func TestProviderImageCapabilityClusterKeyUsesPluginProfilePrefix(t *testing.T) {
	if key := providerImageCapabilityClusterKey(providerImageCapabilityRouteProfile{}, "prv_plugin"); key != "provider-image-capability:prv_plugin" {
		t.Fatalf("default provider image capability key = %q", key)
	}
	profile := providerImageCapabilityProfileFromAction(pluginmeta.ActionDescriptor{
		Metadata: map[string]string{"operation_key_prefix": "plugin-image-lock"},
	})
	if key := providerImageCapabilityClusterKey(profile, "prv_plugin"); key != "plugin-image-lock:prv_plugin" {
		t.Fatalf("plugin provider image capability key = %q", key)
	}
	if key := codexImageCapabilityClusterKey("prv_codex"); key != "codex-image-capability:prv_codex" {
		t.Fatalf("codex image capability key = %q", key)
	}
}

func TestProviderImageCapabilityProbeErrorMessageUsesPluginMetadata(t *testing.T) {
	profile := providerImageCapabilityProfileFromAction(pluginmeta.ActionDescriptor{
		Metadata: map[string]string{
			"probe_error_message.plugin_rate_limited": "Plugin image capability test is rate limited",
		},
	})
	if message := providerImageCapabilityProbeErrorMessage(profile, "plugin_rate_limited"); message != "Plugin image capability test is rate limited" {
		t.Fatalf("plugin probe error message = %q", message)
	}
	if message := providerImageCapabilityProbeErrorMessage(providerImageCapabilityRouteProfile{}, "plugin_unknown"); message != "Provider image capability test failed" {
		t.Fatalf("default probe error message = %q", message)
	}
}
