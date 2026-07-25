package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type providerCatalogRoundTripper func(*http.Request) (*http.Response, error)

func (fn providerCatalogRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestProviderCatalogServiceReadsDatabaseWithoutUpstream(t *testing.T) {
	store := NewMemoryStore()
	entries := []ProviderCatalogEntry{{
		ID:          "stored-provider",
		Name:        "Stored Provider",
		DisplayName: "Stored Provider",
		Type:        ProviderOpenAICompatible,
		ModelsCount: 1,
		Source:      "test-snapshot",
		Models: []ProviderCatalogModel{{
			ID:          "stored-model",
			DisplayName: "Stored Model",
		}},
	}}
	if err := store.SaveProviderCatalogSnapshot(entries, "test-snapshot", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	var upstreamCalls atomic.Int32
	client := &http.Client{Transport: providerCatalogRoundTripper(func(*http.Request) (*http.Response, error) {
		upstreamCalls.Add(1)
		return nil, errors.New("upstream must not be called")
	})}
	service := newProviderCatalogService(store, client, "https://catalog.invalid/all.json")

	summaries, source, err := service.List(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if source != "test-snapshot" || len(summaries) != 1 || summaries[0].ID != "stored-provider" {
		t.Fatalf("unexpected stored catalog: source=%q entries=%+v", source, summaries)
	}
	if len(summaries[0].Models) != 0 || summaries[0].ModelsCount != 1 {
		t.Fatalf("list should use stored summaries without models: %+v", summaries[0])
	}

	entry, source, ok, err := service.Get(context.Background(), "stored-provider", false)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || source != "test-snapshot" || len(entry.Models) != 1 || entry.Models[0].ID != "stored-model" {
		t.Fatalf("unexpected stored catalog entry: source=%q ok=%v entry=%+v", source, ok, entry)
	}
	if upstreamCalls.Load() != 0 {
		t.Fatalf("ordinary database reads made %d upstream calls", upstreamCalls.Load())
	}
}

func TestProviderCatalogRefreshPersistsLastGoodSnapshot(t *testing.T) {
	store := NewMemoryStore()
	if err := store.SaveProviderCatalogSnapshot([]ProviderCatalogEntry{{
		ID: "old-provider", Name: "Old Provider", DisplayName: "Old Provider", Source: "old-snapshot",
	}}, "old-snapshot", time.Now().UTC().Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/all.json" {
			t.Fatalf("unexpected catalog path %s", r.URL.Path)
		}
		w.Header().Set("content-type", "application/json")
		if err := json.NewEncoder(w).Encode(completeProviderCatalogPayload()); err != nil {
			t.Fatal(err)
		}
	}))
	defer upstream.Close()

	service := newProviderCatalogService(store, upstream.Client(), upstream.URL+"/all.json")
	summaries, source, err := service.List(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if source != "public-provider-conf" || len(summaries) != providerCatalogMinProviders+1 || !providerCatalogContains(summaries, "fresh-provider") {
		t.Fatalf("unexpected refreshed catalog: source=%q entries=%+v", source, summaries)
	}

	partial := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"providers":{"openai":{"name":"OpenAI","models":[{"id":"only-model"}]}}}`))
	}))
	defer partial.Close()
	restarted := newProviderCatalogService(store, partial.Client(), partial.URL)
	persisted, persistedSource, err := restarted.List(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if persistedSource != "public-provider-conf" || len(persisted) != providerCatalogMinProviders+1 || !providerCatalogContains(persisted, "fresh-provider") {
		t.Fatalf("refreshed catalog was not persisted: source=%q entries=%+v", persistedSource, persisted)
	}

	if _, _, err := restarted.List(context.Background(), true); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("expected partial refresh to be rejected, got %v", err)
	}
	lastGood, _, err := restarted.List(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(lastGood) != providerCatalogMinProviders+1 || !providerCatalogContains(lastGood, "fresh-provider") {
		t.Fatalf("failed refresh replaced the last good snapshot: %+v", lastGood)
	}

	failingClient := &http.Client{Transport: providerCatalogRoundTripper(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("github unavailable")
	})}
	failedRefresh := newProviderCatalogService(store, failingClient, "https://catalog.invalid/all.json")
	if _, _, err := failedRefresh.List(context.Background(), true); err == nil || !strings.Contains(err.Error(), "github unavailable") {
		t.Fatalf("expected explicit refresh error, got %v", err)
	}
}

func TestBootstrapSeedsProviderCatalogSnapshot(t *testing.T) {
	store := NewMemoryStore()
	config := Config{
		BootstrapAdminPassword: "provider-catalog-bootstrap-password",
		ModelCatalogFile:       "../../../data/model-catalog.yaml",
	}
	if err := BootstrapBaseDataWithConfig(store, config); err != nil {
		t.Fatal(err)
	}

	entries, source, _, found, err := store.LoadProviderCatalogSnapshot(false)
	if err != nil {
		t.Fatal(err)
	}
	if !found || source != "builtin" || len(entries) < 5 {
		t.Fatalf("expected an installed builtin provider catalog, found=%v source=%q entries=%d", found, source, len(entries))
	}
}

func TestProviderCatalogInitializeReplacesBuiltinSnapshot(t *testing.T) {
	store := NewMemoryStore()
	if err := seedBuiltinProviderCatalog(store); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if err := json.NewEncoder(w).Encode(completeProviderCatalogPayload()); err != nil {
			t.Fatal(err)
		}
	}))
	defer upstream.Close()

	service := newProviderCatalogService(store, upstream.Client(), upstream.URL)
	initialized, err := service.Initialize(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !initialized {
		t.Fatal("expected builtin installation snapshot to be replaced")
	}
	entries, source, err := service.List(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if source != "public-provider-conf" || !providerCatalogContains(entries, "fresh-provider") {
		t.Fatalf("unexpected initialized snapshot: source=%q entries=%+v", source, entries)
	}
}

func completeProviderCatalogPayload() map[string]any {
	providers := map[string]any{}
	ids := []string{"openai", "anthropic", "google", "fresh-provider"}
	for index := len(ids); index < providerCatalogMinProviders; index++ {
		ids = append(ids, fmt.Sprintf("provider-%02d", index))
	}
	for _, id := range ids {
		models := make([]map[string]any, 0, 5)
		for modelIndex := 0; modelIndex < 5; modelIndex++ {
			modelID := fmt.Sprintf("%s-model-%d", id, modelIndex)
			models = append(models, map[string]any{"id": modelID, "name": modelID})
		}
		providers[id] = map[string]any{
			"name":   id,
			"api":    "https://" + id + ".example/v1",
			"models": models,
		}
	}
	return map[string]any{"providers": providers}
}

func providerCatalogContains(entries []ProviderCatalogEntry, id string) bool {
	for _, entry := range entries {
		if entry.ID == id {
			return true
		}
	}
	return false
}
