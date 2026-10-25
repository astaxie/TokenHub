package server

import (
	"context"
	"testing"
)

func TestSubscriptionRoutesBindAccountsWithoutNativeRefresh(t *testing.T) {
	store := NewMemoryStore()
	server := New(store)
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	types := []struct{ provider, resource string }{
		{ProviderGitHubCopilotSubscription, ProviderResourceGitHubCopilotSubscription},
		{providerClaudeSubscription, providerResourceClaudeSubscription},
	}
	for _, definition := range subscriptionWorkerDefinitions() {
		types = append(types, struct{ provider, resource string }{definition.providerType(), definition.resourceType()})
	}
	for _, kind := range types {
		t.Run(kind.provider, func(t *testing.T) {
			provider := store.AddProvider(Provider{Name: kind.provider, Type: kind.provider, Status: StatusActive, Healthy: true})
			resource, err := store.AddProviderResource(ProviderResource{ProviderID: provider.ID, Name: kind.provider + " account", ResourceType: kind.resource, Status: StatusActive, Healthy: true,
				Credentials: &ProviderResourceCredentials{AccessToken: "synthetic-token", SessionJSON: `{"type":"oauth","access":"synthetic-token"}`}})
			if err != nil {
				t.Fatal(err)
			}
			if store.SupportsNativeProviderResourceCredentialRefresh(resource) {
				t.Fatal("Fixture must exercise an adapter that owns its credential lifecycle")
			}
			store.AddModel(Model{Name: kind.provider, Modality: "chat", Status: StatusActive})
			store.AddRoute(ModelRoute{ModelName: kind.provider, ProviderID: provider.ID, ProviderResourceID: resource.ID, ProviderModel: "model", Status: StatusActive})
			route, err := store.SelectRoute(kind.provider)
			if err != nil {
				t.Fatal(err)
			}
			// Options are configuration, not authority for account selection.
			route.Provider.Options = map[string]string{"resource_id": "untrusted-option", "retained": "value"}
			prepared, err := server.prepareRouteForUpstream(context.Background(), route)
			if err != nil || prepared.Provider.Options["resource_id"] != resource.ID || prepared.Provider.Options["retained"] != "value" {
				t.Fatalf("Selected account was not bound: options=%v err=%v", prepared.Provider.Options, err)
			}
			if route.Provider.Options["resource_id"] != "untrusted-option" {
				t.Fatal("Preparing an account mutated shared route options")
			}
		})
	}
}

func TestPrepareRouteLeavesNonAccountOptionsUnchanged(t *testing.T) {
	store := NewMemoryStore()
	server := New(store)
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	for _, resource := range []*ProviderResource{nil, {ID: "api-key-resource", ResourceType: ProviderResourceAPIKey}} {
		provider := Provider{Type: ProviderOpenAICompatible, APIKey: "synthetic-key", Options: map[string]string{"resource_id": "existing-option"}}
		prepared, err := server.prepareRouteForUpstream(context.Background(), RouteSelection{Provider: provider, Resource: resource})
		if err != nil || prepared.Provider.Options["resource_id"] != "existing-option" || prepared.Provider.APIKey != "synthetic-key" {
			t.Fatalf("Non-account configuration changed: %+v, %v", prepared.Provider, err)
		}
	}
}
