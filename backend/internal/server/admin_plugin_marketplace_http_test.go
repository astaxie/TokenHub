package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestAdminPluginMarketplaceGetAnnotatesInstalledPlugins(t *testing.T) {
	pluginDir := t.TempDir()
	writeServerPluginManifest(t, filepath.Join(pluginDir, "kimi"), `
schema_version: 1
id: tokenhub.marketplace.kimi
name: Marketplace Kimi
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - extension
`)
	available := pluginmeta.Descriptor{
		ID:      "tokenhub.marketplace.kimi",
		Name:    "Marketplace Kimi",
		Version: "1.1.0",
		Source:  pluginmeta.SourceMarketplace,
		Kinds:   []pluginmeta.Kind{pluginmeta.KindExtension},
		Distribution: &pluginmeta.Distribution{
			DownloadURL:    "https://plugins.example/kimi-1.1.0.zip",
			ChecksumSHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		},
	}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"plugins": []pluginmeta.Descriptor{available}})
	}))
	defer upstream.Close()

	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: pluginDir, PluginMarketplaceURL: upstream.URL + "/index.json"})
	server.pluginMarketplaceClient = upstream.Client()

	response := doJSON(t, server.Handler(), http.MethodGet, "/api/admin/plugin-marketplace", nil, "dev_admin_token")
	if response.Code != http.StatusOK {
		t.Fatalf("GET /api/admin/plugin-marketplace: expected 200, got %d: %s", response.Code, response.Body)
	}
	var body struct {
		Data adminPluginMarketplaceResponse `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
		t.Fatalf("decode marketplace response: %v", err)
	}
	if !body.Data.Available || body.Data.SourceURL != upstream.URL+"/index.json" {
		t.Fatalf("marketplace response = %+v", body.Data)
	}
	if len(body.Data.Plugins) != 1 || !body.Data.Plugins[0].Installed || !body.Data.Plugins[0].UpdateAvailable || body.Data.Plugins[0].InstalledVersion != "1.0.0" {
		t.Fatalf("marketplace plugin annotation = %+v", body.Data.Plugins)
	}
}

func TestAdminPluginMarketplaceGetReportsSourceErrors(t *testing.T) {
	pluginDir := t.TempDir()
	writeServerPluginManifest(t, filepath.Join(pluginDir, "kimi"), `
schema_version: 1
id: tokenhub.marketplace.kimi
name: Marketplace Kimi
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - extension
`)
	upstream := httptest.NewTLSServer(http.NotFoundHandler())
	defer upstream.Close()

	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: pluginDir, PluginMarketplaceURL: upstream.URL + "/index.json"})
	server.pluginMarketplaceClient = upstream.Client()

	response := doJSON(t, server.Handler(), http.MethodGet, "/api/admin/plugin-marketplace", nil, "dev_admin_token")
	if response.Code != http.StatusOK {
		t.Fatalf("GET /api/admin/plugin-marketplace: expected 200, got %d: %s", response.Code, response.Body)
	}
	var body struct {
		Data adminPluginMarketplaceResponse `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
		t.Fatalf("decode marketplace response: %v", err)
	}
	if body.Data.Error == "" || body.Data.Available {
		t.Fatalf("expected source error in marketplace response, got %+v", body.Data)
	}
}
