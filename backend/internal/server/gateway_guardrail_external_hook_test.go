package server

import (
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestExternalGuardrailHookFixtureDeniesUnsafeCompletionOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("external guardrail hook fixture uses POSIX sh")
	}
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
		PluginDir:  filepath.Join("..", "plugin", "testdata", "external-guardrail-hook"),
	})

	response := doJSON(t, app.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-external-guardrail",
		"messages": []map[string]any{
			{"role": "user", "content": "unsafe prompt sentinel"},
		},
	}, secret)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body, "gateway_hook_denied") {
		t.Fatalf("guardrail response = %d %s, want 403 gateway_hook_denied", response.Code, response.Body)
	}
	var guardrailAudit string
	for _, event := range store.ListAuditEvents() {
		if event.Action == "plugin.gateway.guardrail_post" {
			guardrailAudit = event.AfterSnapshot
			break
		}
	}
	if guardrailAudit == "" {
		t.Fatalf("external guardrail hook audit event was not recorded: %+v", store.ListAuditEvents())
	}
	for _, forbidden := range []string{"unsafe prompt sentinel", "provider-secret"} {
		if strings.Contains(guardrailAudit, forbidden) {
			t.Fatalf("guardrail audit leaked %q: %s", forbidden, guardrailAudit)
		}
	}
}
