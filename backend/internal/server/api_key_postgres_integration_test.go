//go:build integration

package server

import (
	"context"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
)

type apiKeyUpdateBlockContextKey struct{}

func testPostgresAPIKeyUpdatePreservesLastUsed(t *testing.T, store *GormStore) {
	t.Helper()
	suffix := NewID("key-last-used-order")
	project := store.CreateProject(Project{ID: "prj_" + suffix, Name: "Last used ordering", Status: StatusActive})
	key, secret, err := store.CreateAPIKey(project.ID, APIKey{
		ID: "key_" + suffix, Name: "Original key name", Status: StatusActive,
	}, "thk_last_used_order_"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.DeleteAPIKey(key.ID)
		_ = store.DeleteProject(project.ID)
	})

	callbackName := "test:block-api-key-update:" + suffix
	blocked := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	if err := store.db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		shouldBlock, _ := tx.Statement.Context.Value(apiKeyUpdateBlockContextKey{}).(bool)
		if !shouldBlock || tx.Statement.Table != "api_keys" {
			return
		}
		once.Do(func() {
			close(blocked)
			<-release
		})
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.db.Callback().Query().Remove(callbackName) })

	ctx := context.WithValue(context.Background(), apiKeyUpdateBlockContextKey{}, true)
	updateDone := make(chan error, 1)
	go func() {
		_, err := store.WithContext(ctx).UpdateAPIKey(key.ID, APIKey{Name: "Updated key name"})
		updateDone <- err
	}()
	select {
	case <-blocked:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("UpdateAPIKey did not pause after reading the API key")
	}

	_, validatedKey, err := store.ValidateAPIKey(secret, "127.0.0.1")
	if err != nil {
		close(release)
		t.Fatalf("concurrent validation failed: %v", err)
	}
	if validatedKey.LastUsedAt == nil {
		close(release)
		t.Fatal("concurrent validation did not report last_used_at")
	}
	close(release)
	select {
	case err := <-updateDone:
		if err != nil {
			t.Fatalf("UpdateAPIKey failed after release: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("UpdateAPIKey did not finish after release")
	}

	var persisted APIKey
	if err := store.db.First(&persisted, "id = ?", key.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.LastUsedAt == nil {
		t.Fatal("UpdateAPIKey overwrote the concurrent last_used_at update")
	}
	if persisted.Name != "Updated key name" {
		t.Fatalf("updated key name = %q", persisted.Name)
	}
}
