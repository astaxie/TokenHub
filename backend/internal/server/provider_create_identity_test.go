package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
)

func TestAdminRepeatedCatalogCreatePreservesExistingProvider(t *testing.T) {
	store := NewMemoryStore()
	app := New(store).Handler()
	var created []Provider
	for index := range 2 {
		response := doJSON(t, app, http.MethodPost, "/api/admin/providers", map[string]any{
			"catalog_id": "deepseek", "name": "DeepSeek", "api_key": fmt.Sprintf("synthetic-key-%d", index),
			"selected_models": []string{"deepseek-flash"},
		}, "")
		if response.Code != http.StatusCreated {
			t.Fatalf("create %d: status=%d body=%s", index, response.Code, response.Body)
		}
		var result ProviderCreateResult
		if err := json.Unmarshal([]byte(response.Body), &result); err != nil {
			t.Fatal(err)
		}
		if result.ImportedModels != 1 || strings.Contains(response.Body, "synthetic-key") {
			t.Fatalf("unexpected create response: %s", response.Body)
		}
		created = append(created, result.Provider)
	}
	if created[0].ID == "" || created[0].ID == created[1].ID {
		t.Fatalf("catalog creates must have distinct IDs: %v", created)
	}
	for index, provider := range created {
		stored, ok := store.GetProvider(provider.ID)
		if !ok || stored.APIKey != fmt.Sprintf("synthetic-key-%d", index) {
			t.Fatalf("provider %d credentials were replaced or lost", index)
		}
		if stored.Options["catalog_id"] != "deepseek" {
			t.Fatalf("provider %d lost catalog provenance", index)
		}
		models := doJSON(t, app, http.MethodGet, "/api/admin/provider-models?provider_id="+provider.ID, nil, "")
		if models.Code != http.StatusOK || !strings.Contains(models.Body, "deepseek-flash") {
			t.Fatalf("provider %d inventory missing: %s", index, models.Body)
		}
	}
}

func TestAdminProviderCreateRejectsExistingExplicitID(t *testing.T) {
	store := NewMemoryStore()
	app := New(store).Handler()
	original := store.AddProvider(Provider{ID: "prv_preserved", Name: "Original", Type: ProviderOpenAICompatible, APIKey: "synthetic-original-key", BaseURL: "https://original.example/v1"})
	store.AddProviderModel(ProviderModel{ProviderID: original.ID, UpstreamModel: "original-model", Status: StatusActive})
	response := doJSON(t, app, http.MethodPost, "/api/admin/providers", map[string]any{
		"id": original.ID, "name": "Replacement", "api_key": "synthetic-replacement-key",
		"catalog_id": "deepseek", "selected_models": []string{"deepseek-flash"},
	}, "")
	if response.Code != http.StatusConflict || !strings.Contains(response.Body, `"code":"provider_exists"`) {
		t.Fatalf("expected duplicate conflict, got %d: %s", response.Code, response.Body)
	}
	stored, ok := store.GetProvider(original.ID)
	if !ok || stored.Name != original.Name || stored.BaseURL != original.BaseURL || stored.APIKey != "synthetic-original-key" {
		t.Fatal("duplicate create mutated the original provider")
	}
	models := doJSON(t, app, http.MethodGet, "/api/admin/provider-models?provider_id="+original.ID, nil, "")
	if !strings.Contains(models.Body, "original-model") || strings.Contains(models.Body, "deepseek-flash") {
		t.Fatalf("duplicate create mutated model inventory: %s", models.Body)
	}
	update := doJSON(t, app, http.MethodPatch, "/api/admin/providers/"+original.ID, map[string]any{
		"name": "Updated", "type": ProviderOpenAICompatible, "base_url": original.BaseURL,
	}, "")
	if update.Code != http.StatusOK {
		t.Fatalf("explicit update failed: %d: %s", update.Code, update.Body)
	}
	stored, _ = store.GetProvider(original.ID)
	if stored.Name != "Updated" || stored.APIKey != "synthetic-original-key" {
		t.Fatal("explicit update did not preserve the saved credential")
	}
}

func TestCreateProviderConcurrentDuplicateID(t *testing.T) {
	store := NewMemoryStore()
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for index := range 2 {
		wg.Go(func() {
			_, err := store.CreateProvider(Provider{ID: "prv_concurrent", Name: fmt.Sprintf("Provider %d", index), Type: ProviderOpenAICompatible})
			results <- err
		})
	}
	wg.Wait()
	close(results)
	created, conflicts := 0, 0
	for err := range results {
		if err == nil {
			created++
		} else if AsHTTPError(err).Code == "provider_exists" {
			conflicts++
		} else {
			t.Fatalf("unexpected create failure: %v", err)
		}
	}
	if created != 1 || conflicts != 1 {
		t.Fatalf("created=%d conflicts=%d; want one of each", created, conflicts)
	}
}
