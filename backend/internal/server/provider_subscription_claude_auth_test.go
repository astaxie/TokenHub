package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

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
