package server

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"reflect"
	"testing"

	"gorm.io/gorm"
	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestCredentialPersistenceRejectsConcurrentProviderTypeChange(t *testing.T) {
	for _, operation := range []string{"native_success", "native_reauthorization", "session", "plugin_success", "plugin_reauthorization", "store_success", "store_reauthorization"} {
		t.Run(operation, func(t *testing.T) {
			url := "sqlite://" + filepath.Join(t.TempDir(), "provider-binding.db")
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
			testCredentialPersistenceProviderTypeChange(t, store, other, operation)
		})
	}
}

func testCredentialPersistenceProviderTypeChange(t *testing.T, store, other *GormStore, operation string) {
	t.Helper()
	store.ConfigureProviderResourceTypePolicy(map[string][]string{ProviderChatGPTSubscription: {ProviderResourceChatGPTSubscription}})
	suffix := NewID("provider-binding")
	provider := store.AddProvider(Provider{Name: "Original provider " + suffix, Type: ProviderChatGPTSubscription, Options: map[string]string{providerCredentialRefreshProfileOption: "synthetic-refresh"}})
	t.Cleanup(func() { _ = store.DeleteProvider(provider.ID) })
	original := ProviderResourceCredentials{AccessToken: "original-access", RefreshToken: "original-refresh", SessionJSON: `{"session":"original"}`}
	resource, err := store.AddProviderResource(ProviderResource{ProviderID: provider.ID, Name: "Original account " + suffix, ResourceType: ProviderResourceChatGPTSubscription, Credentials: &original})
	if err != nil {
		t.Fatal(err)
	}
	var before ProviderResource
	if err := other.db.First(&before, "id = ?", resource.ID).Error; err != nil {
		t.Fatal(err)
	}
	resource = before
	var mutationErr error
	mutated := false
	const callback = "test:change-provider-before-credential-write"
	if err := store.db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table != "provider_resources" || mutated {
			return
		}
		mutated = true
		_, mutationErr = other.UpdateProvider(provider.ID, Provider{Type: ProviderGitHubCopilotSubscription})
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.db.Callback().Update().Remove(callback) })
	var credentials ProviderResourceCredentials
	switch operation {
	case "native_success", "native_reauthorization":
		store.ConfigureProviderCredentialRefreshHandlers([]providerResourceCredentialRefreshRegistration{{ProviderType: provider.Type, Profile: "synthetic-refresh", Refresh: func(_ context.Context, current ProviderResourceCredentials) (ProviderResourceCredentials, error) {
			if operation == "native_reauthorization" {
				return ProviderResourceCredentials{}, NewHTTPError(http.StatusConflict, "provider_resource_reauthorization_required", "Synthetic refresh was rejected")
			}
			current.AccessToken = "stale-refresh"
			return current, nil
		}}})
		credentials, err = store.RefreshProviderResourceCredentials(context.Background(), resource.ID, true)
	case "session":
		err = store.ReplaceProviderSession(context.Background(), resource.ID, provider.Type, original.SessionJSON, `{"session":"rotated"}`)
	case "store_success", "store_reauthorization":
		var updated *ProviderResourceCredentials
		if operation == "store_success" {
			updated = &ProviderResourceCredentials{AccessToken: "stale-refresh"}
		}
		_, err = store.ApplyProviderResourceCredentialRefresh(context.Background(), resource, updated, operation == "store_reauthorization")
	default:
		server := &Server{store: store}
		payload, marshalErr := json.Marshal(map[string]string{"resource_id": resource.ID})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		data := map[string]any{"credentials": map[string]any{"access_token": "stale-refresh"}}
		if operation == "plugin_reauthorization" {
			data = map[string]any{"reauthorization_required": true}
		}
		_, err = server.applyCredentialsRefreshActionSideEffects(context.Background(), pluginmeta.ActionDescriptor{Subject: provider.Type}, payload, pluginmeta.ActionResult{Data: data})
	}
	if !mutated || mutationErr != nil {
		t.Fatalf("provider mutation failed: reached=%t error=%v", mutated, mutationErr)
	}
	if err == nil || AsHTTPError(err).Status != http.StatusConflict || AsHTTPError(err).Code != "provider_resource_changed" {
		t.Errorf("credential write accepted a concurrent provider change: %v", err)
	}
	if credentials != (ProviderResourceCredentials{}) {
		t.Error("stale refresh returned credentials after a provider change")
	}
	var after ProviderResource
	if err := other.db.First(&after, "id = ?", resource.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Error("credential write changed the account after its provider changed")
	}
}
