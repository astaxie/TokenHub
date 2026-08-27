package server

import (
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestProviderResourceUsesPluginResourceTypeDefaultBaseURL(t *testing.T) {
	store := NewMemoryStore()
	store.ConfigureProviderResourceTypeDefaults(map[string]map[string]string{
		"kimi_subscription_account": {
			"auth_type":       "personal_access_token",
			"base_url":        "https://kimi.example/v1",
			"max_concurrency": "7",
		},
	})
	provider := store.AddProvider(Provider{
		ID: "prv_kimi_subscription", Name: "Kimi Subscription", Type: "kimi_subscription",
		Status: StatusActive, Healthy: true,
	})

	created, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_kimi_subscription", ProviderID: provider.ID, Name: "Kimi Account",
		ResourceType: "kimi_subscription_account",
	})
	if err != nil {
		t.Fatalf("create provider resource: %v", err)
	}
	if created.BaseURL != "https://kimi.example/v1" {
		t.Fatalf("created resource base URL = %q, want plugin default", created.BaseURL)
	}
	if created.MaxConcurrency != 7 {
		t.Fatalf("created resource max concurrency = %d, want plugin default", created.MaxConcurrency)
	}
	if created.CredentialSummary["auth_type"] != "personal_access_token" {
		t.Fatalf("created credential summary = %+v, want plugin auth type default", created.CredentialSummary)
	}
	if creds := store.providerResourceCredentialsForRuntime(created); creds.AuthType != "personal_access_token" {
		t.Fatalf("runtime auth type = %q, want plugin default", creds.AuthType)
	}

	updated, err := store.UpdateProviderResource(created.ID, ProviderResource{
		ResourceType: "kimi_subscription_account",
		BaseURL:      "",
	})
	if err != nil {
		t.Fatalf("update provider resource: %v", err)
	}
	if updated.BaseURL != "https://kimi.example/v1" {
		t.Fatalf("updated resource base URL = %q, want plugin default", updated.BaseURL)
	}
}

func TestProviderResourceAccountClassificationUsesPluginMetadata(t *testing.T) {
	store := NewMemoryStore()
	store.ConfigureProviderResourceTypePolicy(map[string][]string{
		"kimi_subscription": {"kimi_subscription_account"},
	})
	provider := store.AddProvider(Provider{
		ID: "prv_kimi_metadata", Name: "Kimi Metadata", Type: "kimi_subscription",
		Status: StatusActive, Healthy: true,
	})

	created, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_kimi_metadata", ProviderID: provider.ID, Name: "Kimi Account",
		ResourceType: "kimi_subscription_account", APIKey: "kimi-access-token",
	})
	if err != nil {
		t.Fatalf("create declared account resource: %v", err)
	}
	if created.CredentialSummary["credential_source"] != "kimi_subscription_account" {
		t.Fatalf("declared resource credential summary = %+v, want plugin account metadata", created.CredentialSummary)
	}

	opaque, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_kimi_opaque", ProviderID: provider.ID, Name: "Kimi Opaque Token",
		ResourceType: "kimi_ephemeral_token", APIKey: "opaque-secret",
		Options: map[string]string{"account_email": "should-not-be-a-summary@example.com", "has_refresh_token": "true"},
	})
	if err != nil {
		t.Fatalf("create undeclared plugin resource: %v", err)
	}
	if opaque.CredentialSummary != nil {
		t.Fatalf("undeclared plugin resource credential summary = %+v, want nil", opaque.CredentialSummary)
	}
	stored, ok := store.GetProviderResource(opaque.ID)
	if !ok {
		t.Fatal("expected undeclared plugin resource to be stored")
	}
	if stored.CredentialSummary != nil {
		t.Fatalf("stored undeclared plugin resource credential summary = %+v, want nil", stored.CredentialSummary)
	}
}

func TestProviderResourceAccountClassificationKeepsLegacyFallbackWithoutMetadata(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{
		ID: "prv_legacy_account", Name: "Legacy Account Provider", Type: "legacy_provider",
		Status: StatusActive, Healthy: true,
	})

	created, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_legacy_account", ProviderID: provider.ID, Name: "Legacy Account",
		ResourceType: "legacy_account", APIKey: "legacy-access-token",
	})
	if err != nil {
		t.Fatalf("create legacy account resource: %v", err)
	}
	if created.CredentialSummary["credential_source"] != "legacy_account" {
		t.Fatalf("legacy resource credential summary = %+v, want legacy account fallback", created.CredentialSummary)
	}
}

func TestProviderResourceTypePolicyFromRegistry(t *testing.T) {
	registry := NewAdapterRegistryWithPlugins(pluginmeta.NewRegistry())
	if err := registry.RegisterPlugin(pluginmeta.BuiltInProviderWithResourceTypeMetadata(
		"tokenhub.provider.kimi",
		"Kimi",
		[]string{"kimi_subscription"},
		[]pluginmeta.ManifestProviderResourceType{{
			Type:        "kimi_subscription_account",
			DisplayName: "Kimi Subscription Account",
		}},
		nil,
	), AdapterRegistration{Type: "kimi_subscription", Adapter: MockAdapter{}}); err != nil {
		t.Fatalf("register plugin: %v", err)
	}
	if err := registry.RegisterPlugin(pluginmeta.BuiltInProvider(
		"tokenhub.provider.legacy",
		"Legacy",
		[]string{"legacy_provider"},
		nil,
	), AdapterRegistration{Type: "legacy_provider", Adapter: MockAdapter{}}); err != nil {
		t.Fatalf("register legacy plugin: %v", err)
	}

	policy := providerResourceTypePolicyFromRegistry(registry)
	if got := policy["kimi_subscription"]; len(got) != 1 || got[0] != "kimi_subscription_account" {
		t.Fatalf("registry resource policy = %+v, want Kimi account resource type", policy)
	}
	if _, ok := policy["legacy_provider"]; ok {
		t.Fatalf("registry resource policy = %+v, want no entry for providers without resource metadata", policy)
	}
}
