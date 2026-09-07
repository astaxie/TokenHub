package server

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestExternalPrivacyHookWithoutIsolationIsQuarantinedBeforeExecution(t *testing.T) {
	pluginDir := copyExternalPluginFixtureForServerTest(t, filepath.Join("..", "plugin", "testdata", "external-privacy-hook"))
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "External Privacy Hook", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "external-privacy-key",
		Allowed: []string{"gpt-privacy"},
		Status:  StatusActive,
	}, "thk_external_privacy")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "prv_external_privacy", Name: "External Privacy Provider", Type: ProviderMock, Status: StatusActive, Healthy: true})
	store.AddModel(Model{Name: "gpt-privacy", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "route_external_privacy", ModelName: "gpt-privacy", ProviderID: provider.ID, ProviderModel: "upstream-chat", Status: StatusActive, Priority: 1, Weight: 100})
	app := NewWithConfig(store, Config{
		AdminToken: "external-privacy-admin",
		PluginDir:  pluginDir,
	})
	if hooks := app.gatewayChain.Hooks(pluginmeta.StagePrivacyPre); len(hooks) != 0 {
		t.Fatalf("quarantined privacy hooks were published: %+v", hooks)
	}

	response := doJSON(t, app.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-privacy",
		"messages": []map[string]any{
			{"role": "user", "content": "raw prompt sentinel"},
		},
	}, secret)
	if response.Code != http.StatusOK || strings.Contains(response.Body, "gateway_hook_failed") {
		t.Fatalf("privacy response = %d %s, want built-in flow without external hook", response.Code, response.Body)
	}
	for _, event := range store.ListAuditEvents() {
		for _, forbidden := range []string{"raw prompt sentinel", "provider-secret"} {
			if strings.Contains(event.AfterSnapshot, forbidden) {
				t.Fatalf("privacy failure audit leaked %q: %s", forbidden, event.AfterSnapshot)
			}
		}
	}
}
