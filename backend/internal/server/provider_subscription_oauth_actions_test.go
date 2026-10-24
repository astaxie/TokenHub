package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func subscriptionActionFixture(t *testing.T, server *Server, providerType, capability, actor string, payload map[string]any) (pluginmeta.ActionResult, error) {
	t.Helper()
	var definition builtinSubscriptionDefinition
	for _, item := range builtinSubscriptionDefinitions() {
		if item.providerType == providerType {
			definition = item
		}
	}
	if definition.providerType == "" {
		t.Fatalf("unknown subscription provider %s", providerType)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return server.pluginActions.Execute(context.Background(), pluginmeta.ActionInvocation{PluginID: definition.pluginID, ActionID: providerType + "." + capability, Actor: pluginmeta.ActionActor{ID: actor, Role: "admin"}, Payload: encoded})
}

func TestSubscriptionActionsAdvertiseResourcePolicyAndAuthorization(t *testing.T) {
	server := New(NewMemoryStore())
	for _, definition := range builtinSubscriptionDefinitions() {
		descriptor, ok := server.adapterRegistry.Describe(definition.providerType)
		if !ok || descriptor.ProviderPolicy.CredentialsScope != providerCredentialsScopeResource || !descriptor.ProviderPolicy.RouteRequiresResource || descriptor.ProviderPolicy.SupportsCustomHeaders {
			t.Fatalf("unsafe subscription policy for %s: %+v", definition.providerType, descriptor.ProviderPolicy)
		}
		start, ok := server.pluginActions.Describe(definition.pluginID, definition.providerType+".oauth.start")
		if !ok || start.Metadata["subscription_auth"] != "true" || start.Metadata["provider_resource_type"] != definition.resourceType {
			t.Fatalf("missing subscription authorization descriptor: %+v", start)
		}
		exchange, ok := server.pluginActions.Describe(definition.pluginID, definition.providerType+".oauth.exchange")
		if !ok || exchange.Metadata["result_secret_policy"] != "provider_account_credentials" {
			t.Fatal("account token exchange has no explicit secret policy")
		}
	}
}

func TestSubscriptionAuthorizationBindsActorProviderAndCallback(t *testing.T) {
	store := NewMemoryStore()
	server := New(store)
	for _, providerType := range []string{ProviderChatGPTSubscription, providerGeminiCLISubscription, providerAntigravitySubscription} {
		t.Run(providerType, func(t *testing.T) {
			started, err := subscriptionActionFixture(t, server, providerType, "oauth.start", "administrator", map[string]any{"inputs": map[string]string{"client_id": "synthetic-google-client", "client_secret": "synthetic-google-secret"}})
			if err != nil {
				t.Fatal(err)
			}
			data := started.Data.(map[string]any)
			if data["flow"] != "authorization_code" || data["auth_url"] == "" {
				t.Fatalf("invalid start result: %v", data)
			}
			payload := map[string]any{"session_id": data["session_id"], "state": data["state"], "code": "code", "client_id": "dynamic-client"}
			if _, err := subscriptionActionFixture(t, server, providerType, "oauth.exchange", "other-administrator", payload); AsHTTPError(err).Code != "invalid_oauth_state" {
				t.Fatalf("other actor accepted: %v", err)
			}
			otherProvider := ProviderGitHubCopilotSubscription
			if _, err := subscriptionActionFixture(t, server, otherProvider, "oauth.exchange", "administrator", payload); AsHTTPError(err).Code != "invalid_oauth_state" {
				t.Fatalf("other provider accepted: %v", err)
			}
			payload["redirect_uri"] = "http://127.0.0.1:9999/wrong"
			if _, err := subscriptionActionFixture(t, server, providerType, "oauth.exchange", "administrator", payload); AsHTTPError(err).Code != "subscription_oauth_redirect_mismatch" {
				t.Fatalf("other callback accepted: %v", err)
			}
			session, ok, err := store.GetProviderAccountOAuthSessionByState(data["state"].(string))
			if err != nil || !ok {
				t.Fatalf("invalid callback consumed valid session: %v", err)
			}
			var secret subscriptionOAuthSecret
			if err := json.Unmarshal([]byte(session.CodeVerifier), &secret); err != nil {
				t.Fatal(err)
			}
			resultJSON, _ := json.Marshal(started)
			if strings.Contains(string(resultJSON), secret.Verifier) || strings.Contains(string(resultJSON), `"device_code"`) {
				t.Fatal("authorization secret escaped into start result")
			}
			secret.ExpiresAt = time.Now().Add(-time.Minute)
			raw, _ := json.Marshal(secret)
			session.CodeVerifier = string(raw)
			if err := store.SaveProviderAccountOAuthSession(session); err != nil {
				t.Fatal(err)
			}
			delete(payload, "redirect_uri")
			if _, err := subscriptionActionFixture(t, server, providerType, "oauth.exchange", "administrator", payload); AsHTTPError(err).Code != "invalid_oauth_state" {
				t.Fatalf("expired session accepted: %v", err)
			}
		})
	}
}

func TestSubscriptionChatGPTExchangeIsSingleUse(t *testing.T) {
	store := NewMemoryStore()
	server := New(store)
	started, err := subscriptionActionFixture(t, server, ProviderChatGPTSubscription, "oauth.start", "administrator", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	data := started.Data.(map[string]any)
	session, ok, err := store.GetProviderAccountOAuthSessionByState(data["state"].(string))
	if err != nil || !ok {
		t.Fatal("authorization session missing")
	}
	var secret subscriptionOAuthSecret
	if err := json.Unmarshal([]byte(session.CodeVerifier), &secret); err != nil {
		t.Fatal(err)
	}
	claims, _ := json.Marshal(map[string]any{"iss": "https://auth.openai.com", "aud": "dynamic-client", "nonce": secret.Nonce, "exp": time.Now().Add(time.Hour).Unix(), "sub": "account-user"})
	idToken := "header." + base64.RawURLEncoding.EncodeToString(claims) + ".signature"
	var exchanges atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			if r.Header.Get("Authorization") != "Bearer access-secret" {
				t.Error("model discovery did not use the exchanged token")
			}
			_, _ = fmt.Fprint(w, `{"models":[{"slug":"gpt-live","visibility":"list"}]}`)
			return
		}
		exchanges.Add(1)
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Form.Get("code_verifier") != secret.Verifier || r.Form.Get("redirect_uri") != session.RedirectURI {
			t.Error("authorization exchange lost session binding")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access-secret", "refresh_token": "refresh-secret", "id_token": idToken, "scope": chatGPTSubscriptionDirectScope, "expires_in": 3600})
	}))
	defer upstream.Close()
	adapter, _ := resolveTypedAdapter[*ChatGPTSubscriptionAdapter](server.adapterRegistry, ProviderChatGPTSubscription)
	adapter.TokenURL, adapter.CredentialRefreshClient = upstream.URL, upstream.Client()
	adapter.APIBaseURL, adapter.Client = upstream.URL, upstream.Client()
	payload := map[string]any{"session_id": data["session_id"], "state": data["state"], "code": "code", "client_id": "dynamic-client"}
	result, err := subscriptionActionFixture(t, server, ProviderChatGPTSubscription, "oauth.exchange", "administrator", payload)
	if err != nil {
		t.Fatal(err)
	}
	credentials := result.Data.(subscriptionOAuthExchangeResult)
	if credentials.AccessToken != "access-secret" || credentials.RefreshToken != "refresh-secret" || credentials.ClientID != "dynamic-client" {
		t.Fatalf("exchange omitted account credential fields: %+v", credentials)
	}
	if credentials.Catalog == nil || len(credentials.Catalog.Models) != 1 || credentials.Catalog.Models[0].ID != "gpt-live" {
		t.Fatal("exchange did not return the live account model catalog")
	}
	if _, err := subscriptionActionFixture(t, server, ProviderChatGPTSubscription, "oauth.exchange", "administrator", payload); AsHTTPError(err).Code != "invalid_oauth_state" {
		t.Fatalf("authorization replay accepted: %v", err)
	}
	if exchanges.Load() != 1 {
		t.Fatalf("authorization exchanged %d times", exchanges.Load())
	}
}

func TestSubscriptionCopilotDevicePollingIsBoundedAndSingleUse(t *testing.T) {
	store := NewMemoryStore()
	server := New(store)
	var polls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			_, _ = fmt.Fprint(w, `{"device_code":"private-device-code","user_code":"ABCD-1234","expires_in":900,"interval":5}`)
			return
		}
		if r.URL.Path == "/token" {
			if r.Header.Get("Authorization") != "token github-access" {
				t.Error("session exchange did not use the authorized GitHub token")
			}
			_, _ = fmt.Fprintf(w, `{"token":"copilot-session","expires_at":%d}`, time.Now().Add(time.Hour).Unix())
			return
		}
		if r.URL.Path == "/models" {
			_, _ = fmt.Fprint(w, `{"data":[{"id":"copilot-live"}]}`)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Form.Get("device_code") != "private-device-code" {
			t.Error("wrong device code")
		}
		switch polls.Add(1) {
		case 1:
			_, _ = fmt.Fprint(w, `{"error":"authorization_pending"}`)
		case 2:
			_, _ = fmt.Fprint(w, `{"error":"slow_down"}`)
		default:
			_, _ = fmt.Fprint(w, `{"access_token":"github-access","scope":"read:user"}`)
		}
	}))
	defer upstream.Close()
	adapter, _ := resolveTypedAdapter[*CopilotSubscriptionAdapter](server.adapterRegistry, ProviderGitHubCopilotSubscription)
	adapter.DeviceURL, adapter.DeviceTokenURL, adapter.CredentialRefreshClient = upstream.URL+"/start", upstream.URL+"/poll", upstream.Client()
	adapter.TokenURL, adapter.APIBaseURL, adapter.Client = upstream.URL+"/token", upstream.URL, upstream.Client()
	started, err := subscriptionActionFixture(t, server, ProviderGitHubCopilotSubscription, "oauth.start", "administrator", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	data := started.Data.(map[string]any)
	raw, _ := json.Marshal(data)
	if data["flow"] != "device_code" || data["user_code"] != "ABCD-1234" || strings.Contains(string(raw), "private-device-code") {
		t.Fatalf("unsafe device start response: %s", raw)
	}
	payload := map[string]any{"session_id": data["session_id"], "state": data["state"]}
	exchange := func() (pluginmeta.ActionResult, error) {
		return subscriptionActionFixture(t, server, ProviderGitHubCopilotSubscription, "oauth.exchange", "administrator", payload)
	}
	early, err := exchange()
	if err != nil || early.Data.(map[string]any)["status"] != "pending" || polls.Load() != 0 {
		t.Fatalf("early poll reached GitHub: %v", err)
	}
	allowPoll := func() {
		session, ok, err := store.GetProviderAccountOAuthSessionByState(data["state"].(string))
		if err != nil || !ok {
			t.Fatal("device session missing")
		}
		var secret subscriptionOAuthSecret
		if err := json.Unmarshal([]byte(session.CodeVerifier), &secret); err != nil {
			t.Fatal(err)
		}
		secret.NextPollAt = time.Now().Add(-time.Second)
		encoded, _ := json.Marshal(secret)
		session.CodeVerifier = string(encoded)
		if err := store.SaveProviderAccountOAuthSession(session); err != nil {
			t.Fatal(err)
		}
	}
	for _, status := range []string{"pending", "slow_down"} {
		allowPoll()
		result, err := exchange()
		if err != nil || result.Data.(map[string]any)["status"] != status {
			t.Fatalf("poll status %s failed: %v", status, err)
		}
		if status == "slow_down" && result.Data.(map[string]any)["poll_interval"] != 10 {
			t.Fatal("slow_down did not add five seconds")
		}
		before := polls.Load()
		if _, err := exchange(); err != nil || polls.Load() != before {
			t.Fatal("poll interval was not enforced")
		}
	}
	allowPoll()
	result, err := exchange()
	if err != nil || result.Data.(subscriptionOAuthExchangeResult).AccessToken != "github-access" {
		t.Fatalf("device exchange failed: %v", err)
	}
	if catalog := result.Data.(subscriptionOAuthExchangeResult).Catalog; catalog == nil || len(catalog.Models) != 1 || catalog.Models[0].ID != "copilot-live" {
		t.Fatal("device exchange did not return the live account model catalog")
	}
	if _, err := exchange(); AsHTTPError(err).Code != "invalid_oauth_state" {
		t.Fatalf("completed device session replayed: %v", err)
	}
}
