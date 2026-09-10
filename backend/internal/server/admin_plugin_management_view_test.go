package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestBuiltInPluginPackagesUseValidV2Manifests(t *testing.T) {
	catalogFile := filepath.Join("..", "..", "..", "data", "provider-catalog.json")
	for _, runtime := range builtInPackageRuntimes(catalogFile) {
		packages, err := runtime.Discover()
		if err != nil {
			t.Fatalf("discover built-in packages in %s: %v", runtime.Dir, err)
		}
		if len(packages) == 0 {
			t.Fatalf("no built-in packages discovered in %s", runtime.Dir)
		}
		for _, pkg := range packages {
			if pkg.Manifest.SchemaVersion != pluginmeta.PluginManifestSchemaV2 || pkg.Manifest.TokenHub.PluginAPI != pluginmeta.PluginAPIV2 {
				t.Fatalf("built-in package %s uses schema=%d API=%s", pkg.Manifest.ID, pkg.Manifest.SchemaVersion, pkg.Manifest.TokenHub.PluginAPI)
			}
		}
	}
}

func TestPluginManagementViewTreatsBuiltInProviderCatalogAsInstalled(t *testing.T) {
	catalogFile := filepath.Join("..", "..", "..", "data", "provider-catalog.json")
	server := NewWithConfig(NewMemoryStore(), Config{
		AdminToken:          "plugin-management-test-token",
		PluginDir:           t.TempDir(),
		ProviderCatalogFile: catalogFile,
	})

	plugin := requireAdminPluginDescriptor(t, server, builtinProviderCatalogPluginPrefix+"requesty")
	if plugin.Category != pluginmeta.CategoryProviderIntegration {
		t.Fatalf("provider category = %q", plugin.Category)
	}
	if !plugin.Lifecycle.Available || !plugin.Lifecycle.Installed || !plugin.Lifecycle.Enabled || plugin.Lifecycle.Configured || plugin.Lifecycle.InUse || !plugin.Lifecycle.SetupRequired {
		t.Fatalf("built-in provider lifecycle = %+v", plugin.Lifecycle)
	}
}

func TestPluginManagementViewConnectsProviderConfigurationAndRouteUsage(t *testing.T) {
	store := NewMemoryStore()
	catalogFile := filepath.Join("..", "..", "..", "data", "provider-catalog.json")
	server := NewWithConfig(store, Config{
		AdminToken:          "plugin-management-test-token",
		PluginDir:           t.TempDir(),
		ProviderCatalogFile: catalogFile,
	})
	provider := store.AddProvider(Provider{
		ID:      "prv_requesty",
		Name:    "Requesty",
		Type:    ProviderOpenAICompatible,
		Status:  StatusActive,
		Options: map[string]string{"catalog_id": "requesty"},
	})
	store.AddRoute(ModelRoute{
		ID:            "route_requesty",
		ModelName:     "requesty-chat",
		ProviderID:    provider.ID,
		ProviderModel: "requesty-chat",
		Status:        StatusActive,
	})

	plugin := requireAdminPluginDescriptor(t, server, builtinProviderCatalogPluginPrefix+"requesty")
	if !plugin.Lifecycle.Available || !plugin.Lifecycle.Installed || !plugin.Lifecycle.Enabled || !plugin.Lifecycle.Configured || !plugin.Lifecycle.InUse {
		t.Fatalf("configured provider lifecycle = %+v", plugin.Lifecycle)
	}
	if plugin.Compatibility.Verdict != "compatible" || plugin.Compatibility.PluginAPI != pluginmeta.PluginAPIV2 || plugin.Legacy {
		t.Fatalf("configured provider compatibility = %+v legacy=%t", plugin.Compatibility, plugin.Legacy)
	}
}

func TestBuiltInProviderDetailExposesRealPackageFiles(t *testing.T) {
	catalogFile := filepath.Join("..", "..", "..", "data", "provider-catalog.json")
	server := NewWithConfig(NewMemoryStore(), Config{
		AdminToken:          "plugin-management-test-token",
		PluginDir:           t.TempDir(),
		ProviderCatalogFile: catalogFile,
	})
	response := doJSON(t, server.Handler(), http.MethodGet, "/api/admin/plugins/tokenhub.provider-catalog.requesty/detail", nil, "plugin-management-test-token")
	if response.Code != http.StatusOK {
		t.Fatalf("plugin detail status = %d: %s", response.Code, response.Body)
	}
	var payload struct {
		Data adminPluginDetailResponse `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &payload); err != nil {
		t.Fatalf("decode plugin detail: %v", err)
	}
	if payload.Data.Package == nil || payload.Data.Package.FileCount != 4 {
		t.Fatalf("built-in package inspection = %+v", payload.Data.Package)
	}
	file := doJSON(t, server.Handler(), http.MethodGet, "/api/admin/plugins/tokenhub.provider-catalog.requesty/file?path=plugin.yaml", nil, "plugin-management-test-token")
	if file.Code != http.StatusOK {
		t.Fatalf("plugin file status = %d: %s", file.Code, file.Body)
	}
}

func requireAdminPluginDescriptor(t *testing.T, server *Server, pluginID string) adminPluginDescriptorResponse {
	t.Helper()
	descriptors, err := server.adminPluginDescriptors()
	if err != nil {
		t.Fatalf("list plugin descriptors: %v", err)
	}
	for _, descriptor := range descriptors {
		if descriptor.ID == pluginID {
			return descriptor
		}
	}
	t.Fatalf("plugin %q was not returned", pluginID)
	return adminPluginDescriptorResponse{}
}
