package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRunStartupBootstrapReloadsCatalogOnEveryStart(t *testing.T) {
	store := NewMemoryStore()
	catalogPath := filepath.Join(t.TempDir(), "model-catalog.yaml")
	config := Config{
		BootstrapAdminPassword: "startup-bootstrap-test-password",
		ModelCatalogFile:       catalogPath,
	}
	writeCatalog := func(category string) {
		t.Helper()
		content := []byte("version: 1\nmodels:\n  - name: startup-reloaded-model\n    category: " + category + "\n")
		if err := os.WriteFile(catalogPath, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	modelCategory := func() string {
		t.Helper()
		for _, model := range store.ListModels() {
			if model.Name == "startup-reloaded-model" {
				return model.Category
			}
		}
		t.Fatal("startup catalog model was not loaded")
		return ""
	}

	writeCatalog("first-start")
	if err := RunStartupBootstrap(context.Background(), store, config); err != nil {
		t.Fatal(err)
	}
	if got := modelCategory(); got != "first-start" {
		t.Fatalf("unexpected first-start category %q", got)
	}

	writeCatalog("second-start")
	if err := RunStartupBootstrap(context.Background(), store, config); err != nil {
		t.Fatal(err)
	}
	if got := modelCategory(); got != "second-start" {
		t.Fatalf("catalog edit was not applied on restart: category=%q", got)
	}
}

func TestRunStartupBootstrapPreservesExistingModelPrices(t *testing.T) {
	store := NewMemoryStore()
	catalogPath := filepath.Join(t.TempDir(), "model-catalog.yaml")
	config := Config{
		BootstrapAdminPassword: "startup-price-test-password",
		ModelCatalogFile:       catalogPath,
	}
	writeCatalog := func(content string) {
		t.Helper()
		if err := os.WriteFile(catalogPath, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	modelsByName := func() map[string]Model {
		t.Helper()
		models := map[string]Model{}
		for _, model := range store.ListModels() {
			models[model.Name] = model
		}
		return models
	}

	writeCatalog(`
version: 1
models:
  - name: startup-priced-model
    category: first
    family: startup
    modality: chat
    context_window: 1000
    input_price_usd_per_1m: 1
    cache_read_price_usd_per_1m: 0.1
    output_price_usd_per_1m: 2
    embedding_price_usd_per_1m: 0.2
`)
	if err := RunStartupBootstrap(context.Background(), store, config); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateModel("startup-priced-model", Model{
		InputPriceUSDPer1M:     7,
		CacheReadPriceUSDPer1M: 0,
		OutputPriceUSDPer1M:    9,
		EmbeddingPriceUSDPer1M: 0.9,
	}); err != nil {
		t.Fatal(err)
	}

	writeCatalog(`
version: 1
models:
  - name: startup-priced-model
    category: second
    family: startup
    modality: chat
    context_window: 2000
    input_price_usd_per_1m: 10
    cache_read_price_usd_per_1m: 1
    output_price_usd_per_1m: 20
    embedding_price_usd_per_1m: 2
  - name: startup-new-model
    category: second
    family: startup
    modality: chat
    context_window: 3000
    input_price_usd_per_1m: 11
    cache_read_price_usd_per_1m: 1.1
    output_price_usd_per_1m: 22
    embedding_price_usd_per_1m: 2.2
`)
	if err := RunStartupBootstrap(context.Background(), store, config); err != nil {
		t.Fatal(err)
	}

	models := modelsByName()
	priced := models["startup-priced-model"]
	if priced.InputPriceUSDPer1M != 7 || priced.CacheReadPriceUSDPer1M != 0 ||
		priced.OutputPriceUSDPer1M != 9 || priced.EmbeddingPriceUSDPer1M != 0.9 {
		t.Fatalf("startup replaced custom prices: %+v", priced)
	}
	if priced.Category != "second" || priced.ContextWindow != 2000 {
		t.Fatalf("startup did not refresh non-price catalog fields: %+v", priced)
	}
	newModel := models["startup-new-model"]
	if newModel.InputPriceUSDPer1M != 11 || newModel.CacheReadPriceUSDPer1M != 1.1 ||
		newModel.OutputPriceUSDPer1M != 22 || newModel.EmbeddingPriceUSDPer1M != 2.2 {
		t.Fatalf("new catalog model did not use catalog prices: %+v", newModel)
	}
}
