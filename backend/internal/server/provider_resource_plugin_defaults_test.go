package server

import "testing"

func TestProviderResourceUsesPluginResourceTypeDefaultBaseURL(t *testing.T) {
	store := NewMemoryStore()
	store.ConfigureProviderResourceTypeDefaults(map[string]map[string]string{
		"kimi_subscription_account": {
			"base_url": "https://kimi.example/v1",
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
