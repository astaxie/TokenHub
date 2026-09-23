package server

import (
	"reflect"
	"testing"
	"time"
)

func TestGetAPIKeyHydratesAndRedactsLegacyRecord(t *testing.T) {
	store := NewMemoryStore()
	project := store.CreateProject(Project{ID: "prj_get_api_key", Name: "Get API key"})
	stored := APIKey{
		ID:        "key_get_api_key",
		ProjectID: project.ID,
		Name:      "Legacy key",
		KeyHash:   "sensitive-hash",
		Allowed:   []string{"model-b", "model-a", "model-a"},
		Status:    StatusActive,
		CreatedAt: time.Now().UTC(),
	}
	if err := store.db.Create(&stored).Error; err != nil {
		t.Fatalf("create legacy API key: %v", err)
	}

	key, ok := store.GetAPIKey(stored.ID)
	if !ok {
		t.Fatal("GetAPIKey did not find the stored API key")
	}
	if key.KeyHash != "" {
		t.Fatalf("GetAPIKey exposed key hash %q", key.KeyHash)
	}
	if key.ModelAccessMode != ModelAccessModeRestricted {
		t.Fatalf("model access mode = %q, want %q", key.ModelAccessMode, ModelAccessModeRestricted)
	}
	if len(key.Allowed) != 2 || key.Allowed[0] != "model-a" || key.Allowed[1] != "model-b" {
		t.Fatalf("allowed models = %#v, want sorted unique models", key.Allowed)
	}
	if !key.AllowedModels["model-a"] || !key.AllowedModels["model-b"] {
		t.Fatalf("hydrated allowed model set = %#v", key.AllowedModels)
	}

	if missing, ok := store.GetAPIKey("key_missing"); ok || !reflect.DeepEqual(missing, APIKey{}) {
		t.Fatalf("missing lookup = (%#v, %v), want zero value and false", missing, ok)
	}
}

type apiKeySingleLookupStore struct {
	Store
	key      APIKey
	getCalls int
}

func (s *apiKeySingleLookupStore) GetAPIKey(id string) (APIKey, bool) {
	s.getCalls++
	if id != s.key.ID {
		return APIKey{}, false
	}
	return s.key, true
}

func (s *apiKeySingleLookupStore) ListAPIKeys() []APIKey {
	panic("findAPIKey must not list all API keys")
}

func TestFindAPIKeyUsesSingleLookupAndPreservesNotFoundError(t *testing.T) {
	store := &apiKeySingleLookupStore{key: APIKey{ID: "key_single_lookup", Name: "Single lookup"}}
	server := &Server{store: store}

	key, err := server.findAPIKey(store.key.ID)
	if err != nil {
		t.Fatalf("find existing API key: %v", err)
	}
	if !reflect.DeepEqual(key, store.key) {
		t.Fatalf("found API key = %#v, want %#v", key, store.key)
	}
	if store.getCalls != 1 {
		t.Fatalf("GetAPIKey calls = %d, want 1", store.getCalls)
	}

	_, err = server.findAPIKey("key_missing")
	httpErr := AsHTTPError(err)
	if httpErr.Status != 404 || httpErr.Code != "api_key_not_found" || httpErr.Message != "API key not found" {
		t.Fatalf("missing API key error = %#v", httpErr)
	}
}
