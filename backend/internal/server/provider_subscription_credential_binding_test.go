package server

import (
	"context"
	"net/http"
	"testing"
)

func TestSubscriptionCredentialLoadRejectsReboundProvider(t *testing.T) {
	store := NewMemoryStore()
	server := New(store)
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	definitions := builtinSubscriptionDefinitions()
	definitions = append(definitions, builtinSubscriptionDefinition{providerType: providerClaudeSubscription, resourceType: providerResourceClaudeSubscription}, builtinSubscriptionDefinition{providerType: ProviderOpenAICodex, resourceType: ProviderResourceOpenAISubscription})
	for _, definition := range subscriptionWorkerDefinitions() {
		definitions = append(definitions, builtinSubscriptionDefinition{providerType: definition.providerType(), resourceType: definition.resourceType()})
	}
	for _, definition := range definitions {
		t.Run(definition.providerType, func(t *testing.T) {
			source := store.AddProvider(Provider{Name: "Source " + definition.providerType, Type: definition.providerType, Status: StatusActive})
			target := store.AddProvider(Provider{Name: "Target " + definition.providerType, Type: "cursor_subscription", Status: StatusActive})
			targetResourceType := "cursor_account"
			if source.Type == target.Type {
				target = store.AddProvider(Provider{Name: "Alternate " + definition.providerType, Type: "qoder_subscription", Status: StatusActive})
				targetResourceType = "qoder_account"
			}
			resource, err := store.AddProviderResource(ProviderResource{ProviderID: source.ID, Name: definition.providerType + " account", ResourceType: definition.resourceType, Credentials: &ProviderResourceCredentials{AccessToken: "source-secret", SessionJSON: `{"type":"oauth","access":"source-secret"}`}})
			if err != nil {
				t.Fatal(err)
			}
			runtime, err := server.adapterRegistry.Resolve(source.Type)
			if err != nil {
				t.Fatal(err)
			}
			var refresh func(context.Context, string, bool) (ProviderResourceCredentials, error)
			switch adapter := runtime.(type) {
			case *ChatGPTSubscriptionAdapter:
				refresh = adapter.RefreshCredentials
			case *CopilotSubscriptionAdapter:
				refresh = adapter.RefreshCredentials
			case *GoogleSubscriptionAdapter:
				refresh = adapter.RefreshCredentials
			case *ClaudeSubscriptionAdapter:
				refresh = adapter.RefreshCredentials
			case *CodexSubscriptionAdapter:
				refresh = adapter.RefreshCredentials
			case *SubscriptionWorkerAdapter:
				refresh = adapter.RefreshCredentials
			default:
				t.Fatalf("unexpected adapter type %T", runtime)
			}
			if credentials, err := refresh(context.Background(), resource.ID, false); err != nil || credentials.AccessToken != "source-secret" {
				t.Fatalf("original account was not readable: %v", err)
			}
			_, err = store.UpdateProviderResource(resource.ID, ProviderResource{ProviderID: target.ID, ResourceType: targetResourceType, Credentials: &ProviderResourceCredentials{Replace: true, SessionJSON: `{"type":"oauth","access":"other-service-secret"}`}})
			if err != nil {
				t.Fatal(err)
			}
			for _, force := range []bool{false, true} {
				credentials, err := refresh(context.Background(), resource.ID, force)
				if err == nil || AsHTTPError(err).Status != http.StatusConflict {
					t.Errorf("stale adapter credential load (force=%t) = %v, want 409", force, err)
				}
				if credentials.AccessToken != "" || credentials.SessionJSON != "" {
					t.Error("stale adapter received another provider's credentials")
				}
			}
		})
	}
}

func TestSubscriptionRoutePreparationRejectsReboundAccount(t *testing.T) {
	store := NewMemoryStore()
	server := New(store)
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	source := store.AddProvider(Provider{Name: "Source ChatGPT", Type: ProviderChatGPTSubscription, Status: StatusActive, Options: map[string]string{providerCredentialRefreshProfileOption: chatGPTSubscriptionRefreshProfile}})
	target := store.AddProvider(Provider{Name: "Target Cursor", Type: "cursor_subscription", Status: StatusActive})
	resource, err := store.AddProviderResource(ProviderResource{ProviderID: source.ID, Name: "Selected account", ResourceType: ProviderResourceChatGPTSubscription, Credentials: &ProviderResourceCredentials{AccessToken: "source-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.UpdateProviderResource(resource.ID, ProviderResource{ProviderID: target.ID, ResourceType: "cursor_account", Credentials: &ProviderResourceCredentials{Replace: true, SessionJSON: `{"type":"oauth","access":"other-service-secret"}`}})
	if err != nil {
		t.Fatal(err)
	}
	route, err := server.prepareRouteForUpstream(context.Background(), RouteSelection{Provider: source, Resource: &resource})
	if err == nil || AsHTTPError(err).Status != http.StatusConflict {
		t.Fatalf("stale route preparation returned %v, want 409", err)
	}
	if route.Provider.APIKey != "" {
		t.Fatal("stale route preparation loaded another provider's credentials")
	}
}
