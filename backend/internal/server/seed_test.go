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
