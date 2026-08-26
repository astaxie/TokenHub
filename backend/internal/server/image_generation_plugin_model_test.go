package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestPluginImagePublicModelUsesActionMetadataRoute(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Plugin Image Project", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name: "Plugin Image Key", Allowed: []string{"kimi-image"}, Status: StatusActive,
	}, "thk_plugin_image_public_model")
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "kimi-image", Modality: "image", Status: StatusActive})
	provider := store.AddProvider(Provider{
		ID: "prv_kimi_image_gateway", Name: "Kimi Image Gateway", Type: "kimi_subscription",
		Status: StatusActive, Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_kimi_image_gateway", ProviderID: provider.ID, Name: "Kimi Image Gateway Account",
		ResourceType: "kimi_subscription_account", Status: StatusActive, Healthy: true,
		Options: map[string]string{"kimi_image_capability": "available"},
	})
	if err != nil {
		t.Fatal(err)
	}
	store.AddRoute(ModelRoute{
		ModelName: "kimi-image", ProviderID: provider.ID, ProviderResourceID: resource.ID,
		ProviderModel: "moonshot-image", Status: StatusActive, Priority: 1, Weight: 100,
	})
	var requestedModel string
	adapter := pluginPublicImageAdapter{
		image: realPNGFixture(t),
		model: &requestedModel,
	}
	server := NewWithConfig(store, Config{AdminToken: "plugin-image-admin", SecretKey: "plugin-image-secret", ImageStorageDir: t.TempDir()})
	pluginID := "tokenhub.provider.kimi-image-gateway"
	if err := server.adapterRegistry.RegisterPlugin(pluginmeta.BuiltInProvider(pluginID, "Kimi Image Gateway", []string{"kimi_subscription"}, []string{string(AdapterCapabilityImageGenerate)}), AdapterRegistration{
		Type:         "kimi_subscription",
		Adapter:      adapter,
		Capabilities: []AdapterCapability{AdapterCapabilityImageGenerate},
	}); err != nil {
		t.Fatalf("register plugin image adapter: %v", err)
	}
	if err := server.pluginActions.Register(pluginmeta.ActionDescriptor{
		PluginID:   pluginID,
		ActionID:   "kimi.image_capability.configure",
		Kind:       pluginmeta.ActionKindMutate,
		Capability: "image.capability.configure",
		Subject:    "kimi_subscription",
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
		t.Fatalf("register plugin image action: %v", err)
	}

	response := doImageJSON(t, server.Handler(), http.MethodPost, "/v1/images/generations", map[string]any{
		"model":           "kimi-image",
		"prompt":          "draw with plugin public model",
		"response_format": "b64_json",
	}, secret, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("plugin image generation: status=%d body=%s", response.Code, response.Body)
	}
	if requestedModel != "moonshot-image" {
		t.Fatalf("plugin image adapter saw model %q, want moonshot-image", requestedModel)
	}
	if !strings.Contains(response.Body, `"b64_json"`) || !strings.Contains(response.Body, "plugin image revised") {
		t.Fatalf("plugin image response did not come from adapter: %s", response.Body)
	}
}

type pluginPublicImageAdapter struct {
	image []byte
	model *string
}

func (a pluginPublicImageAdapter) GenerateImage(_ context.Context, provider Provider, providerModel string, req ProviderImageGenerationRequest) ([]byte, string, Usage, error) {
	if provider.Type != "kimi_subscription" || providerModel != "moonshot-image" || req.Model != "moonshot-image" {
		return nil, "", Usage{}, fmt.Errorf("unexpected plugin image route provider=%s provider_model=%s request_model=%s", provider.Type, providerModel, req.Model)
	}
	if a.model != nil {
		*a.model = req.Model
	}
	return a.image, "plugin image revised", Usage{PromptTokens: 2, CompletionTokens: 3, TotalTokens: 5, ServedModel: providerModel}, nil
}
