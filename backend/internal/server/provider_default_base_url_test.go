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

func TestProviderStoreCreateUsesConfiguredPluginDefaultBaseURL(t *testing.T) {
	store := NewMemoryStore()
	store.ConfigureProviderTypeDefaults(map[string]string{
		"default_base_url_plugin": "https://default.example/v1/",
	})

	provider := store.AddProvider(Provider{
		Name:    "Plugin Default",
		Type:    "default_base_url_plugin",
		Status:  StatusActive,
		Healthy: true,
	})
	if provider.BaseURL != "https://default.example/v1" {
		t.Fatalf("provider base URL = %q, want configured plugin default", provider.BaseURL)
	}
}

func TestProviderTypeDefaultBaseURLsFromRegistry(t *testing.T) {
	registry := NewAdapterRegistryWithPlugins(pluginmeta.NewRegistry())
	providerType := "registry_default_base_url_plugin"
	descriptor := pluginmeta.BuiltInProvider("tokenhub.provider.registry-default-base-url", "Registry Default Base URL", []string{providerType}, nil)
	descriptor.Capabilities = append(descriptor.Capabilities, pluginmeta.CapabilityDescriptor{
		Kind:    "provider_policy",
		Name:    "default_base_url",
		Subject: providerType,
		Value:   "https://registry-default.example/v1/",
	})
	if err := registry.RegisterPlugin(descriptor, AdapterRegistration{Type: providerType, Adapter: MockAdapter{}}); err != nil {
		t.Fatalf("register provider plugin: %v", err)
	}

	defaults := providerTypeDefaultBaseURLsFromRegistry(registry)
	if defaults[providerType] != "https://registry-default.example/v1" {
		t.Fatalf("provider default base URLs = %+v, want trimmed plugin default", defaults)
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
