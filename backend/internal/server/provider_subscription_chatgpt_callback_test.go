package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func chatGPTCallbackFixture(t *testing.T, publicBase string, automatic bool) (*Server, *MemoryStore, map[string]any) {
	t.Helper()
	store := NewMemoryStore()
	server := NewWithConfig(store, Config{PublicBaseURL: publicBase})
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	started, err := subscriptionActionFixture(t, server, ProviderChatGPTSubscription, "oauth.start", "administrator", map[string]any{"automatic_callback": automatic})
	if err != nil {
		t.Fatal(err)
	}
	return server, store, started.Data.(map[string]any)
}

func chatGPTCallbackRequest(server *Server, data map[string]any, callback url.Values) *httptest.ResponseRecorder {
	query := url.Values{"state": {data["state"].(string)}}
	for key, values := range callback {
		query[key] = values
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/auth/callback?"+query.Encode(), nil))
	return recorder
}

func TestChatGPTSubscriptionAutomaticCallbackCompletesAuthenticatedPoll(t *testing.T) {
	server, store, data := chatGPTCallbackFixture(t, "http://127.0.0.1:8080", true)
	if data["flow"] != "auto" || data["callback_mode"] != "none" || data["poll_interval"] != 2 || data["redirect_uri"] != "http://127.0.0.1:8080/auth/callback" {
		t.Fatalf("local authorization did not advertise automatic callback: %v", data)
	}
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
			_, _ = fmt.Fprint(w, `{"models":[{"slug":"gpt-live","visibility":"list"}]}`)
			return
		}
		exchanges.Add(1)
		if err := r.ParseForm(); err != nil || r.Form.Get("code") != "private-code" || r.Form.Get("client_id") != "dynamic-client" || r.Form.Get("code_verifier") != secret.Verifier || r.Form.Get("redirect_uri") != session.RedirectURI {
			t.Error("automatic exchange lost the captured callback or PKCE binding")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "private-access", "refresh_token": "private-refresh", "id_token": idToken, "scope": chatGPTSubscriptionDirectScope, "expires_in": 3600})
	}))
	t.Cleanup(upstream.Close)
	adapter, _ := resolveTypedAdapter[*ChatGPTSubscriptionAdapter](server.adapterRegistry, ProviderChatGPTSubscription)
	adapter.TokenURL, adapter.CredentialRefreshClient = upstream.URL, upstream.Client()
	adapter.APIBaseURL, adapter.Client = upstream.URL, upstream.Client()
	payload := map[string]any{"session_id": data["session_id"], "state": data["state"], "code": ""}
	before, err := subscriptionActionFixture(t, server, ProviderChatGPTSubscription, "oauth.exchange", "administrator", payload)
	if err != nil || before.Data.(map[string]any)["status"] != "pending" || exchanges.Load() != 0 {
		t.Fatalf("pending callback reached token endpoint: %v", err)
	}
	callback := chatGPTCallbackRequest(server, data, url.Values{"code": {"private-code"}, "client_id": {"dynamic-client"}})
	if callback.Code != http.StatusOK || !strings.Contains(callback.Header().Get("Content-Type"), "text/html") || callback.Header().Get("Cache-Control") != "no-store" || callback.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("callback did not return a private confirmation page: %d", callback.Code)
	}
	if strings.Contains(callback.Body.String(), "private-") || strings.Contains(callback.Body.String(), data["state"].(string)) || exchanges.Load() != 0 {
		t.Fatal("unauthenticated callback exposed credentials or exchanged tokens")
	}
	var record providerAccountOAuthSessionRecord
	if err := store.db.First(&record, "id = ?", session.ID).Error; err != nil || !strings.HasPrefix(record.CodeVerifierEncrypted, "enc:v1:") || strings.Contains(record.CodeVerifierEncrypted, "private-code") {
		t.Fatal("captured authorization code was not encrypted")
	}
	for _, attempt := range []struct{ provider, actor string }{{ProviderChatGPTSubscription, "other-administrator"}, {providerGeminiCLISubscription, "administrator"}} {
		if _, err := subscriptionActionFixture(t, server, attempt.provider, "oauth.exchange", attempt.actor, payload); AsHTTPError(err).Code != "invalid_oauth_state" {
			t.Fatalf("callback crossed its actor/provider binding: %v", err)
		}
	}
	result, err := subscriptionActionFixture(t, server, ProviderChatGPTSubscription, "oauth.exchange", "administrator", payload)
	if err != nil {
		t.Fatal(err)
	}
	credentials := result.Data.(subscriptionOAuthExchangeResult)
	if credentials.AccessToken != "private-access" || credentials.ClientID != "dynamic-client" || credentials.Catalog == nil || exchanges.Load() != 1 {
		t.Fatal("authenticated callback poll omitted exchanged credentials/catalog")
	}
	if replay := chatGPTCallbackRequest(server, data, url.Values{"code": {"another-code"}, "client_id": {"another-client"}}); replay.Code == http.StatusOK {
		t.Fatal("consumed callback was replayed")
	}
	if _, err := subscriptionActionFixture(t, server, ProviderChatGPTSubscription, "oauth.exchange", "administrator", payload); AsHTTPError(err).Code != "invalid_oauth_state" || exchanges.Load() != 1 {
		t.Fatal("callback poll exchanged a consumed code again")
	}
}
