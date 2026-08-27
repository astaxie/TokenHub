package server

import "testing"

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
