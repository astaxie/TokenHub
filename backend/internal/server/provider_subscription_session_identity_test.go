package server

import (
	"context"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"

	"gorm.io/gorm"
)

func TestSubscriptionSessionRotationGuardsFinalPersistence(t *testing.T) {
	for _, action := range []string{"recreate", "delete", "upsert"} {
		t.Run(action, func(t *testing.T) {
			databaseURL := "sqlite://" + filepath.Join(t.TempDir(), "session-rotation.db")
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
			provider := storeA.AddProvider(Provider{Name: "Session rotation", Type: "cursor_subscription", Status: StatusActive})
			const original = `{"type":"oauth","access":"original-token"}`
			const replacement = `{"type":"oauth","access":"replacement-token"}`
			resource, err := storeA.AddProviderResource(ProviderResource{ProviderID: provider.ID, Name: "Original account", ResourceType: "cursor_account", Credentials: &ProviderResourceCredentials{SessionJSON: original}})
			if err != nil {
				t.Fatal(err)
			}
			var mutated atomic.Bool
			var mutationErr error
			const callback = "test:replace-during-session-rotation"
			if err := storeA.db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
				if _, ok := tx.Statement.Dest.(*ProviderResource); !ok || mutated.Swap(true) {
					return
				}
				if action != "upsert" {
					mutationErr = storeB.DeleteProviderResource(resource.ID)
				}
				if mutationErr == nil && action != "delete" {
					_, mutationErr = storeB.AddProviderResource(ProviderResource{ID: resource.ID, ProviderID: provider.ID, Name: "Replacement account", ResourceType: resource.ResourceType, Credentials: &ProviderResourceCredentials{SessionJSON: replacement}, Options: map[string]string{"replacement_marker": "keep"}})
				}
			}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = storeA.db.Callback().Query().Remove(callback) })
			err = storeA.ReplaceProviderSession(context.Background(), resource.ID, provider.Type, original, `{"type":"oauth","access":"stale-rotation","refresh":"stale-refresh"}`)
			if mutationErr != nil {
				t.Fatal(mutationErr)
			}
			if !mutated.Load() {
				t.Fatal("rotation did not reach the persistence boundary")
			}
			want := http.StatusConflict
			if action == "delete" {
				want = http.StatusNotFound
			}
			if err == nil || AsHTTPError(err).Status != want {
				t.Errorf("rotation against replaced account returned %v, want %d", err, want)
			}
			current, exists := storeB.GetProviderResource(resource.ID)
			if action == "delete" {
				if exists {
					t.Fatal("rotation recreated a deleted account")
				}
				return
			}
			if !exists || current.Name != "Replacement account" || storeB.providerResourceCredentialsForRuntime(current).SessionJSON != replacement || current.Options["replacement_marker"] != "keep" {
				t.Fatal("rotation overwrote the replacement account")
			}
		})
	}
}
