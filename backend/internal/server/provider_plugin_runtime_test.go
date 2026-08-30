package server

import (
	"net/http"
	"testing"
	"time"

	pluginmeta "tokenhub/backend/internal/plugin"
)

type configurableProviderAdapterForTest struct {
	supportsResourceModel func(providerType string, resourceType string) bool
	imageProfiles         func(providerType string) []providerImageCapabilityRouteProfile
}

func (a *configurableProviderAdapterForTest) ConfigureProviderResourceModelSupport(supports func(providerType string, resourceType string) bool) {
	a.supportsResourceModel = supports
}

func (a *configurableProviderAdapterForTest) ConfigureProviderImageCapabilityProfiles(profiles func(providerType string) []providerImageCapabilityRouteProfile) {
	a.imageProfiles = profiles
}

func TestBuiltinProviderRuntimeBuildsAdaptersForPluginRegistration(t *testing.T) {
	store := NewMemoryStore()
	client := &http.Client{Timeout: time.Second}
	streamClient := &http.Client{Timeout: 2 * time.Second}

	runtime := newBuiltinProviderRuntime(builtinProviderRuntimeDependencies{
		Store:             store,
		Client:            client,
		StreamClient:      streamClient,
		StreamIdleTimeout: 3 * time.Second,
	})

	for _, providerType := range []string{
		ProviderMock,
		ProviderOpenAI,
		ProviderOpenAICompatible,
		"deepseek",
		"qwen",
		"local",
		ProviderKronk,
		ProviderAzureOpenAI,
		ProviderAnthropic,
		ProviderGemini,
	} {
		if runtime.adapters[providerType] == nil {
			t.Fatalf("runtime adapter %q was not built", providerType)
		}
	}
	openai, ok := runtime.adapters[ProviderOpenAI].(OpenAICompatibleAdapter)
	if !ok || openai.Client != client || openai.StreamClient != streamClient || openai.StreamIdleTimeout != 3*time.Second {
		t.Fatalf("OpenAI runtime adapter = %+v ok=%t", openai, ok)
	}
	if _, ok := runtime.adapters[ProviderKronk].(KronkAdapter); !ok {
		t.Fatalf("Kronk runtime adapter = %T, want KronkAdapter", runtime.adapters[ProviderKronk])
	}
	codexSubscription := codexSubscriptionAdapterFrom(runtime.adapters)
	if codexSubscription == nil {
		t.Fatal("Codex subscription runtime adapter was not built")
	}
	if codexSubscription.StreamIdleTimeout != 3*time.Second {
		t.Fatalf("Codex stream idle timeout = %v, want 3s", codexSubscription.StreamIdleTimeout)
	}
	if codexSubscription.Client == nil || codexSubscription.Client.Transport == nil {
		t.Fatal("Codex subscription client or transport was not configured")
	}
	if codexSubscription.RefreshCredentials == nil {
		t.Fatal("Codex subscription credential refresh callback was not configured")
	}
}

func TestServerCodexSubscriptionAdapterResolvesFromRegistry(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token"})
	raw, err := server.adapterRegistry.Resolve(ProviderOpenAICodex)
	if err != nil {
		t.Fatalf("resolve registered Codex subscription adapter: %v", err)
	}
	registered, ok := raw.(*CodexSubscriptionAdapter)
	if !ok || registered == nil {
		t.Fatalf("registered Codex adapter = %T, want *CodexSubscriptionAdapter", raw)
	}

	resolved, err := server.codexSubscriptionAdapter()
	if err != nil {
		t.Fatalf("resolve Codex subscription adapter: %v", err)
	}
	if resolved != registered {
		t.Fatal("Codex subscription adapter should resolve from the adapter registry")
	}
}

func TestProviderRuntimeConfiguratorsAreAdapterDriven(t *testing.T) {
	adapter := &configurableProviderAdapterForTest{}
	registry := NewAdapterRegistryWithPlugins(pluginmeta.NewRegistry())
	if err := registry.RegisterPlugin(pluginmeta.Descriptor{
		ID:      "tokenhub.provider.configurable",
		Name:    "Configurable Provider",
		Version: "test",
		Source:  pluginmeta.SourceBuiltIn,
		Kinds:   []pluginmeta.Kind{pluginmeta.KindProvider},
		Capabilities: []pluginmeta.CapabilityDescriptor{{
			Kind:    "provider_resource_type",
			Name:    "configurable_account",
			Subject: "configurable",
		}},
	}, AdapterRegistration{
		Type:         "configurable",
		Adapter:      adapter,
		Capabilities: []AdapterCapability{AdapterCapabilityModels, AdapterCapabilityImageGenerate},
	}); err != nil {
		t.Fatalf("register configurable adapter: %v", err)
	}

	configureProviderResourceModelSupport(registry.adapters, registry)
	if adapter.supportsResourceModel == nil {
		t.Fatal("resource model support resolver was not configured")
	}
	if !adapter.supportsResourceModel("configurable", "configurable_account") {
		t.Fatal("resource model support resolver should use adapter registry resource metadata")
	}
	if adapter.supportsResourceModel("configurable", "other_account") {
		t.Fatal("resource model support resolver should reject undeclared resource metadata")
	}

	profile := providerImageCapabilityRouteProfile{ProviderType: "configurable", PublicModel: "configurable-image"}
	configureProviderImageCapabilityProfiles(registry.adapters, func(string) []providerImageCapabilityRouteProfile {
		return []providerImageCapabilityRouteProfile{profile}
	})
	if adapter.imageProfiles == nil {
		t.Fatal("image capability profile resolver was not configured")
	}
	profiles := adapter.imageProfiles("configurable")
	if len(profiles) != 1 || profiles[0].PublicModel != "configurable-image" {
		t.Fatalf("image capability profiles = %+v, want configurable profile", profiles)
	}
}
