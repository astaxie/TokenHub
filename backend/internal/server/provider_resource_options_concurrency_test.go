package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestProviderResourceMutationPreservesConcurrentProtectedOptions(t *testing.T) {
	databaseURL := "sqlite://" + filepath.Join(t.TempDir(), "provider-resource-options.db")
	storeA, err := NewSQLiteStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	storeB, err := NewSQLiteStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, store := range []*GormStore{storeA, storeB} {
			if sqlDB, dbErr := store.db.DB(); dbErr == nil {
				_ = sqlDB.Close()
			}
		}
	})

	provider := storeA.AddProvider(Provider{
		Name: "Concurrent Protected Options", Type: ProviderOpenAICodex, Status: StatusActive, Healthy: true,
	})
	resource, err := storeA.AddProviderResource(ProviderResource{
		ProviderID: provider.ID, Name: "Before Concurrent Edit", ResourceType: ProviderResourceOpenAISubscription,
		Status: StatusActive, Healthy: true,
		Credentials: &ProviderResourceCredentials{
			AccessToken: "concurrent-options-access", RefreshToken: "concurrent-options-refresh",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	loaded := make(chan struct{})
	release := make(chan struct{})
	var loadOnce sync.Once
	var releaseOnce sync.Once
	releasePatch := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releasePatch)
	callbackName := "test:block-provider-resource-protected-options"
	if err := storeA.db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement == nil || tx.Statement.Schema == nil || tx.Statement.Schema.Table != "provider_resources" {
			return
		}
		loadOnce.Do(func() {
			close(loaded)
			<-release
		})
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := storeA.db.Callback().Query().Remove(callbackName); err != nil {
			t.Errorf("remove query callback: %v", err)
		}
	})

	patchDone := make(chan error, 1)
	go func() {
		_, updateErr := storeA.UpdateProviderResource(resource.ID, ProviderResource{
			ProviderID: provider.ID, Name: "After Concurrent Edit", ResourceType: resource.ResourceType,
			BaseURL: resource.BaseURL, Status: StatusActive, Healthy: true, Weight: resource.Weight,
			Options: map[string]string{"auth_type": "oauth", "operator_note": "retained"},
		})
		patchDone <- updateErr
	}()
	select {
	case <-loaded:
	case <-time.After(time.Second):
		t.Fatal("resource patch did not load the provider resource")
	}

	optionsStarted := make(chan struct{})
	optionsDone := make(chan error, 1)
	go func() {
		close(optionsStarted)
		_, updateErr := storeB.UpdateProviderResourceOptions(resource.ID, map[string]string{
			codexImageCapabilityOption:                 codexImageCapabilitySupported,
			codexImageCapabilityCheckedAtOption:        time.Now().UTC().Format(time.RFC3339Nano),
			openAIAccountReauthorizationRequiredOption: "true",
		})
		optionsDone <- updateErr
	}()
	<-optionsStarted
	select {
	case err := <-optionsDone:
		t.Fatalf("protected option update bypassed the shared resource mutation lease: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	releasePatch()
	if err := <-patchDone; err != nil {
		t.Fatal(err)
	}
	if err := <-optionsDone; err != nil {
		t.Fatal(err)
	}
	updated, ok := storeA.GetProviderResource(resource.ID)
	if !ok {
		t.Fatal("provider resource disappeared after concurrent updates")
	}
	if updated.Name != "After Concurrent Edit" || updated.Options["operator_note"] != "retained" ||
		updated.Options[codexImageCapabilityOption] != codexImageCapabilitySupported ||
		updated.Options[codexImageCapabilityCheckedAtOption] == "" ||
		updated.Options[openAIAccountReauthorizationRequiredOption] != "true" {
		t.Fatalf("concurrent update lost resource or protected option state: %+v", updated)
	}
}

func TestProviderResourceGuardedUpdateDoesNotRecreateDeletedResource(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		delete func(*GormStore, Provider, ProviderResource) error
	}{
		{name: "resource deletion", delete: func(store *GormStore, _ Provider, resource ProviderResource) error {
			return store.DeleteProviderResource(resource.ID)
		}},
		{name: "provider deletion", delete: func(store *GormStore, provider Provider, _ ProviderResource) error {
			return store.DeleteProvider(provider.ID)
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			databaseURL := "sqlite://" + filepath.Join(t.TempDir(), "guarded-resource-update.db")
			storeA, err := NewSQLiteStore(databaseURL)
			if err != nil {
				t.Fatal(err)
			}
			storeB, err := NewSQLiteStore(databaseURL)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				for _, store := range []*GormStore{storeA, storeB} {
					if sqlDB, dbErr := store.db.DB(); dbErr == nil {
						_ = sqlDB.Close()
					}
				}
			})

			provider := storeA.AddProvider(Provider{
				Name: "Guarded Resource Update", Type: ProviderOpenAICodex, Status: StatusActive, Healthy: true,
			})
			resource, err := storeA.AddProviderResource(ProviderResource{
				ProviderID: provider.ID, Name: "Guarded Account", ResourceType: ProviderResourceOpenAISubscription,
				Status: StatusActive, Healthy: true,
				Credentials: &ProviderResourceCredentials{AccessToken: "guarded-access", RefreshToken: "guarded-refresh"},
			})
			if err != nil {
				t.Fatal(err)
			}

			loaded := make(chan struct{})
			release := make(chan struct{})
			var once sync.Once
			callbackName := "test:block-guarded-resource-update"
			if err := storeA.db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
				if tx.Statement == nil || tx.Statement.Schema == nil || tx.Statement.Schema.Table != "provider_resources" {
					return
				}
				once.Do(func() {
					close(loaded)
					<-release
				})
			}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := storeA.db.Callback().Query().Remove(callbackName); err != nil {
					t.Errorf("remove query callback: %v", err)
				}
			})

			updated := make(chan error, 1)
			go func() {
				_, updateErr := storeA.UpdateProviderResource(resource.ID, ProviderResource{
					ProviderID: provider.ID, Name: "Updated Guarded Account", ResourceType: resource.ResourceType,
					Status: StatusActive, Healthy: true, Weight: resource.Weight,
				})
				updated <- updateErr
			}()
			select {
			case <-loaded:
			case <-time.After(time.Second):
				close(release)
				t.Fatal("resource update did not load the provider resource")
			}
			if err := testCase.delete(storeB, provider, resource); err != nil {
				close(release)
				t.Fatal(err)
			}
			close(release)
			if err := <-updated; err == nil {
				t.Fatal("concurrent update unexpectedly succeeded after deletion")
			}
			if _, ok := storeB.GetProviderResource(resource.ID); ok {
				t.Fatal("concurrent update recreated the deleted provider resource")
			}
		})
	}
}

func TestProviderCredentialRefreshPreservesConcurrentAvailabilityState(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		mutate        func(*GormStore, string) error
		expectedState func(*testing.T, ProviderResource)
	}{
		{
			name: "bulk disable",
			mutate: func(store *GormStore, resourceID string) error {
				result, err := store.BulkOperateProviderResources("disable", []string{resourceID})
				if err == nil && result.Success != 1 {
					return NewHTTPError(http.StatusInternalServerError, "test_bulk_disable_failed", "Bulk disable did not update the provider resource")
				}
				return err
			},
			expectedState: func(t *testing.T, resource ProviderResource) {
				t.Helper()
				if resource.Status != StatusDisabled || resource.Healthy || resource.CooldownUntil != nil {
					t.Fatalf("credential refresh overwrote bulk disable state: %+v", resource)
				}
			},
		},
		{
			name: "health update",
			mutate: func(store *GormStore, resourceID string) error {
				_, err := store.SetProviderResourceHealth(resourceID, false)
				return err
			},
			expectedState: func(t *testing.T, resource ProviderResource) {
				t.Helper()
				if resource.Status != StatusActive || resource.Healthy {
					t.Fatalf("credential refresh overwrote health state: %+v", resource)
				}
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			databaseURL := "sqlite://" + filepath.Join(t.TempDir(), "credential-refresh-state.db")
			storeA, err := NewSQLiteStore(databaseURL)
			if err != nil {
				t.Fatal(err)
			}
			storeB, err := NewSQLiteStore(databaseURL)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				for _, store := range []*GormStore{storeA, storeB} {
					if sqlDB, dbErr := store.db.DB(); dbErr == nil {
						_ = sqlDB.Close()
					}
				}
			})

			provider := storeA.AddProvider(Provider{
				Name: "Refresh Availability State", Type: ProviderOpenAICodex, Status: StatusActive, Healthy: true,
			})
			cooldown := time.Now().UTC().Add(time.Minute)
			resource, err := storeA.AddProviderResource(ProviderResource{
				ProviderID: provider.ID, Name: "Refresh Availability Account", ResourceType: ProviderResourceOpenAISubscription,
				Status: StatusActive, Healthy: true, CooldownUntil: &cooldown,
				Credentials: &ProviderResourceCredentials{
					AuthType: "oauth", AccessToken: "availability-access-before", RefreshToken: "availability-refresh-before",
					ClientID: openAIAccountOAuthClientID, ExpiresAt: time.Now().UTC().Add(time.Minute).Format(time.RFC3339),
				},
			})
			if err != nil {
				t.Fatal(err)
			}

			tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				writeJSON(w, http.StatusOK, map[string]any{
					"access_token": "availability-access-after", "refresh_token": "availability-refresh-after", "expires_in": 3600,
				})
			}))
			defer tokenServer.Close()
			previousEndpoint := openAIAccountOAuthTokenEndpoint
			openAIAccountOAuthTokenEndpoint = tokenServer.URL
			defer func() { openAIAccountOAuthTokenEndpoint = previousEndpoint }()

			loaded := make(chan struct{})
			release := make(chan struct{})
			var queryCount atomic.Int32
			callbackName := "test:block-refreshed-resource-save"
			if err := storeA.db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
				if tx.Statement == nil || tx.Statement.Schema == nil || tx.Statement.Schema.Table != "provider_resources" {
					return
				}
				if queryCount.Add(1) == 2 {
					close(loaded)
					<-release
				}
			}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := storeA.db.Callback().Query().Remove(callbackName); err != nil {
					t.Errorf("remove query callback: %v", err)
				}
			})

			refreshDone := make(chan error, 1)
			go func() {
				_, refreshErr := storeA.RefreshProviderResourceCredentials(context.Background(), resource.ID, true)
				refreshDone <- refreshErr
			}()
			select {
			case <-loaded:
			case <-time.After(2 * time.Second):
				close(release)
				t.Fatal("credential refresh did not load the resource before persistence")
			}
			if err := testCase.mutate(storeB, resource.ID); err != nil {
				close(release)
				t.Fatal(err)
			}
			close(release)
			if err := <-refreshDone; err != nil {
				t.Fatal(err)
			}

			stored, ok := storeB.GetProviderResource(resource.ID)
			if !ok {
				t.Fatal("provider resource disappeared after credential refresh")
			}
			credentials := storeB.providerResourceCredentialsForRuntime(stored)
			if credentials.AccessToken != "availability-access-after" || credentials.RefreshToken != "availability-refresh-after" {
				t.Fatalf("credential refresh did not persist rotated credentials: %+v", credentials)
			}
			testCase.expectedState(t, stored)
		})
	}
}
