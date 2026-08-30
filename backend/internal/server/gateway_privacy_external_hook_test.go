package server

import (
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestExternalPrivacyHookFixtureMasksRequestBodyBeforeCompletion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("external privacy hook fixture uses POSIX sh")
	}
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
		PluginDir:  filepath.Join("..", "plugin", "testdata", "external-privacy-hook"),
	})

	response := doJSON(t, app.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "gpt-privacy",
		"messages": []map[string]any{
			{"role": "user", "content": "raw prompt sentinel"},
		},
	}, secret)
	if response.Code != http.StatusOK {
		t.Fatalf("privacy response = %d %s, want 200", response.Code, response.Body)
	}
	if strings.Contains(response.Body, "raw prompt sentinel") {
		t.Fatalf("privacy response leaked raw prompt: %s", response.Body)
	}
	if !strings.Contains(response.Body, "[masked-by-privacy]") {
		t.Fatalf("privacy response = %s, want masked output", response.Body)
	}
	var traceAudit string
	for _, event := range store.ListAuditEvents() {
		if event.Action == "plugin.gateway.privacy_pre" {
			traceAudit = event.AfterSnapshot
			break
		}
	}
	if traceAudit == "" {
		t.Fatalf("external privacy hook audit event was not recorded: %+v", store.ListAuditEvents())
	}
	for _, forbidden := range []string{"raw prompt sentinel", "provider-secret"} {
		if strings.Contains(traceAudit, forbidden) {
			t.Fatalf("privacy audit leaked %q: %s", forbidden, traceAudit)
		}
	}
}
