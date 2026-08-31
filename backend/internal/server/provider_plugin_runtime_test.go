package server

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	pluginmeta "tokenhub/backend/internal/plugin"
)

type configurableProviderAdapterForTest struct {
	supportsResourceModel func(providerType string, resourceType string) bool
	imageProfiles         func(providerType string) []providerImageCapabilityRouteProfile
}

type credentialRefreshAdapterForTest struct {
	registrations []providerResourceCredentialRefreshRegistration
}

type credentialIdentityAdapterForTest struct {
	registrations []providerResourceCredentialIdentityRegistration
}

func (a credentialRefreshAdapterForTest) ProviderResourceCredentialRefreshHandlers() []providerResourceCredentialRefreshRegistration {
	return a.registrations
}

func (a credentialIdentityAdapterForTest) ProviderResourceCredentialIdentityProfiles() []providerResourceCredentialIdentityRegistration {
	return a.registrations
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
	if codexSubscription.CredentialRefreshClient != client {
		t.Fatal("Codex subscription credential refresh client should use the shared upstream client")
	}
	if codexSubscription.RefreshCredentials == nil {
		t.Fatal("Codex subscription credential refresh callback was not configured")
	}
	registry := NewAdapterRegistryWithPlugins(pluginmeta.NewRegistry())
	registerBuiltinProviderAdapters(registry, runtime.adapters)
	if err := configureProviderCredentialIdentityProfileHandlers(store, registry); err != nil {
		t.Fatalf("configure credential identity profile handlers: %v", err)
	}
	if err := configureProviderCredentialRefreshHandlers(store, registry); err != nil {
		t.Fatalf("configure credential refresh handlers: %v", err)
	}
	store.ConfigureProviderResourceCredentialIdentityProfiles(providerResourceCredentialIdentityProfilesFromRegistry(registry))
	if _, ok := store.providerCredentialIdentityRegistration(ProviderOpenAICodex, ProviderResourceOpenAISubscription); !ok {
		t.Fatal("Codex subscription identity profile handler was not configured")
	}
	if _, ok := store.providerCredentialRefreshRegistration(Provider{
		Type: ProviderOpenAICodex,
		Options: map[string]string{
			providerCredentialRefreshProfileOption: openAIAccountOAuthRefreshProfile,
		},
	}); !ok {
		t.Fatal("Codex subscription native credential refresh handler was not configured")
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

func TestProviderCredentialRefreshRegistrationsAreProviderScoped(t *testing.T) {
	registry := NewAdapterRegistryWithPlugins(pluginmeta.NewRegistry())
	for _, providerType := range []string{"refresh_alpha", "refresh_beta"} {
		adapter := credentialRefreshAdapterForTest{registrations: []providerResourceCredentialRefreshRegistration{{
			Profile: "shared_oauth_profile",
			Refresh: func(context.Context, ProviderResourceCredentials) (ProviderResourceCredentials, error) {
				return ProviderResourceCredentials{}, nil
			},
		}}}
		if err := registry.RegisterPlugin(pluginmeta.Descriptor{
			ID:      "tokenhub.provider." + providerType,
			Name:    providerType,
			Version: "test",
			Source:  pluginmeta.SourceBuiltIn,
			Kinds:   []pluginmeta.Kind{pluginmeta.KindProvider},
			Capabilities: []pluginmeta.CapabilityDescriptor{{
				Kind:    pluginmeta.CapabilityKindProviderType,
				Name:    providerType,
				Subject: providerType,
			}, {
				Kind:    pluginmeta.CapabilityKindProviderPolicy,
				Name:    providerCredentialRefreshProfileOption,
				Subject: providerType,
				Value:   "shared_oauth_profile",
			}},
		}, AdapterRegistration{Type: providerType, Adapter: adapter}); err != nil {
			t.Fatalf("register %s adapter: %v", providerType, err)
		}
	}

	registrations, err := registry.ProviderCredentialRefreshRegistrations()
	if err != nil {
		t.Fatalf("credential refresh registrations: %v", err)
	}
	if len(registrations) != 2 {
		t.Fatalf("registrations = %+v, want two provider-scoped registrations", registrations)
	}
	for _, registration := range registrations {
		if registration.Profile != "shared_oauth_profile" || registration.ProviderType == "" || registration.Refresh == nil {
			t.Fatalf("registration was not normalized: %+v", registration)
		}
	}
}

func TestProviderCredentialRefreshRegistrationsRejectDuplicateProviderProfile(t *testing.T) {
	registry := NewAdapterRegistryWithPlugins(pluginmeta.NewRegistry())
	adapter := credentialRefreshAdapterForTest{registrations: []providerResourceCredentialRefreshRegistration{
		{Profile: "duplicate_oauth_profile", Refresh: func(context.Context, ProviderResourceCredentials) (ProviderResourceCredentials, error) {
			return ProviderResourceCredentials{}, nil
		}},
		{Profile: "duplicate_oauth_profile", Refresh: func(context.Context, ProviderResourceCredentials) (ProviderResourceCredentials, error) {
			return ProviderResourceCredentials{}, nil
		}},
	}}
	if err := registry.RegisterPlugin(pluginmeta.Descriptor{
		ID:      "tokenhub.provider.refresh-duplicate",
		Name:    "Refresh Duplicate",
		Version: "test",
		Source:  pluginmeta.SourceBuiltIn,
		Kinds:   []pluginmeta.Kind{pluginmeta.KindProvider},
		Capabilities: []pluginmeta.CapabilityDescriptor{{
			Kind:    pluginmeta.CapabilityKindProviderType,
			Name:    "refresh_duplicate",
			Subject: "refresh_duplicate",
		}, {
			Kind:    pluginmeta.CapabilityKindProviderPolicy,
			Name:    providerCredentialRefreshProfileOption,
			Subject: "refresh_duplicate",
			Value:   "duplicate_oauth_profile",
		}},
	}, AdapterRegistration{Type: "refresh_duplicate", Adapter: adapter}); err != nil {
		t.Fatalf("register duplicate adapter: %v", err)
	}

	_, err := registry.ProviderCredentialRefreshRegistrations()
	if err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("duplicate provider refresh profile error = %v, want already registered", err)
	}
}

func TestProviderCredentialIdentityProfileRegistrationsAreProviderScoped(t *testing.T) {
	registry := NewAdapterRegistryWithPlugins(pluginmeta.NewRegistry())
	for _, providerType := range []string{"identity_alpha", "identity_beta"} {
		adapter := credentialIdentityAdapterForTest{registrations: []providerResourceCredentialIdentityRegistration{{
			Profile: "shared_id_token_profile",
			Resolve: func(credentials ProviderResourceCredentials) ProviderResourceCredentials {
				return credentials
			},
		}}}
		if err := registry.RegisterPlugin(pluginmeta.BuiltInProviderWithResourceTypeMetadata(
			"tokenhub.provider."+providerType,
			providerType,
			[]string{providerType},
			[]pluginmeta.ManifestProviderResourceType{{
				Type:                      providerType + "_account",
				CredentialIdentityProfile: "shared_id_token_profile",
			}},
			nil,
		), AdapterRegistration{Type: providerType, Adapter: adapter}); err != nil {
			t.Fatalf("register %s adapter: %v", providerType, err)
		}
	}

	registrations, err := registry.ProviderCredentialIdentityProfileRegistrations()
	if err != nil {
		t.Fatalf("credential identity registrations: %v", err)
	}
	if len(registrations) != 2 {
		t.Fatalf("registrations = %+v, want two provider-scoped identity registrations", registrations)
	}
	for _, registration := range registrations {
		if registration.Profile != "shared_id_token_profile" || registration.ProviderType == "" || registration.Resolve == nil {
			t.Fatalf("registration was not normalized: %+v", registration)
		}
	}
}

func TestProviderCredentialIdentityProfileRegistrationsRejectDuplicateProviderProfile(t *testing.T) {
	registry := NewAdapterRegistryWithPlugins(pluginmeta.NewRegistry())
	adapter := credentialIdentityAdapterForTest{registrations: []providerResourceCredentialIdentityRegistration{
		{Profile: "duplicate_id_token_profile", Resolve: func(credentials ProviderResourceCredentials) ProviderResourceCredentials {
			return credentials
		}},
		{Profile: "duplicate_id_token_profile", Resolve: func(credentials ProviderResourceCredentials) ProviderResourceCredentials {
			return credentials
		}},
	}}
	if err := registry.RegisterPlugin(pluginmeta.BuiltInProviderWithResourceTypeMetadata(
		"tokenhub.provider.identity-duplicate",
		"Identity Duplicate",
		[]string{"identity_duplicate"},
		[]pluginmeta.ManifestProviderResourceType{{
			Type:                      "identity_duplicate_account",
			CredentialIdentityProfile: "duplicate_id_token_profile",
		}},
		nil,
	), AdapterRegistration{Type: "identity_duplicate", Adapter: adapter}); err != nil {
		t.Fatalf("register duplicate identity adapter: %v", err)
	}

	_, err := registry.ProviderCredentialIdentityProfileRegistrations()
	if err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("duplicate provider identity profile error = %v, want already registered", err)
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
