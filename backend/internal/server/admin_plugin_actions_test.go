package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestAdminPluginActionExecutesThroughBroker(t *testing.T) {
	store := NewMemoryStore()
	server := NewWithConfig(store, Config{AdminToken: "plugin-action-admin"})
	if err := server.pluginActions.Register(pluginmeta.ActionDescriptor{
		PluginID: "tokenhub.provider.openai-codex",
		ActionID: "test.echo",
		Kind:     pluginmeta.ActionKindTest,
	}, pluginmeta.ActionHandlerFunc(func(_ context.Context, invocation pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
		var payload map[string]string
		if err := json.Unmarshal(invocation.Payload, &payload); err != nil {
			t.Fatalf("decode action payload: %v", err)
		}
		return pluginmeta.ActionResult{Data: map[string]string{
			"actor_id":    invocation.Actor.ID,
			"resource_id": payload["resource_id"],
		}}, nil
	})); err != nil {
		t.Fatalf("register plugin action: %v", err)
	}

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/tokenhub.provider.openai-codex/actions/test.echo", map[string]any{
		"resource_id": "res_codex",
	}, "plugin-action-admin")
	if response.Code != http.StatusOK {
		t.Fatalf("POST plugin action: expected 200, got %d: %s", response.Code, response.Body)
	}
	body := response.Body
	if !strings.Contains(body, `"resource_id":"res_codex"`) || !strings.Contains(body, `"actor_id":"dev_admin"`) {
		t.Fatalf("POST plugin action response did not include broker result: %s", body)
	}
	events := store.ListAuditEvents()
	if len(events) == 0 || events[0].Action != "plugin.action.test.echo" || events[0].ResourceID != "tokenhub.provider.openai-codex" {
		t.Fatalf("plugin action audit events = %+v", events)
	}
}

func TestAdminPluginActionsListsBuiltInActions(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "plugin-action-admin"})

	response := doJSON(t, server.Handler(), http.MethodGet, "/api/admin/plugin-actions", nil, "plugin-action-admin")
	if response.Code != http.StatusOK {
		t.Fatalf("GET plugin actions: expected 200, got %d: %s", response.Code, response.Body)
	}
	body := response.Body
	if !strings.Contains(body, `"action_id":"openai_codex.quota.read"`) ||
		!strings.Contains(body, `"action_id":"openai_codex.oauth.start"`) ||
		!strings.Contains(body, `"action_id":"openai_codex.oauth.exchange"`) {
		t.Fatalf("GET plugin actions did not include built-in Codex actions: %s", body)
	}
}

func TestAdminPluginActionStartsOpenAICodexOAuth(t *testing.T) {
	store := NewMemoryStore()
	server := NewWithConfig(store, Config{
		AdminToken:         "plugin-action-admin",
		CORSAllowedOrigins: []string{"http://localhost:3001"},
	})

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/tokenhub.provider.openai-codex/actions/openai_codex.oauth.start", map[string]any{
		"return_url": "http://localhost:3001/providers",
	}, "plugin-action-admin")
	if response.Code != http.StatusOK {
		t.Fatalf("POST OAuth action: expected 200, got %d: %s", response.Code, response.Body)
	}
	var result struct {
		Data        providerAccountOAuthGenerateResponse `json:"data"`
		RedirectURL string                               `json:"redirect_url"`
	}
	if err := json.Unmarshal([]byte(response.Body), &result); err != nil {
		t.Fatal(err)
	}
	if result.Data.AuthURL == "" || result.RedirectURL != result.Data.AuthURL || result.Data.SessionID == "" || result.Data.State == "" {
		t.Fatalf("unexpected OAuth action result: %+v", result)
	}
	authURL, err := url.Parse(result.Data.AuthURL)
	if err != nil {
		t.Fatal(err)
	}
	if authURL.Query().Get("client_id") != openAIAccountOAuthClientID || authURL.Query().Get("state") != result.Data.State {
		t.Fatalf("unexpected OAuth action auth URL: %s", result.Data.AuthURL)
	}
	if events := store.ListAuditEvents(); len(events) == 0 || events[0].Action != "plugin.action.openai_codex.oauth.start" {
		t.Fatalf("plugin action audit events = %+v", events)
	}
}

func TestAdminPluginActionsLoadsExternalManifestActions(t *testing.T) {
	pluginRoot := t.TempDir()
	pluginDir := filepath.Join(pluginRoot, "sync")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.yaml"), []byte(`
schema_version: 1
id: tokenhub.sync
name: Sync Plugin
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - extension
placement:
  - management_action
capabilities:
  actions:
    - id: sync.run
      kind: mutate
      title: Run sync
`), 0o644); err != nil {
		t.Fatal(err)
	}
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "plugin-action-admin", PluginDir: pluginRoot})

	response := doJSON(t, server.Handler(), http.MethodGet, "/api/admin/plugin-actions", nil, "plugin-action-admin")
	if response.Code != http.StatusOK {
		t.Fatalf("GET plugin actions: expected 200, got %d: %s", response.Code, response.Body)
	}
	if !strings.Contains(response.Body, `"plugin_id":"tokenhub.sync"`) || !strings.Contains(response.Body, `"action_id":"sync.run"`) {
		t.Fatalf("GET plugin actions did not include external manifest action: %s", response.Body)
	}

	execute := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/tokenhub.sync/actions/sync.run", map[string]any{}, "plugin-action-admin")
	assertResponseBodyJSONError(t, execute, http.StatusNotImplemented, "plugin_action_unavailable")
}

func TestAdminPluginActionRejectsUnknownPlugin(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "plugin-action-admin"})

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/tokenhub.missing/actions/test.echo", map[string]any{}, "plugin-action-admin")
	assertResponseBodyJSONError(t, response, http.StatusNotFound, "plugin_not_found")
}

func TestAdminPluginActionRejectsUnknownAction(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "plugin-action-admin"})

	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/plugins/tokenhub.provider.openai-codex/actions/missing", map[string]any{}, "plugin-action-admin")
	assertResponseBodyJSONError(t, response, http.StatusNotFound, "plugin_action_not_found")
}

func assertResponseBodyJSONError(t *testing.T, response responseBody, wantStatus int, wantCode string) {
	t.Helper()
	if response.Code != wantStatus {
		t.Fatalf("expected %d, got %d: %s", wantStatus, response.Code, response.Body)
	}
	if !strings.Contains(response.Body, `"code":"`+wantCode+`"`) {
		t.Fatalf("response body = %s, want code %s", response.Body, wantCode)
	}
}
