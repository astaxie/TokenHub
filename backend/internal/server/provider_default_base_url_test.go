package server

import (
	"context"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestProviderCreateUsesPluginDefaultBaseURL(t *testing.T) {
	server := New(NewMemoryStore())
	providerType := "default_base_url_plugin"
	descriptor := pluginmeta.BuiltInProvider("tokenhub.provider.default-base-url", "Default Base URL", []string{providerType}, []string{string(AdapterCapabilityChat)})
	descriptor.Capabilities = append(descriptor.Capabilities, pluginmeta.CapabilityDescriptor{
		Kind:    "provider_policy",
		Name:    "default_base_url",
		Subject: providerType,
		Value:   "https://default.example/v1/",
	})
	if err := server.adapterRegistry.RegisterPlugin(descriptor, AdapterRegistration{Type: providerType, Adapter: MockAdapter{}, Capabilities: []AdapterCapability{AdapterCapabilityChat}}); err != nil {
		t.Fatalf("register provider plugin: %v", err)
	}

	provider, _, _, err := server.providerForCreate(context.Background(), ProviderCreateRequest{
		Name:    "Plugin Default",
		Type:    providerType,
		Status:  StatusActive,
		Healthy: ptrBool(true),
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	if provider.BaseURL != "https://default.example/v1" {
		t.Fatalf("provider base URL = %q, want plugin default", provider.BaseURL)
	}
}

func TestProviderCreateUsesBuiltInPluginDefaultBaseURL(t *testing.T) {
	server := New(NewMemoryStore())

	provider, _, _, err := server.providerForCreate(context.Background(), ProviderCreateRequest{
		Name:    "OpenAI",
		Type:    ProviderOpenAI,
		Status:  StatusActive,
		Healthy: ptrBool(true),
	})
	if err != nil {
		t.Fatalf("create OpenAI provider: %v", err)
	}
	if provider.BaseURL != "https://api.openai.com/v1" {
		t.Fatalf("provider base URL = %q, want built-in plugin default", provider.BaseURL)
	}
}

func TestNormalizeProviderBaseURLDoesNotSupplyBuiltInDefaults(t *testing.T) {
	for _, providerID := range []string{"openai", "anthropic", "google", "ollama", "lmstudio", ProviderKronk} {
		if got := normalizeProviderBaseURL(providerID, ""); got != "" {
			t.Fatalf("normalizeProviderBaseURL(%q, empty) = %q, want empty", providerID, got)
		}
	}
}

func ptrBool(value bool) *bool {
	return &value
}
