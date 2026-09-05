package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestBootstrapRegistersEveryTrackedProviderCatalogEntryAsPlugin(t *testing.T) {
	catalogFile := filepath.Join("..", "..", "..", "data", "provider-catalog.json")
	bootstrap, err := bootstrapServerPlugins(NewMemoryStore(), Config{ProviderCatalogFile: catalogFile}, map[string]any{
		ProviderOpenAICodex: &CodexSubscriptionAdapter{},
	})
	if err != nil {
		t.Fatalf("bootstrap provider catalog plugins: %v", err)
	}
	entries, err := loadProviderCatalogPluginEntries(catalogFile, providerCatalogTypesFromRegistry(bootstrap.adapterRegistry), providerCatalogDefaultTypeFromRegistry(bootstrap.adapterRegistry))
	if err != nil {
		t.Fatalf("load provider catalog plugin entries: %v", err)
	}
	if len(entries) < 100 {
		t.Fatalf("tracked provider catalog entries = %d, want at least 100", len(entries))
	}
	pluginIDs := providerCatalogPluginIDs(bootstrap.pluginRegistry)
	for _, entry := range entries {
		if pluginIDs[entry.ID] == "" {
			t.Errorf("provider catalog entry %q has no plugin", entry.ID)
		}
	}
	if pluginIDs["openai"] != "tokenhub.provider.openai" {
		t.Fatalf("OpenAI catalog plugin = %q, want native provider plugin", pluginIDs["openai"])
	}
	if pluginIDs["requesty"] != builtinProviderCatalogPluginPrefix+"requesty" {
		t.Fatalf("Requesty catalog plugin = %q, want generated catalog plugin", pluginIDs["requesty"])
	}
}

func TestGeneratedProviderCatalogPluginKeepsModelsOutOfDescriptor(t *testing.T) {
	catalogFile := writeProviderCatalogPluginFixture(t)
	registry := pluginmeta.NewRegistry()
	adapters := NewAdapterRegistryWithPlugins(registry)
	if err := registerBuiltinProviderAdapters(adapters, map[string]any{ProviderOpenAICodex: &CodexSubscriptionAdapter{}}); err != nil {
		t.Fatalf("register provider adapters: %v", err)
	}
	registerBuiltinProviderCatalogPlugins(registry)
	if err := registerBuiltinProviderCatalogFilePlugins(registry, adapters, catalogFile, pluginmeta.NewRuntime(t.TempDir())); err != nil {
		t.Fatalf("register provider catalog file plugins: %v", err)
	}
	descriptor, ok := registry.Describe(builtinProviderCatalogPluginPrefix + "acme")
	if !ok {
		t.Fatal("generated Acme provider catalog plugin is missing")
	}
	entries := providerCatalogEntriesFromPluginCapabilities(descriptor)
	if len(entries) != 1 {
		t.Fatalf("Acme catalog capabilities = %+v, want one entry", entries)
	}
	if entries[0].ModelsCount != 2 || len(entries[0].Models) != 0 {
		t.Fatalf("Acme plugin models count/models = %d/%d, want 2/0", entries[0].ModelsCount, len(entries[0].Models))
	}
}

func TestDisabledProviderCatalogPluginIsRemovedFromProviderCatalog(t *testing.T) {
	catalogFile := writeProviderCatalogPluginFixture(t)
	pluginDir := t.TempDir()
	pluginID := builtinProviderCatalogPluginPrefix + "acme"
	if _, err := pluginmeta.NewRuntime(pluginDir).UpdateBuiltInPackageState(pluginID, pluginmeta.PackageState{Status: pluginmeta.StatusDisabled}); err != nil {
		t.Fatalf("disable provider catalog plugin: %v", err)
	}
	bootstrap, err := bootstrapServerPlugins(NewMemoryStore(), Config{PluginDir: pluginDir, ProviderCatalogFile: catalogFile}, map[string]any{
		ProviderOpenAICodex: &CodexSubscriptionAdapter{},
	})
	if err != nil {
		t.Fatalf("bootstrap disabled provider catalog plugin: %v", err)
	}
	descriptor, ok := bootstrap.pluginRegistry.Describe(pluginID)
	if !ok || descriptor.Status != pluginmeta.StatusDisabled {
		t.Fatalf("disabled provider catalog plugin = %+v found=%t", descriptor, ok)
	}
	server := &Server{pluginRegistry: bootstrap.pluginRegistry, adapterRegistry: bootstrap.adapterRegistry}
	merged, changed := server.providerCatalogEntriesWithPlugins([]ProviderCatalogEntry{{ID: "acme", Name: "Acme", Type: ProviderOpenAICompatible}})
	if !changed {
		t.Fatal("disabled provider catalog plugin did not change the merged catalog")
	}
	for _, entry := range merged {
		if entry.ID == "acme" {
			t.Fatalf("disabled provider catalog entry remains in merged catalog: %+v", entry)
		}
	}
}

func writeProviderCatalogPluginFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "provider-catalog.json")
	payload := map[string]any{
		"providers": map[string]any{
			"acme": map[string]any{
				"name":         "Acme AI",
				"display_name": "Acme AI",
				"api":          "https://api.acme.example/v1",
				"doc":          "https://docs.acme.example",
				"models": []map[string]any{
					{"id": "acme-chat", "name": "Acme Chat"},
					{"id": "acme-reasoning", "name": "Acme Reasoning"},
				},
			},
		},
	}
	content, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("encode provider catalog fixture: %v", err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write provider catalog fixture: %v", err)
	}
	return path
}
