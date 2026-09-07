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

func TestAdminPluginDetailShowsExternalCommandPackageAsInstalledButNotOperational(t *testing.T) {
	pluginDir := t.TempDir()
	packageDir := filepath.Join(pluginDir, "example.command")
	writeServerPluginManifest(t, packageDir, `
schema_version: 2
id: example.command
name: External Command Example
version: 1.0.0
summary: Exercises the external command lifecycle boundary.
category: ui_template
tokenhub:
  plugin_api: v2
kinds: [extension, sim]
placement: [background, presentation]
entry:
  backend:
    protocol: stdio-json-v1
    command: bin/run
capabilities:
  background_jobs:
    - id: example.run
      title: Run example
      schedule: "0 * * * *"
  sim:
    theme_tokens:
      - id: command-theme
        mode: light
        tokens:
          accent: "#2563eb"
permissions:
  data:
    read: []
    write: []
`)
	writeAdminPluginDetailFile(t, packageDir, "bin/run", "#!/bin/sh\nprintf '{}'")
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: pluginDir})

	response := doJSON(t, server.Handler(), http.MethodGet, "/api/admin/plugins/example.command/detail", nil, "dev_admin_token")
	if response.Code != http.StatusOK {
		t.Fatalf("GET external command plugin detail: expected 200, got %d: %s", response.Code, response.Body)
	}
	var body struct {
		Data adminPluginDetailResponse `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
		t.Fatalf("decode external command plugin detail: %v", err)
	}
	plugin := body.Data.Plugin
	if plugin.Status != pluginmeta.StatusFailedStartup || plugin.Loadable ||
		plugin.Lifecycle.Enabled || plugin.Lifecycle.ActiveEnabled ||
		plugin.HasSettings || plugin.Lifecycle.DesiredVersion != "1.0.0" || plugin.Lifecycle.ActiveVersion != "" ||
		plugin.LastErrorCode != string(pluginmeta.PluginErrorPermissionUnsupported) ||
		body.Data.Package == nil || body.Data.Package.FileCount == 0 {
		t.Fatalf("external command plugin detail = %+v, want inspectable startup failure", body.Data)
	}
	hasSIMDeclaration := false
	for _, capability := range plugin.Capabilities {
		if capability.Kind == pluginmeta.CapabilityKindSIM && capability.Name == pluginmeta.SIMCapabilityThemeTokens && capability.Subject == "command-theme" {
			hasSIMDeclaration = true
			break
		}
	}
	if !hasSIMDeclaration {
		t.Fatalf("external command plugin detail lost its inspectable SIM declaration: %+v", plugin.Capabilities)
	}
	for _, contribution := range server.adminUI.List() {
		if contribution.PluginID == "example.command" {
			t.Fatalf("external command plugin published an admin UI contribution: %+v", contribution)
		}
	}
	if job, ok := server.pluginBackgroundJobs.Describe("example.command", "example.run"); ok {
		t.Fatalf("external command job was published: %+v", job)
	}
}

func TestAdminPluginDescriptorsPreserveBuiltInSIMFallbackCapabilities(t *testing.T) {
	pluginDir := t.TempDir()
	packageDir := filepath.Join(pluginDir, "default-sim-override")
	writeServerPluginManifest(t, packageDir, `
schema_version: 2
id: tokenhub.sim.default
name: Quarantined Default SIM Override
version: 2.0.0
summary: Exercises the built-in SIM fallback boundary.
category: ui_template
tokenhub:
  plugin_api: v2
kinds: [sim]
placement: [presentation]
entry:
  backend:
    protocol: stdio-json-v1
    command: run.sh
capabilities:
  sim:
    theme_tokens:
      - id: external-theme
        mode: light
        tokens:
          accent: "#dc2626"
permissions:
  data:
    read: []
    write: []
`)
	writeAdminPluginDetailFile(t, packageDir, "run.sh", "#!/bin/sh\nprintf '{}'")
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", PluginDir: pluginDir})

	plugin := requireAdminPluginDescriptor(t, server, "tokenhub.sim.default")
	if plugin.Name != "Quarantined Default SIM Override" || plugin.Version != "2.0.0" ||
		plugin.Status != pluginmeta.StatusFailedStartup || plugin.Loadable ||
		plugin.Lifecycle.ActiveVersion != pluginmeta.BuiltInVersion || !plugin.Lifecycle.ActiveEnabled ||
		plugin.Lifecycle.RestartRequired {
		t.Fatalf("built-in SIM fallback descriptor = %+v", plugin)
	}
	desiredFound := false
	for _, capability := range plugin.Capabilities {
		if capability.Kind == pluginmeta.CapabilityKindSIM && capability.Subject == "external-theme" {
			desiredFound = true
		}
	}
	activeFound := false
	for _, capability := range plugin.ActiveCapabilities {
		if capability.Kind == pluginmeta.CapabilityKindSIM && capability.Subject == "default-light" {
			activeFound = true
		}
		if capability.Subject == "external-theme" {
			t.Fatalf("quarantined SIM capability appeared in the active projection: %+v", plugin.ActiveCapabilities)
		}
	}
	if !desiredFound || !activeFound {
		t.Fatalf("desired capabilities = %+v, active capabilities = %+v", plugin.Capabilities, plugin.ActiveCapabilities)
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
