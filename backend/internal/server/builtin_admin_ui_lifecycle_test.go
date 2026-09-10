package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestBuiltinPresentationLifecycleAppliesImmediatelyAndAfterRestart(t *testing.T) {
	for _, pluginID := range []string{
		"tokenhub.sim.antd", "tokenhub.sim.default", "tokenhub.sim.knowledge-sidebar",
		"tokenhub.admin.plugin-ecosystem", "tokenhub.admin.core-provider", "tokenhub.provider.openai-codex",
	} {
		t.Run(pluginID, func(t *testing.T) {
			config := Config{AdminToken: "dev_admin_token", PluginDir: t.TempDir()}
			server := NewWithConfig(NewMemoryStore(), config)
			contributionCount := countBuiltinPresentationContributions(server, pluginID)
			assertBuiltinPresentationStatus(t, server, pluginID, pluginmeta.StatusEnabled, contributionCount)

			patchBuiltinPresentationStatus(t, server, pluginID, pluginmeta.StatusDisabled)
			assertBuiltinPresentationStatus(t, server, pluginID, pluginmeta.StatusDisabled, 0)
			if pluginID != "tokenhub.sim.default" {
				assertBuiltinPresentationStatus(t, server, "tokenhub.sim.default", pluginmeta.StatusEnabled, 0)
			} else {
				assertBuiltinPresentationStatus(t, server, "tokenhub.sim.antd", pluginmeta.StatusEnabled, 0)
			}

			restarted := NewWithConfig(NewMemoryStore(), config)
			assertBuiltinPresentationStatus(t, restarted, pluginID, pluginmeta.StatusDisabled, 0)
			patchBuiltinPresentationStatus(t, restarted, pluginID, pluginmeta.StatusEnabled)
			assertBuiltinPresentationStatus(t, restarted, pluginID, pluginmeta.StatusEnabled, contributionCount)
		})
	}
}

func TestBuiltinPresentationRejectsUnreadableLifecycleState(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, ".built-in-state", "tokenhub.sim.antd")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "plugin.state.json"), []byte("invalid JSON"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := bootstrapServerPlugins(Config{PluginDir: root}, nil)
	if err == nil {
		t.Fatal("invalid lifecycle state silently re-enabled the built-in template")
	}
}

func patchBuiltinPresentationStatus(t *testing.T, server *Server, pluginID string, status pluginmeta.Status) {
	t.Helper()
	response := doJSON(t, server.Handler(), http.MethodPatch, "/api/admin/plugins/"+pluginID+"/state", map[string]any{"status": status}, "dev_admin_token")
	if response.Code != http.StatusOK {
		t.Fatalf("set plugin %s to %s: %d %s", pluginID, status, response.Code, response.Body)
	}
	var body struct {
		Data adminPluginStateResponse `json:"data"`
	}
	if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
		t.Fatal(err)
	}
	enabled := status == pluginmeta.StatusEnabled
	if body.Data.Lifecycle.ActiveEnabled != enabled || body.Data.Lifecycle.DesiredEnabled != enabled || body.Data.Lifecycle.RestartRequired || body.Data.RestartRequired {
		t.Fatalf("plugin %s lifecycle did not apply immediately: %+v", pluginID, body.Data)
	}
}

func assertBuiltinPresentationStatus(t *testing.T, server *Server, pluginID string, status pluginmeta.Status, contributions int) {
	t.Helper()
	descriptor, found := server.pluginRegistry.Describe(pluginID)
	if !found || descriptor.Status != status {
		t.Fatalf("plugin %s status = %s, found = %t; want %s", pluginID, descriptor.Status, found, status)
	}
	if len(descriptor.Capabilities) == 0 {
		t.Fatalf("plugin %s lost its inspectable capability declarations", pluginID)
	}
	if got := countBuiltinPresentationContributions(server, pluginID); got != contributions {
		t.Fatalf("plugin %s publishes %d contributions; want %d", pluginID, got, contributions)
	}
	if pluginID == "tokenhub.provider.openai-codex" {
		_, available := server.adapterRegistry.Describe(ProviderOpenAICodex)
		if available != (status == pluginmeta.StatusEnabled) {
			t.Fatalf("Codex Provider adapter available = %t with plugin status %s", available, status)
		}
	}
}

func countBuiltinPresentationContributions(server *Server, pluginID string) int {
	count := 0
	for _, contribution := range server.adminUI.List() {
		if contribution.PluginID == pluginID {
			count++
		}
	}
	return count
}
