package server

import (
	"context"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"

	"gorm.io/gorm"
)

func TestPluginCredentialRefreshGuardsIdentityDuringPersistence(t *testing.T) {
	for _, testCase := range []struct {
		name            string
		resourceRead    int32
		reauthorization bool
		deleteOnly      bool
	}{
		{name: "credentials after identity read", resourceRead: 1},
		{name: "credentials after update read", resourceRead: 2},
		{name: "reauthorization after identity read", resourceRead: 1, reauthorization: true},
		{name: "deletion after identity read", resourceRead: 1, deleteOnly: true},
		{name: "deletion after update read", resourceRead: 2, deleteOnly: true},
		{name: "deletion before reauthorization save", resourceRead: 1, reauthorization: true, deleteOnly: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			databaseURL := "sqlite://" + filepath.Join(t.TempDir(), "refresh-recreation.db")
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
					if sqlDB, err := store.db.DB(); err == nil {
						_ = sqlDB.Close()
					}
				}
			})
			provider := storeA.AddProvider(Provider{Name: "Recreated subscription", Type: ProviderOpenAICodex, Status: StatusActive})
			resource, err := storeA.AddProviderResource(ProviderResource{ProviderID: provider.ID, Name: "Original account", ResourceType: ProviderResourceOpenAISubscription, Credentials: &ProviderResourceCredentials{AccessToken: "original-token"}})
			if err != nil {
				t.Fatal(err)
			}
			var reads atomic.Int32
			var mutationErr error
			const callback = "test:recreate-during-credential-refresh"
			if err := storeA.db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
				if _, ok := tx.Statement.Dest.(*ProviderResource); !ok || reads.Add(1) != testCase.resourceRead {
					return
				}
				mutationErr = storeB.DeleteProviderResource(resource.ID)
				if mutationErr == nil && !testCase.deleteOnly {
					_, mutationErr = storeB.AddProviderResource(ProviderResource{ID: resource.ID, ProviderID: provider.ID, Name: "Replacement account", ResourceType: resource.ResourceType, Credentials: &ProviderResourceCredentials{AccessToken: "replacement-token"}})
				}
			}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = storeA.db.Callback().Query().Remove(callback) })
			credentials := &ProviderResourceCredentials{AccessToken: "stale-refreshed-token"}
			if testCase.reauthorization {
				credentials = nil
			}
			_, err = storeA.ApplyProviderResourceCredentialRefresh(context.Background(), resource, credentials, testCase.reauthorization)
			if mutationErr != nil {
				t.Fatal(mutationErr)
			}
			if reads.Load() < testCase.resourceRead {
				t.Fatal("refresh did not reach the selected persistence boundary")
			}
			want := http.StatusConflict
			if testCase.deleteOnly {
				want = http.StatusNotFound
			}
			if err == nil || AsHTTPError(err).Status != want {
				t.Errorf("refresh against deleted or recreated account returned %v, want %d", err, want)
			}
			current, exists := storeB.GetProviderResource(resource.ID)
			if testCase.deleteOnly {
				if exists {
					t.Fatal("late refresh recreated a deleted resource")
				}
				return
			}
			if !exists || current.Name != "Replacement account" || storeB.providerResourceCredentialsForRuntime(current).AccessToken != "replacement-token" || current.CredentialSummary[providerResourceReauthorizationRequiredOption] == "true" {
				t.Fatal("late refresh changed the recreated account")
			}
		})
	}
}
