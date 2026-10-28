package server

import (
	"context"
	"net/http"
	"reflect"
	"testing"
)

func TestSubscriptionCredentialRefreshRejectsBindingChangedDuringRefresh(t *testing.T) {
	for _, change := range []string{"resource_move", "provider_type"} {
		for _, outcome := range []string{"success", "reauthorization"} {
			t.Run(change+"/"+outcome, func(t *testing.T) {
				store := NewMemoryStore()
				store.ConfigureProviderResourceTypePolicy(map[string][]string{
					ProviderChatGPTSubscription:       {ProviderResourceChatGPTSubscription},
					ProviderGitHubCopilotSubscription: {ProviderResourceGitHubCopilotSubscription},
				})
				source := store.AddProvider(Provider{Name: "Source account", Type: ProviderChatGPTSubscription, Options: map[string]string{providerCredentialRefreshProfileOption: "synthetic-refresh"}})
				target := store.AddProvider(Provider{Name: "Target account", Type: ProviderGitHubCopilotSubscription})
				resource, err := store.AddProviderResource(ProviderResource{ProviderID: source.ID, Name: "Refresh account", ResourceType: ProviderResourceChatGPTSubscription,
					Credentials: &ProviderResourceCredentials{AccessToken: "source-secret", RefreshToken: "source-refresh"}})
				if err != nil {
					t.Fatal(err)
				}
				var changed ProviderResource
				store.ConfigureProviderCredentialRefreshHandlers([]providerResourceCredentialRefreshRegistration{{ProviderType: source.Type, Profile: "synthetic-refresh",
					Refresh: func(_ context.Context, credentials ProviderResourceCredentials) (ProviderResourceCredentials, error) {
						if change == "resource_move" {
							_, err = store.UpdateProviderResource(resource.ID, ProviderResource{ProviderID: target.ID, ResourceType: ProviderResourceGitHubCopilotSubscription,
								Credentials: &ProviderResourceCredentials{Replace: true, AccessToken: "target-secret"}})
						} else {
							_, err = store.UpdateProvider(source.ID, Provider{Type: target.Type})
							if err == nil && outcome == "success" {
								_, err = store.UpdateProviderResource(resource.ID, ProviderResource{APIKey: "target-secret"})
							}
						}
						if err != nil {
							return ProviderResourceCredentials{}, err
						}
						if err := store.db.First(&changed, "id = ?", resource.ID).Error; err != nil {
							return ProviderResourceCredentials{}, err
						}
						if outcome == "reauthorization" {
							return ProviderResourceCredentials{}, NewHTTPError(http.StatusConflict, "provider_resource_reauthorization_required", "Synthetic refresh token was rejected")
						}
						credentials.AccessToken = "rotated-source-secret"
						return credentials, nil
					}}})
				credentials, err := providerCredentialLoaderForType(store, source.Type)(context.Background(), resource.ID, true)
				if err == nil || AsHTTPError(err).Status != http.StatusConflict || AsHTTPError(err).Code != "provider_resource_changed" {
					t.Errorf("refresh accepted a changed binding: %v", err)
				}
				if credentials != (ProviderResourceCredentials{}) {
					t.Error("refresh returned credentials after the binding changed")
				}
				var current ProviderResource
				if err := store.db.First(&current, "id = ?", resource.ID).Error; err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(current, changed) {
					t.Error("stale refresh mutated the rebound account")
				}
			})
		}
	}
}
