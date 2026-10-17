package server

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testMediaTaskBindingPersistence(t *testing.T, first, second *GormStore) {
	t.Helper()
	ctx := context.Background()
	binding := mediaTaskBinding{ID: NewID("task"), ProjectID: "media-project", APIKeyID: "media-key", AttributedUserID: "owner", Model: "public-video", RouteID: "video-route", ProviderID: "provider", ProviderModel: "seedance-2.0", ResourceID: "original-account", IdentityHash: "credential-fingerprint", ExpiresAt: time.Now().Add(time.Hour).Unix()}
	t.Cleanup(func() { _ = first.db.Delete(&mediaTaskBinding{}, "id = ?", binding.ID).Error })
	if err := first.SaveMediaTaskBindings(ctx, []mediaTaskBinding{binding}, []string{"9007199254740993"}); err != nil {
		t.Fatal(err)
	}
	loaded, upstream, found, err := second.LoadMediaTaskBinding(ctx, binding.ID)
	if err != nil || !found || upstream != "9007199254740993" {
		t.Fatalf("cross-instance task: %+v %q %v %v", loaded, upstream, found, err)
	}
	if !strings.HasPrefix(loaded.UpstreamCiphertext, "enc:v1:") {
		t.Fatal("provider identifier is not encrypted")
	}
	loaded.UpstreamCiphertext = ""
	if loaded != binding {
		t.Fatalf("binding changed: %+v", loaded)
	}
	conflict := binding
	conflict.ResourceID = "replacement-account"
	if err := second.SaveMediaTaskBindings(ctx, []mediaTaskBinding{conflict}, []string{"new-task"}); err == nil {
		t.Fatal("existing task was rebound to another account")
	}
	_, upstream, found, err = first.LoadMediaTaskBinding(ctx, binding.ID)
	if err != nil || !found || upstream != "9007199254740993" {
		t.Fatal("failed rebind changed durable identity")
	}
	if err := first.db.Model(&mediaTaskBinding{}).Where("id = ?", binding.ID).Update("expires_at", time.Now().Add(-time.Minute).Unix()).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, found, err := second.LoadMediaTaskBinding(ctx, binding.ID); err != nil || found {
		t.Fatalf("expired task: %v %v", found, err)
	}
}

func TestMediaTaskSQLitePersistence(t *testing.T) {
	url := "sqlite://" + filepath.Join(t.TempDir(), "media-tasks.db")
	config := Config{SecretKey: "media-task-persistence-secret"}
	first, err := NewSQLiteStoreWithConfig(url, config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	second, err := NewSQLiteStoreWithConfig(url, config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	testMediaTaskBindingPersistence(t, first, second)
}
