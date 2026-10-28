package server

import (
	"context"
	"net/http"
	"path/filepath"
	"reflect"
	"testing"

	"gorm.io/gorm"
)

func TestNativeCredentialRefreshRejectsChangedAccountSnapshot(t *testing.T) {
	for _, binding := range []string{"adapter", "scheduler"} {
		for _, change := range []string{"move", "recreate", "replace", "provider_type"} {
			for _, outcome := range []string{"success", "reauthorization"} {
				t.Run(binding+"/"+change+"/"+outcome, func(t *testing.T) {
					store := NewMemoryStore()
					store.ConfigureProviderResourceTypePolicy(map[string][]string{
						ProviderChatGPTSubscription: {ProviderResourceChatGPTSubscription},
					})
					provider := store.AddProvider(Provider{Name: "Original provider", Type: ProviderChatGPTSubscription, Options: map[string]string{providerCredentialRefreshProfileOption: "snapshot-refresh"}})
					target := store.AddProvider(Provider{Name: "Target provider", Type: provider.Type})
					original := ProviderResourceCredentials{AccessToken: "original-access", RefreshToken: "original-refresh"}
					resource, err := store.AddProviderResource(ProviderResource{ProviderID: provider.ID, Name: "Original account", ResourceType: ProviderResourceChatGPTSubscription, Credentials: &original})
					if err != nil {
						t.Fatal(err)
					}
					var changed ProviderResource
					store.ConfigureProviderCredentialRefreshHandlers([]providerResourceCredentialRefreshRegistration{{ProviderType: provider.Type, Profile: "snapshot-refresh",
						Refresh: func(_ context.Context, credentials ProviderResourceCredentials) (ProviderResourceCredentials, error) {
							switch change {
							case "move":
								_, err = store.UpdateProviderResource(resource.ID, ProviderResource{ProviderID: target.ID})
							case "recreate":
								err = store.DeleteProviderResource(resource.ID)
								if err == nil {
									_, err = store.AddProviderResource(ProviderResource{ID: resource.ID, ProviderID: provider.ID, Name: "Recreated account", ResourceType: resource.ResourceType, Credentials: &original})
								}
							case "replace":
								replacement := original
								replacement.Replace = true
								_, err = store.UpdateProviderResource(resource.ID, ProviderResource{Credentials: &replacement})
							case "provider_type":
								_, err = store.UpdateProvider(provider.ID, Provider{Type: ProviderGitHubCopilotSubscription})
							}
							if err != nil {
								return ProviderResourceCredentials{}, err
							}
							if err := store.db.First(&changed, "id = ?", resource.ID).Error; err != nil {
								return ProviderResourceCredentials{}, err
							}
							if outcome == "reauthorization" {
								return ProviderResourceCredentials{}, NewHTTPError(http.StatusConflict, "provider_resource_reauthorization_required", "Synthetic refresh was rejected")
							}
							credentials.AccessToken = "stale-refreshed-access"
							return credentials, nil
						}}})
					refresh := store.RefreshProviderResourceCredentials
					if binding == "adapter" {
						refresh = providerCredentialLoaderForType(store, provider.Type)
					}
					credentials, err := refresh(context.Background(), resource.ID, true)
					if err == nil || AsHTTPError(err).Status != http.StatusConflict || AsHTTPError(err).Code != "provider_resource_changed" {
						t.Errorf("stale refresh returned %v, want account conflict", err)
					}
					if credentials != (ProviderResourceCredentials{}) {
						t.Error("stale refresh returned credentials for a changed account")
					}
					var current ProviderResource
					if err := store.db.First(&current, "id = ?", resource.ID).Error; err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(current, changed) {
						t.Error("stale refresh modified the current account")
					}
				})
			}
		}
	}
}

func TestNativeCredentialRefreshGuardsFinalPersistenceIdentity(t *testing.T) {
	for _, outcome := range []string{"success", "reauthorization"} {
		for _, change := range []string{"delete", "recreate"} {
			t.Run(outcome+"/"+change, func(t *testing.T) {
				url := "sqlite://" + filepath.Join(t.TempDir(), "refresh-identity.db")
				store, err := NewSQLiteStore(url)
				if err != nil {
					t.Fatal(err)
				}
				other, err := NewSQLiteStore(url)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					for _, database := range []*GormStore{store, other} {
						if db, err := database.db.DB(); err == nil {
							_ = db.Close()
						}
					}
				})
				store.ConfigureProviderResourceTypePolicy(map[string][]string{ProviderChatGPTSubscription: {ProviderResourceChatGPTSubscription}})
				provider := store.AddProvider(Provider{Name: "Refresh provider", Type: ProviderChatGPTSubscription, Options: map[string]string{providerCredentialRefreshProfileOption: "snapshot-refresh"}})
				original := ProviderResourceCredentials{AccessToken: "original-access", RefreshToken: "original-refresh"}
				resource, err := store.AddProviderResource(ProviderResource{ProviderID: provider.ID, Name: "Refresh account", ResourceType: ProviderResourceChatGPTSubscription, Credentials: &original})
				if err != nil {
					t.Fatal(err)
				}
				var mutationErr error
				var refreshed, mutated bool
				const callback = "test:native-refresh-recreate-after-read"
				if err := store.db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
					if _, ok := tx.Statement.Dest.(*ProviderResource); !ok || !refreshed || mutated {
						return
					}
					mutated = true
					mutationErr = other.DeleteProviderResource(resource.ID)
					if mutationErr == nil && change == "recreate" {
						_, mutationErr = other.AddProviderResource(ProviderResource{ID: resource.ID, ProviderID: provider.ID, Name: "Replacement account", ResourceType: resource.ResourceType, Credentials: &ProviderResourceCredentials{AccessToken: "replacement-access"}})
					}
				}); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = store.db.Callback().Query().Remove(callback) })
				store.ConfigureProviderCredentialRefreshHandlers([]providerResourceCredentialRefreshRegistration{{ProviderType: provider.Type, Profile: "snapshot-refresh",
					Refresh: func(_ context.Context, credentials ProviderResourceCredentials) (ProviderResourceCredentials, error) {
						refreshed = true
						if outcome == "reauthorization" {
							return ProviderResourceCredentials{}, NewHTTPError(http.StatusConflict, "provider_resource_reauthorization_required", "Synthetic refresh was rejected")
						}
						credentials.AccessToken = "stale-refreshed-access"
						return credentials, nil
					}}})
				credentials, err := store.RefreshProviderResourceCredentials(context.Background(), resource.ID, true)
				if mutationErr != nil || !mutated {
					t.Fatalf("resource mutation failed: %v, reached=%t", mutationErr, mutated)
				}
				want := http.StatusConflict
				if change == "delete" {
					want = http.StatusNotFound
				}
				if err == nil || AsHTTPError(err).Status != want || credentials != (ProviderResourceCredentials{}) {
					t.Errorf("stale refresh returned %v, want %d with no credentials", err, want)
				}
				current, exists := other.GetProviderResource(resource.ID)
				if change == "delete" {
					if exists {
						t.Error("stale refresh recreated a deleted account")
					}
				} else if !exists || other.providerResourceCredentialsForRuntime(current).AccessToken != "replacement-access" || current.Options[providerResourceReauthorizationRequiredOption] == "true" {
					t.Error("stale refresh changed the recreated account")
				}
			})
		}
	}
}
