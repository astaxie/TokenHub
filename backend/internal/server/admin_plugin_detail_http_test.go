package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestAdminPluginDetailReturnsPackageInventory(t *testing.T) {
	pluginDir := t.TempDir()
	packageDir := filepath.Join(pluginDir, "example.detail")
	writeServerPluginManifest(t, packageDir, `
schema_version: 1
id: example.detail
name: Detail Example
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds: [extension]
placement: [presentation]
entry:
  frontend:
    schema: ui/schema.json
`)
	writeAdminPluginDetailFile(t, packageDir, "ui/schema.json", `{"schema_version":1,"contributions":[]}`)
	writeAdminPluginDetailFile(t, packageDir, "src/main.go", "package main\n")
	writeAdminPluginDetailFile(t, packageDir, "private-secret.txt", "not-visible\n")
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: pluginDir})

	response := doJSON(t, server.Handler(), http.MethodGet, "/api/admin/plugins/example.detail/detail", nil, "dev_admin_token")
	if response.Code != http.StatusOK {
		t.Fatalf("GET plugin detail: expected 200, got %d: %s", response.Code, response.Body)
	}
	var body struct {
		Data adminPluginDetailResponse `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
		t.Fatalf("decode plugin detail: %v", err)
	}
	if body.Data.Plugin.ID != "example.detail" || body.Data.Package == nil || body.Data.Package.FileCount != 4 {
		t.Fatalf("plugin detail = %+v", body.Data)
	}
	files := map[string]pluginmeta.PackageFileInspection{}
	for _, file := range body.Data.Package.Files {
		files[file.Path] = file
	}
	if files["ui/schema.json"].Kind != "schema" || !files["src/main.go"].Viewable || files["private-secret.txt"].Viewable {
		t.Fatalf("plugin file inventory = %+v", files)
	}
}

func TestAdminPluginFileReturnsOnlySafeTextPreview(t *testing.T) {
	pluginDir := t.TempDir()
	packageDir := filepath.Join(pluginDir, "example.files")
	writeServerPluginManifest(t, packageDir, `
schema_version: 1
id: example.files
name: File Example
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds: [extension]
`)
	writeAdminPluginDetailFile(t, packageDir, "src/main.go", "package main\n")
	writeAdminPluginDetailFile(t, packageDir, "credentials.json", `{"token":"not-visible"}`)
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: pluginDir})

	path := "/api/admin/plugins/example.files/file?path=" + url.QueryEscape("src/main.go")
	response := doJSON(t, server.Handler(), http.MethodGet, path, nil, "dev_admin_token")
	if response.Code != http.StatusOK || !json.Valid([]byte(response.Body)) {
		t.Fatalf("GET plugin source: expected 200 JSON, got %d: %s", response.Code, response.Body)
	}
	var body struct {
		Data pluginmeta.PackageFileContent `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Path != "src/main.go" || body.Data.Content != "package main\n" {
		t.Fatalf("plugin source response = %+v", body.Data)
	}

	blocked := doJSON(t, server.Handler(), http.MethodGet, "/api/admin/plugins/example.files/file?path=credentials.json", nil, "dev_admin_token")
	if blocked.Code != http.StatusUnprocessableEntity {
		t.Fatalf("GET private plugin file: expected 422, got %d: %s", blocked.Code, blocked.Body)
	}
	traversal := doJSON(t, server.Handler(), http.MethodGet, "/api/admin/plugins/example.files/file?path=../outside.txt", nil, "dev_admin_token")
	if traversal.Code != http.StatusNotFound {
		t.Fatalf("GET traversal path: expected 404, got %d: %s", traversal.Code, traversal.Body)
	}
}

func writeAdminPluginDetailFile(t *testing.T, root string, relative string, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
