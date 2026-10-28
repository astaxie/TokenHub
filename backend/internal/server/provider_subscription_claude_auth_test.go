package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestSubscriptionCancellationBindsActorAndProvider(t *testing.T) {
	server := New(NewMemoryStore())
	started, err := subscriptionActionFixture(t, server, ProviderChatGPTSubscription, "oauth.start", "admin-a", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	data := started.Data.(map[string]any)
	payload := map[string]any{"session_id": data["session_id"], "state": data["state"]}
	for _, test := range []struct{ provider, actor string }{{ProviderChatGPTSubscription, "admin-b"}, {providerGeminiCLISubscription, "admin-a"}} {
		if _, err := subscriptionActionFixture(t, server, test.provider, "oauth.cancel", test.actor, payload); AsHTTPError(err).Code != "invalid_oauth_state" {
			t.Fatalf("unbound cancellation accepted: %v", err)
		}
	}
	if _, err := subscriptionActionFixture(t, server, ProviderChatGPTSubscription, "oauth.cancel", "admin-a", payload); err != nil {
		t.Fatal(err)
	}
	payload["code"] = "code"
	payload["client_id"] = "client"
	if _, err := subscriptionActionFixture(t, server, ProviderChatGPTSubscription, "oauth.exchange", "admin-a", payload); AsHTTPError(err).Code != "invalid_oauth_state" {
		t.Fatalf("cancelled session exchanged: %v", err)
	}
}

func TestClaudeSubscriptionImportsTokenAndSessionThroughAdminActions(t *testing.T) {
	server := New(NewMemoryStore())
	start := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/provider-actions/claude_subscription/oauth.start", map[string]any{"resource_type": providerResourceClaudeSubscription, "inputs": map[string]string{"key": "claude-import-secret"}}, "dev_admin_token")
	if start.Code != http.StatusOK || strings.Contains(start.Body, "claude-import-secret") {
		t.Fatalf("unsafe or failed import start: status=%d body=%s", start.Code, start.Body)
	}
	var result struct {
		Data map[string]any `json:"data"`
	}
	if json.Unmarshal([]byte(start.Body), &result) != nil {
		t.Fatal("invalid start response")
	}
	payload := map[string]any{"resource_type": providerResourceClaudeSubscription, "session_id": result.Data["session_id"], "state": result.Data["state"]}
	encoded, _ := json.Marshal(payload)
	if _, err := server.pluginActions.Execute(context.Background(), pluginmeta.ActionInvocation{PluginID: "tokenhub.provider.claude-subscription", ActionID: "claude_subscription.oauth.exchange", Actor: pluginmeta.ActionActor{ID: "different-admin"}, Payload: encoded}); AsHTTPError(err).Code != "invalid_oauth_state" {
		t.Fatalf("other actor read imported token: %v", err)
	}
	exchanged := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/provider-actions/claude_subscription/oauth.exchange", payload, "dev_admin_token")
	if exchanged.Code != http.StatusOK || !strings.Contains(exchanged.Body, `"access_token":"claude-import-secret"`) || !strings.Contains(exchanged.Body, `"catalog"`) {
		t.Fatalf("token exchange missing credentials or catalog: %s", exchanged.Body)
	}
	replay := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/provider-actions/claude_subscription/oauth.exchange", payload, "dev_admin_token")
	if replay.Code != http.StatusBadRequest {
		t.Fatal("token import was replayed")
	}
	session := `{"claudeAiOauth":{"accessToken":"imported-access","refreshToken":"imported-refresh"}}`
	imported := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/provider-actions/claude_subscription/oauth.exchange", map[string]any{"resource_type": providerResourceClaudeSubscription, "mode": "import", "session_json": session}, "dev_admin_token")
	if imported.Code != http.StatusOK || !strings.Contains(imported.Body, "imported-refresh") || !strings.Contains(imported.Body, `"catalog"`) {
		t.Fatalf("session import failed: %s", imported.Body)
	}
	events, _ := json.Marshal(server.store.ListAuditEvents())
	if strings.Contains(string(events), "claude-import-secret") || strings.Contains(string(events), "imported-refresh") {
		t.Fatal("import credentials leaked into audit events")
	}
}

func TestClaudeBrowserAuthorizationRunsThroughProviderActions(t *testing.T) {
	server := New(NewMemoryStore())
	adapter, ok := resolveTypedAdapter[*ClaudeSubscriptionAdapter](server.adapterRegistry, providerClaudeSubscription)
	if !ok {
		t.Fatal("Claude adapter is unavailable")
	}
	adapter.Executable = os.Args[0]
	adapter.ExecutableArgs = []string{"-test.run=^TestClaudeSignInHelperProcess$", "--", "tokenhub-claude-signin-fixture", "code"}
	t.Cleanup(adapter.Close)
	start := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/provider-actions/claude_subscription/oauth.start", map[string]any{"resource_type": providerResourceClaudeSubscription, "method_id": "browser"}, "dev_admin_token")
	if start.Code != http.StatusOK {
		t.Fatalf("browser authorization could not start: %s", start.Body)
	}
	var started struct {
		Data map[string]any `json:"data"`
	}
	if json.Unmarshal([]byte(start.Body), &started) != nil || started.Data["auth_url"] == nil {
		t.Fatal("authorization URL was not returned")
	}
	payload := map[string]any{"resource_type": providerResourceClaudeSubscription, "session_id": started.Data["session_id"], "state": started.Data["state"], "callback_url": "fixture-code#fixture-cli-state"}
	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/provider-actions/claude_subscription/oauth.exchange", payload, "dev_admin_token")
	if response.Code != http.StatusOK {
		t.Fatalf("pasted code was rejected: %s", response.Body)
	}
	delete(payload, "callback_url")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(response.Body, `"session_json"`) {
		time.Sleep(10 * time.Millisecond)
		response = doJSON(t, server.Handler(), http.MethodPost, "/api/admin/provider-actions/claude_subscription/oauth.exchange", payload, "dev_admin_token")
	}
	if response.Code != http.StatusOK || !strings.Contains(response.Body, "fixture-refresh") || !strings.Contains(response.Body, `"catalog"`) {
		t.Fatalf("CLI credentials were not delivered: %s", response.Body)
	}
	events, _ := json.Marshal(server.store.ListAuditEvents())
	if strings.Contains(string(events), "fixture-refresh") || strings.Contains(string(events), "fixture-code") {
		t.Fatal("CLI authorization secrets leaked into audit events")
	}
}
