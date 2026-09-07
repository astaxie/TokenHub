package server

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestExternalGuardrailHookWithoutIsolationIsQuarantinedBeforeExecution(t *testing.T) {
	pluginDir := copyExternalPluginFixtureForServerTest(t, filepath.Join("..", "plugin", "testdata", "external-guardrail-hook"))
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "External Guardrail Hook", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "external-guardrail-key",
		Allowed: []string{"gpt-external-guardrail"},
		Status:  StatusActive,
	}, "thk_external_guardrail")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "prv_external_guardrail", Name: "External Guardrail Provider", Type: ProviderMock, Status: StatusActive, Healthy: true})
	store.AddModel(Model{Name: "gpt-external-guardrail", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "route_external_guardrail", ModelName: "gpt-external-guardrail", ProviderID: provider.ID, ProviderModel: "upstream-chat", Status: StatusActive, Priority: 1, Weight: 100})
	app := NewWithConfig(store, Config{
		AdminToken: "external-guardrail-admin",
		PluginDir:  pluginDir,
	})
	if hooks := app.gatewayChain.Hooks(pluginmeta.StageGuardrailPost); len(hooks) != 0 {
		t.Fatalf("quarantined guardrail hooks were published: %+v", hooks)
	}

	response := doJSON(t, app.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-external-guardrail",
		"messages": []map[string]any{
			{"role": "user", "content": "unsafe prompt sentinel"},
		},
	}, secret)
	if response.Code != http.StatusOK || strings.Contains(response.Body, "gateway_hook_failed") {
		t.Fatalf("guardrail response = %d %s, want built-in flow without external hook", response.Code, response.Body)
	}
	for _, event := range store.ListAuditEvents() {
		for _, forbidden := range []string{"unsafe prompt sentinel", "provider-secret"} {
			if strings.Contains(event.AfterSnapshot, forbidden) {
				t.Fatalf("guardrail failure audit leaked %q: %s", forbidden, event.AfterSnapshot)
			}
		}
	}
}
