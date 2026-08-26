package server

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestAdminPluginInstallPostDownloadsAndInstallsPackage(t *testing.T) {
	pluginDir := t.TempDir()
	archive := adminPluginZip(t, map[string]string{
		"bundle/plugin.yaml": adminPluginManifest("tokenhub.marketplace.kimi", "Marketplace Kimi", "1.0.0"),
	})
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/zip")
		_, _ = w.Write(archive)
	}))
	defer upstream.Close()
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: pluginDir})
	server.pluginInstallClient = upstream.Client()

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/install", map[string]any{
		"download_url":    upstream.URL + "/kimi.zip",
		"checksum_sha256": adminSHA256Hex(archive),
		"reason":          "installed from marketplace",
		"enable":          false,
		"replace":         false,
	}, "dev_admin_token")
	if response.Code != http.StatusCreated {
		t.Fatalf("POST /api/admin/plugins/install: expected 201, got %d: %s", response.Code, response.Body)
	}
	var body struct {
		Data adminPluginInstallResponse `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
		t.Fatalf("decode install response: %v", err)
	}
	if body.Data.Plugin.ID != "tokenhub.marketplace.kimi" || body.Data.Plugin.Status != pluginmeta.StatusDisabled || !body.Data.RestartRequired {
		t.Fatalf("install response = %+v, want disabled plugin requiring restart", body.Data)
	}
	stateData, err := os.ReadFile(filepath.Join(pluginDir, "tokenhub.marketplace.kimi", "plugin.state.json"))
	if err != nil {
		t.Fatalf("read installed package state: %v", err)
	}
	var state pluginmeta.PackageState
	if err := json.Unmarshal(stateData, &state); err != nil {
		t.Fatalf("decode installed package state: %v", err)
	}
	if state.Status != pluginmeta.StatusDisabled || state.Reason != "installed from marketplace" {
		t.Fatalf("installed package state = %+v", state)
	}
}

func TestAdminPluginInstallPostRejectsUnsafeDownloadURL(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: t.TempDir()})

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/install", map[string]any{
		"download_url":    "http://plugins.example/kimi.zip",
		"checksum_sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}, "dev_admin_token")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("POST unsafe plugin install: expected 400, got %d: %s", response.Code, response.Body)
	}
}

func TestAdminPluginInstallPostRejectsChecksumMismatch(t *testing.T) {
	archive := adminPluginZip(t, map[string]string{
		"plugin.yaml": adminPluginManifest("tokenhub.marketplace.bad-checksum", "Bad Checksum", "1.0.0"),
	})
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer upstream.Close()
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: t.TempDir()})
	server.pluginInstallClient = upstream.Client()

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/install", map[string]any{
		"download_url":    upstream.URL + "/plugin.zip",
		"checksum_sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}, "dev_admin_token")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("POST checksum mismatch plugin install: expected 400, got %d: %s", response.Code, response.Body)
	}
}

func TestAdminPluginUpdatePostDownloadsAndReplacesPackage(t *testing.T) {
	pluginDir := t.TempDir()
	localPluginDir := filepath.Join(pluginDir, "kimi")
	archive := adminPluginZip(t, map[string]string{
		"plugin.yaml": adminPluginManifest("tokenhub.marketplace.kimi", "Marketplace Kimi", "1.1.0"),
	})
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer upstream.Close()
	writeServerPluginManifest(t, localPluginDir, `
schema_version: 1
id: tokenhub.marketplace.kimi
name: Marketplace Kimi
version: 1.0.0
distribution:
  download_url: `+upstream.URL+`/kimi-1.1.0.zip
  checksum_sha256: `+adminSHA256Hex(archive)+`
tokenhub:
  plugin_api: v1
kinds:
  - extension
`)
	if err := os.WriteFile(filepath.Join(localPluginDir, "plugin.state.json"), []byte(`{"status":"disabled","reason":"operator disabled"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: pluginDir})
	server.pluginInstallClient = upstream.Client()

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/tokenhub.marketplace.kimi/update", map[string]any{}, "dev_admin_token")
	if response.Code != http.StatusOK {
		t.Fatalf("POST /api/admin/plugins/{id}/update: expected 200, got %d: %s", response.Code, response.Body)
	}
	var body struct {
		Data adminPluginInstallResponse `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
		t.Fatalf("decode update response: %v", err)
	}
	if body.Data.Plugin.Version != "1.1.0" || body.Data.Plugin.Status != pluginmeta.StatusDisabled || !body.Data.RestartRequired || !body.Data.Replaced {
		t.Fatalf("update response = %+v, want replaced disabled updated package", body.Data)
	}
	stateData, err := os.ReadFile(filepath.Join(localPluginDir, "plugin.state.json"))
	if err != nil {
		t.Fatalf("read updated package state: %v", err)
	}
	var state pluginmeta.PackageState
	if err := json.Unmarshal(stateData, &state); err != nil {
		t.Fatalf("decode updated package state: %v", err)
	}
	if state.Status != pluginmeta.StatusDisabled || state.Reason != "operator disabled" {
		t.Fatalf("updated package state = %+v", state)
	}
}

func TestAdminPluginUpdatePostAcceptsMarketplaceDistributionOverride(t *testing.T) {
	pluginDir := t.TempDir()
	localPluginDir := filepath.Join(pluginDir, "kimi")
	archive := adminPluginZip(t, map[string]string{
		"plugin.yaml": adminPluginManifest("tokenhub.marketplace.kimi", "Marketplace Kimi", "1.2.0"),
	})
	var requestedPath string
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		_, _ = w.Write(archive)
	}))
	defer upstream.Close()
	writeServerPluginManifest(t, localPluginDir, `
schema_version: 1
id: tokenhub.marketplace.kimi
name: Marketplace Kimi
version: 1.0.0
distribution:
  download_url: https://plugins.example/kimi-1.0.0.zip
  checksum_sha256: 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
tokenhub:
  plugin_api: v1
kinds:
  - extension
`)
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: pluginDir})
	server.pluginInstallClient = upstream.Client()

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/tokenhub.marketplace.kimi/update", map[string]any{
		"download_url":    upstream.URL + "/kimi-1.2.0.zip",
		"checksum_sha256": adminSHA256Hex(archive),
	}, "dev_admin_token")
	if response.Code != http.StatusOK {
		t.Fatalf("POST /api/admin/plugins/{id}/update with override: expected 200, got %d: %s", response.Code, response.Body)
	}
	if requestedPath != "/kimi-1.2.0.zip" {
		t.Fatalf("update downloaded %q, want marketplace override path", requestedPath)
	}
	var body struct {
		Data adminPluginInstallResponse `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
		t.Fatalf("decode update response: %v", err)
	}
	if body.Data.Plugin.Version != "1.2.0" || !body.Data.Replaced {
		t.Fatalf("update response = %+v, want marketplace replacement", body.Data)
	}
}

func TestAdminPluginStatePatchWritesLocalPackageState(t *testing.T) {
	pluginDir := t.TempDir()
	localPluginDir := filepath.Join(pluginDir, "privacy")
	writeServerPluginManifest(t, localPluginDir, `
schema_version: 1
id: tokenhub.local-privacy
name: Local Privacy
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - extension
`)
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: pluginDir})

	response := doJSON(t, server.Handler(), http.MethodPatch, "/api/admin/plugins/tokenhub.local-privacy/state", map[string]any{
		"status": "disabled",
		"reason": "disable before upgrade",
	}, "dev_admin_token")
	if response.Code != http.StatusOK {
		t.Fatalf("PATCH /api/admin/plugins/{id}/state: expected 200, got %d: %s", response.Code, response.Body)
	}
	var body struct {
		Data adminPluginStateResponse `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Data.PluginID != "tokenhub.local-privacy" || body.Data.Status != pluginmeta.StatusDisabled || !body.Data.RestartRequired {
		t.Fatalf("state response = %+v, want disabled restart-required response", body.Data)
	}
	data, err := os.ReadFile(filepath.Join(localPluginDir, "plugin.state.json"))
	if err != nil {
		t.Fatalf("read package state: %v", err)
	}
	var state pluginmeta.PackageState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("decode package state: %v", err)
	}
	if state.Status != pluginmeta.StatusDisabled || state.Reason != "disable before upgrade" {
		t.Fatalf("package state = %+v, want disabled state file", state)
	}
}

func adminPluginZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, body := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func adminPluginManifest(id string, name string, version string) string {
	return `
schema_version: 1
id: ` + id + `
name: ` + name + `
version: ` + version + `
tokenhub:
  plugin_api: v1
kinds:
  - extension
`
}

func adminSHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestAdminPluginStatePatchRejectsInvalidState(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: t.TempDir()})

	missing := doJSON(t, server.Handler(), http.MethodPatch, "/api/admin/plugins/tokenhub.local-privacy/state", map[string]any{}, "dev_admin_token")
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("PATCH missing plugin state: expected 400, got %d: %s", missing.Code, missing.Body)
	}

	response := doJSON(t, server.Handler(), http.MethodPatch, "/api/admin/plugins/tokenhub.local-privacy/state", map[string]any{
		"status": "paused",
	}, "dev_admin_token")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("PATCH invalid plugin state: expected 400, got %d: %s", response.Code, response.Body)
	}
}

func TestAdminPluginStatePatchRejectsMissingPlugin(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: t.TempDir()})

	response := doJSON(t, server.Handler(), http.MethodPatch, "/api/admin/plugins/tokenhub.missing/state", map[string]any{
		"status": "disabled",
	}, "dev_admin_token")
	if response.Code != http.StatusNotFound {
		t.Fatalf("PATCH missing plugin state: expected 404, got %d: %s", response.Code, response.Body)
	}
}
