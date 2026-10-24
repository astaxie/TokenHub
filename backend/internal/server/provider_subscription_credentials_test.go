package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestSubscriptionSessionIsEncryptedAndNeverListed(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{ID: "prv_session", Name: "Session", Type: "cursor_subscription", Status: StatusActive})
	session := `{"type":"oauth","refresh":"synthetic-refresh-secret","access":"synthetic-access-secret"}`
	resource, err := store.AddProviderResource(ProviderResource{ID: "rsrc_session", Name: "Session Account", ProviderID: provider.ID, ResourceType: "cursor_account", Credentials: &ProviderResourceCredentials{SessionJSON: session}, Options: map[string]string{"session_json": session}})
	if err != nil {
		t.Fatal(err)
	}
	var stored ProviderResource
	if err := store.db.First(&stored, "id = ?", resource.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stored.CredentialBlob, "enc:v1:") || strings.Contains(stored.CredentialBlob, "synthetic-") {
		t.Fatal("session must use encrypted credential storage")
	}
	if stored.Options["session_json"] != "" {
		t.Fatal("session leaked into public options")
	}
	credentials, err := store.RefreshProviderResourceCredentials(context.Background(), resource.ID, false)
	if err != nil || credentials.SessionJSON != session {
		t.Fatalf("session did not round trip: %v", err)
	}
	listed, err := json.Marshal(store.ListProviderResources())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(listed), "synthetic-") || strings.Contains(string(listed), "session_json") {
		t.Fatal("listed resource leaked its session")
	}
}

func TestSubscriptionSessionRotationDoesNotOverwriteAccountEdits(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{ID: "prv_session_cas", Name: "Session CAS", Type: "cursor_subscription", Status: StatusActive})
	original := `{"type":"oauth","access":"original"}`
	rotated := `{"type":"oauth","access":"rotated"}`
	resource, err := store.AddProviderResource(ProviderResource{ID: "rsrc_session_cas", Name: "CAS Account", ProviderID: provider.ID, ResourceType: "cursor_account", Credentials: &ProviderResourceCredentials{SessionJSON: original}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceProviderSession(context.Background(), resource.ID, provider.Type, original, rotated); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceProviderSession(context.Background(), resource.ID, provider.Type, original, `{"type":"oauth","access":"stale"}`); err == nil {
		t.Fatal("stale rotation was accepted")
	}
	if err := store.ReplaceProviderSession(context.Background(), resource.ID, "another_subscription", rotated, original); err == nil {
		t.Fatal("cross-provider rotation was accepted")
	}
	credentials, err := store.RefreshProviderResourceCredentials(context.Background(), resource.ID, false)
	if err != nil || credentials.SessionJSON != rotated {
		t.Fatal("rotation changed after rejected updates")
	}
}

func TestSubscriptionSessionOptionsCannotBypassEncryptedStorage(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{ID: "prv_session_options", Name: "Session options", Type: "cursor_subscription", Status: StatusActive})
	session := `{"type":"oauth","access":"encrypted-session-secret"}`
	resource, err := store.AddProviderResource(ProviderResource{ID: "rsrc_session_options", Name: "Session options account", ProviderID: provider.ID, ResourceType: "cursor_account", Credentials: &ProviderResourceCredentials{SessionJSON: session}})
	if err != nil {
		t.Fatal(err)
	}
	for _, update := range []func() error{
		func() error {
			_, err := store.UpdateProviderResource(resource.ID, ProviderResource{Options: map[string]string{"session_json": "plaintext-secret", "label": "kept"}})
			return err
		},
		func() error {
			_, err := store.UpdateProviderResourceOptions(resource.ID, map[string]string{"session_json": "plaintext-secret"})
			return err
		},
	} {
		if err := update(); err != nil {
			t.Fatal(err)
		}
		var stored ProviderResource
		if err := store.db.First(&stored, "id = ?", resource.ID).Error; err != nil {
			t.Fatal(err)
		}
		if stored.Options["session_json"] != "" {
			t.Fatal("Plaintext session persisted through options update")
		}
		credentials, err := store.RefreshProviderResourceCredentials(context.Background(), resource.ID, false)
		if err != nil || credentials.SessionJSON != session {
			t.Fatal("Options-only update modified encrypted session")
		}
	}
}

func TestSubscriptionSessionAdminEditWinsOverLateRotation(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{ID: "prv_session_edit", Name: "Session edit", Type: "cursor_subscription", Status: StatusActive})
	original := `{"type":"oauth","access":"original-session"}`
	replacement := `{"type":"oauth","access":"administrator-session"}`
	resource, err := store.AddProviderResource(ProviderResource{ID: "rsrc_session_edit", Name: "Session edit account", ProviderID: provider.ID, ResourceType: "cursor_account", Credentials: &ProviderResourceCredentials{SessionJSON: original}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateProviderResource(resource.ID, ProviderResource{Credentials: &ProviderResourceCredentials{SessionJSON: replacement}}); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceProviderSession(context.Background(), resource.ID, provider.Type, original, `{"type":"oauth","access":"late-rotation"}`); err == nil {
		t.Fatal("Late refresh overwrote administrator credentials")
	}
	credentials, err := store.RefreshProviderResourceCredentials(context.Background(), resource.ID, false)
	if err != nil || credentials.SessionJSON != replacement {
		t.Fatal("Administrator session was not retained")
	}
}

func TestSubscriptionSessionRedactionCoversAuditAndGenericActions(t *testing.T) {
	secret := `{"type":"oauth","access":"sensitive-session"}`
	for _, key := range []string{"session_json", "SessionJSON"} {
		payload := map[string]any{"nested": map[string]any{key: secret}}
		audit, _ := auditPayloadBody(payload)
		if strings.Contains(audit, "sensitive-session") || !strings.Contains(audit, "[redacted]") {
			t.Fatalf("Session leaked into audit: %s", audit)
		}
		encoded, err := json.Marshal(sanitizePluginActionValue(payload))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "sensitive-session") || !strings.Contains(string(encoded), "[redacted]") {
			t.Fatalf("Session leaked from generic plugin result: %s", encoded)
		}
	}
	metadata := sanitizeAdminPluginActionMetadata(map[string]string{"subscription_session_json": "true"})
	if metadata["subscription_session_json"] != "true" {
		t.Fatal("Public login-flow metadata was redacted")
	}
	for _, capability := range []string{"oauth.methods", "oauth.cancel"} {
		if got, ok := providerAdapterCapabilityForActionCapability(capability); !ok || got != AdapterCapabilityOAuth {
			t.Fatalf("Provider login capability unavailable: %s", capability)
		}
	}
}

func TestSubscriptionSessionLegacyOptionsAreNotReturned(t *testing.T) {
	resource := ProviderResource{ResourceType: "cursor_account", Options: map[string]string{"session_json": "legacy-plaintext-secret", "label": "kept"}}
	redactProviderResourceSecrets(&resource)
	if resource.Options["session_json"] != "" || resource.Options["label"] != "kept" {
		t.Fatalf("Invalid public resource options: %#v", resource.Options)
	}
}

func TestSubscriptionSessionRejectsMalformedStoredPayloads(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{ID: "prv_session_invalid", Name: "Session invalid", Type: "cursor_subscription", Status: StatusActive})
	for _, session := range []string{`null`, `[]`, `{}`, `not-json`, strings.Repeat("x", (1<<20)+1)} {
		_, err := store.AddProviderResource(ProviderResource{ID: NewID("rsrc"), Name: NewID("invalid-session"), ProviderID: provider.ID, ResourceType: "cursor_account", Credentials: &ProviderResourceCredentials{SessionJSON: session}})
		if err == nil {
			t.Fatal("Accepted malformed provider session")
		}
	}
	if err := store.ReplaceProviderSession(context.Background(), "missing", provider.Type, "", `null`); AsHTTPError(err).Code != "provider_session_invalid" {
		t.Fatalf("Malformed rotation was not rejected: %v", err)
	}
}

func TestSubscriptionSessionHTTPExchangeUsesSecretPolicyAndNoStore(t *testing.T) {
	store := NewMemoryStore()
	server := NewWithConfig(store, Config{AdminToken: "session-admin"})
	defer func() { _ = server.Shutdown(context.Background()) }()
	const providerType = "session_test_subscription"
	const pluginID = "tokenhub.provider.session-test"
	if err := server.adapterRegistry.RegisterPlugin(pluginmeta.BuiltInProvider(pluginID, "Session test", []string{providerType}, []string{string(AdapterCapabilityOAuth)}), AdapterRegistration{Type: providerType, Adapter: MockAdapter{}, Capabilities: []AdapterCapability{AdapterCapabilityOAuth}}); err != nil {
		t.Fatal(err)
	}
	if err := server.pluginActions.Register(pluginmeta.ActionDescriptor{PluginID: pluginID, ActionID: "session.oauth.exchange", Kind: pluginmeta.ActionKindMutate, Capability: "oauth.exchange", Subject: providerType, Metadata: map[string]string{"result_secret_policy": "provider_account_credentials"}}, pluginmeta.ActionHandlerFunc(func(context.Context, pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
		return pluginmeta.ActionResult{Data: map[string]any{"session_json": `{"access":"http-session-secret"}`, "catalog": map[string]any{"models": []any{}}}}, nil
	})); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path   string
		secret bool
	}{
		{"/api/admin/provider-actions/" + providerType + "/oauth.exchange", true},
		{"/api/admin/plugins/" + pluginID + "/actions/session.oauth.exchange", false},
	} {
		request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(`{}`))
		request.Header.Set("Authorization", "Bearer session-admin")
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("Exchange failed: %d %s", response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "http-session-secret") != test.secret {
			t.Fatalf("Wrong credential disclosure policy: %s", response.Body.String())
		}
		if test.secret && response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("Credential response can be cached")
		}
	}
	events, err := json.Marshal(store.ListAuditEvents())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(events), "http-session-secret") {
		t.Fatal("Exchange credential leaked into audit")
	}
}
