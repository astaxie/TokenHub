package server

import (
	"context"
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

func TestProviderResourceTypeDefaultsAreScopedByProviderType(t *testing.T) {
	registry := NewAdapterRegistryWithPlugins(pluginmeta.NewRegistry())
	for _, tc := range []struct {
		pluginID     string
		providerType string
		authType     string
		baseURL      string
	}{
		{"tokenhub.provider.alpha", "alpha_subscription", "alpha_oauth", "https://alpha.example/v1"},
		{"tokenhub.provider.beta", "beta_subscription", "beta_oauth", "https://beta.example/v1"},
	} {
		if err := registry.RegisterPlugin(pluginmeta.BuiltInProviderWithResourceTypeMetadata(
			tc.pluginID,
			tc.providerType,
			[]string{tc.providerType},
			[]pluginmeta.ManifestProviderResourceType{{
				Type: "oauth_account",
				Defaults: map[string]string{
					"auth_type": tc.authType,
					"base_url":  tc.baseURL,
				},
			}},
			nil,
		), AdapterRegistration{Type: tc.providerType, Adapter: MockAdapter{}}); err != nil {
			t.Fatalf("register %s plugin: %v", tc.providerType, err)
		}
	}
	store := NewMemoryStore()
	configureProviderResourceTypeDefaults(store, registry)
	alphaProvider := store.AddProvider(Provider{
		ID: "prv_alpha", Name: "Alpha", Type: "alpha_subscription",
		Status: StatusActive, Healthy: true,
	})
	betaProvider := store.AddProvider(Provider{
		ID: "prv_beta", Name: "Beta", Type: "beta_subscription",
		Status: StatusActive, Healthy: true,
	})

	alpha, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_alpha", ProviderID: alphaProvider.ID, Name: "Alpha Account",
		ResourceType: "oauth_account", APIKey: "alpha-access",
	})
	if err != nil {
		t.Fatalf("create alpha resource: %v", err)
	}
	beta, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_beta", ProviderID: betaProvider.ID, Name: "Beta Account",
		ResourceType: "oauth_account", APIKey: "beta-access",
	})
	if err != nil {
		t.Fatalf("create beta resource: %v", err)
	}

	if alpha.BaseURL != "https://alpha.example/v1" || alpha.CredentialSummary["auth_type"] != "alpha_oauth" {
		t.Fatalf("alpha resource = %+v, want alpha-scoped defaults", alpha)
	}
	if beta.BaseURL != "https://beta.example/v1" || beta.CredentialSummary["auth_type"] != "beta_oauth" {
		t.Fatalf("beta resource = %+v, want beta-scoped defaults", beta)
	}
}

func TestProviderResourceAccountClassificationUsesPluginMetadata(t *testing.T) {
	store := NewMemoryStore()
	store.ConfigureProviderResourceTypePolicy(map[string][]string{
		"kimi_subscription": {"kimi_subscription_account"},
		"plain_plugin":      {},
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
	if store.IsProviderAccountResourceType("plain_plugin", "plain_account") {
		t.Fatal("provider with explicit empty plugin resource policy treated an undeclared type as an account")
	}
}

func TestProviderResourceCredentialInputOptionalUsesPluginMetadata(t *testing.T) {
	store := NewMemoryStore()
	store.ConfigureProviderResourceTypePolicy(map[string][]string{
		"optional_provider": {"optional_account"},
		"required_provider": {"required_account"},
	})
	store.ConfigureProviderResourceCredentialInputOptional(map[string]bool{
		"optional_account": true,
	})

	optionalProvider := store.AddProvider(Provider{
		ID: "prv_optional_account", Name: "Optional Account Provider", Type: "optional_provider",
		Status: StatusActive, Healthy: true,
	})
	optional, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_optional_account", ProviderID: optionalProvider.ID, Name: "Optional Account",
		ResourceType: "optional_account",
	})
	if err != nil {
		t.Fatalf("create optional account resource: %v", err)
	}
	if optional.CredentialSummary["credential_source"] != "optional_account" {
		t.Fatalf("optional account credential summary = %+v", optional.CredentialSummary)
	}

	requiredProvider := store.AddProvider(Provider{
		ID: "prv_required_account", Name: "Required Account Provider", Type: "required_provider",
		Status: StatusActive, Healthy: true,
	})
	required, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_required_account", ProviderID: requiredProvider.ID, Name: "Required Account",
		ResourceType: "required_account",
	})
	if err != nil {
		t.Fatalf("create required account resource: %v", err)
	}
	if required.CredentialSummary != nil {
		t.Fatalf("required account without credentials summary = %+v, want nil", required.CredentialSummary)
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

func TestProviderAccountOptionsFallbackIsProviderNeutral(t *testing.T) {
	options := map[string]string{}
	applyProviderAccountOptions(options, "", ProviderResourceCredentials{AuthType: "oauth"})
	if options["credential_source"] != providerAccountCredentialSourceFallback {
		t.Fatalf("credential source = %q, want provider-neutral fallback", options["credential_source"])
	}

	openAIOptions := map[string]string{}
	applyOpenAIAccountOptions(openAIOptions, ProviderResourceCredentials{AuthType: "oauth"})
	if openAIOptions["credential_source"] != ProviderResourceOpenAISubscription {
		t.Fatalf("OpenAI credential source = %q, want explicit OpenAI subscription", openAIOptions["credential_source"])
	}
}

func TestProviderResourceCredentialIdentityProfileControlsIDTokenClaims(t *testing.T) {
	store := NewMemoryStore()
	store.ConfigureProviderResourceTypePolicy(map[string][]string{
		"profiled_provider": {"profiled_account"},
	})
	store.ConfigureProviderResourceCredentialIdentityProfiles(map[string]string{
		"profiled_account": providerResourceIdentityProfileOpenAIIDToken,
	})
	provider := store.AddProvider(Provider{
		ID: "prv_profiled_identity", Name: "Profiled Identity", Type: "profiled_provider",
		Status: StatusActive, Healthy: true,
	})

	created, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_profiled_identity", ProviderID: provider.ID, Name: "Profiled Account",
		ResourceType: "profiled_account",
		Credentials: &ProviderResourceCredentials{IDToken: testJWT(map[string]any{
			"email": "profiled@example.com",
			"https://api.openai.com/auth": map[string]any{
				"chatgpt_account_id": "account-profiled",
				"user_id":            "user-profiled",
				"chatgpt_plan_type":  "team",
				"organizations":      []map[string]any{{"id": "org-profiled", "is_default": true}},
			},
		})},
	})
	if err != nil {
		t.Fatalf("create profiled resource: %v", err)
	}
	if created.CredentialSummary["account_email"] != "profiled@example.com" ||
		created.CredentialSummary["account_id"] != "account-profiled" ||
		created.CredentialSummary["user_id"] != "user-profiled" ||
		created.CredentialSummary["organization_id"] != "org-profiled" ||
		created.CredentialSummary["plan_type"] != "team" {
		t.Fatalf("profiled credential summary = %+v", created.CredentialSummary)
	}

	legacyProvider := store.AddProvider(Provider{
		ID: "prv_legacy_identity", Name: "Legacy Identity", Type: ProviderOpenAICodex,
		Status: StatusActive, Healthy: true,
	})
	legacy, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_legacy_identity", ProviderID: legacyProvider.ID, Name: "Legacy Account",
		ResourceType: ProviderResourceOpenAISubscription,
		Credentials:  &ProviderResourceCredentials{IDToken: testJWT(map[string]any{"email": "legacy@example.com"})},
	})
	if err != nil {
		t.Fatalf("create legacy resource: %v", err)
	}
	if legacy.CredentialSummary["account_email"] != "" {
		t.Fatalf("legacy resource without identity profile decoded claims: %+v", legacy.CredentialSummary)
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
	if got, ok := policy["legacy_provider"]; !ok || len(got) != 0 {
		t.Fatalf("registry resource policy = %+v, want empty policy for providers without resource metadata", policy)
	}
}

func TestProviderResourceCredentialIdentityProfilesFromRegistry(t *testing.T) {
	registry := NewAdapterRegistryWithPlugins(pluginmeta.NewRegistry())
	if err := registry.RegisterPlugin(pluginmeta.BuiltInProviderWithResourceTypeMetadata(
		"tokenhub.provider.profiled",
		"Profiled Provider",
		[]string{"profiled_provider"},
		[]pluginmeta.ManifestProviderResourceType{{
			Type:                      "profiled_account",
			CredentialIdentityProfile: providerResourceIdentityProfileOpenAIIDToken,
		}},
		nil,
	), AdapterRegistration{Type: "profiled_provider", Adapter: MockAdapter{}}); err != nil {
		t.Fatalf("register plugin: %v", err)
	}

	profiles := providerResourceCredentialIdentityProfilesFromRegistry(registry)
	if profiles[providerResourceScopedKey("profiled_provider", "profiled_account")] != providerResourceIdentityProfileOpenAIIDToken {
		t.Fatalf("registry credential identity profiles = %+v", profiles)
	}
}

func TestProviderResourceCredentialInputOptionalFromRegistry(t *testing.T) {
	registry := NewAdapterRegistryWithPlugins(pluginmeta.NewRegistry())
	if err := registry.RegisterPlugin(pluginmeta.BuiltInProviderWithResourceTypeMetadata(
		"tokenhub.provider.optional",
		"Optional Provider",
		[]string{"optional_provider"},
		[]pluginmeta.ManifestProviderResourceType{{
			Type:                    "optional_account",
			CredentialInputOptional: true,
		}},
		nil,
	), AdapterRegistration{Type: "optional_provider", Adapter: MockAdapter{}}); err != nil {
		t.Fatalf("register plugin: %v", err)
	}

	resourceTypes := providerResourceCredentialInputOptionalFromRegistry(registry)
	if !resourceTypes[providerResourceScopedKey("optional_provider", "optional_account")] {
		t.Fatalf("registry optional credential input resource types = %+v", resourceTypes)
	}
}

func TestProviderResourceCachedCatalogSourceUsesPluginCatalogSource(t *testing.T) {
	catalog, ok := providerResourceCachedCatalog(
		Provider{ID: "prv_vendor", Name: "Vendor", Type: "vendor_subscription"},
		&ProviderResource{
			ID:           "rsrc_vendor",
			ProviderID:   "prv_vendor",
			Name:         "Vendor Account",
			ResourceType: "vendor_account",
			Options: map[string]string{
				providerResourceModelCatalogOption: `[{"id":"vendor-model","name":"Vendor Model"}]`,
			},
		},
		ProviderCatalogEntry{
			ID:          "vendor-catalog",
			Name:        "Vendor Catalog",
			DisplayName: "Vendor Catalog",
			Type:        "vendor_subscription",
			Source:      "vendor-live",
		},
	)
	if !ok {
		t.Fatal("expected cached provider resource catalog")
	}
	if catalog.Source != "vendor-cache" || catalog.ID != "vendor-catalog" || catalog.Type != "vendor_subscription" {
		t.Fatalf("cached provider resource catalog = %+v", catalog)
	}
}

func TestPrepareRouteForUpstreamUsesRefreshProfilePolicy(t *testing.T) {
	store := NewMemoryStore()
	refreshProfile := "profiled_route_oauth"
	store.ConfigureProviderResourceTypePolicy(map[string][]string{
		"profiled_provider": {"profiled_account"},
	})
	store.ConfigureProviderCredentialRefreshHandlers([]providerResourceCredentialRefreshRegistration{{
		ProviderType: "profiled_provider",
		Profile:      refreshProfile,
		Refresh: func(context.Context, ProviderResourceCredentials) (ProviderResourceCredentials, error) {
			return ProviderResourceCredentials{}, nil
		},
	}})
	provider := store.AddProvider(Provider{
		ID: "prv_profiled_route", Name: "Profiled Route", Type: "profiled_provider",
		Status: StatusActive, Healthy: true,
		Options: map[string]string{
			providerCredentialRefreshProfileOption: refreshProfile,
		},
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_profiled_route", ProviderID: provider.ID, Name: "Profiled Account",
		ResourceType: "profiled_account", Status: StatusActive, Healthy: true,
		Credentials: &ProviderResourceCredentials{
			AuthType: "oauth", AccessToken: "profiled-route-access", RefreshToken: "profiled-route-refresh",
		},
	})
	if err != nil {
		t.Fatalf("create profiled route resource: %v", err)
	}

	prepared, err := (&Server{store: store}).prepareRouteForUpstream(context.Background(), RouteSelection{
		Provider: provider,
		Resource: &resource,
	})
	if err != nil {
		t.Fatalf("prepare route: %v", err)
	}
	if prepared.Provider.APIKey != "profiled-route-access" ||
		prepared.Provider.Options["resource_id"] != resource.ID ||
		prepared.Provider.Options["credential_source"] != "profiled_account" {
		t.Fatalf("prepared provider did not use plugin resource credentials: %+v", prepared.Provider)
	}
}
