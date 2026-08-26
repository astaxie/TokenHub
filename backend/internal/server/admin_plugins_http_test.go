package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

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
