package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestGoogleSubscriptionDefaultAuthorizationPreservesClientBinding(t *testing.T) {
	for _, definition := range builtinSubscriptionDefinitions() {
		fingerprint := map[string]string{
			providerGeminiCLISubscription:   "20dee4e4587c70d034c6b57ef57887a9ab5014ede499dc33ec6b18a5ad229d5f",
			providerAntigravitySubscription: "0eba362d53c81721da20f4ab53266802b2e39aa8dad0af58956abd1b7c72e3ce",
		}[definition.providerType]
		if fingerprint == "" {
			continue
		}
		for _, method := range []string{"", "google", "google_project"} {
			t.Run(definition.providerType+"/method="+method, func(t *testing.T) {
				store := NewMemoryStore()
				server := New(store)
				path := "/api/admin/provider-actions/" + definition.providerType + "/"
				project := ""
				if method == "google_project" {
					project = "synthetic-project"
				}
				started := doJSON(t, server.Handler(), http.MethodPost, path+"oauth.start", map[string]any{"resource_type": definition.resourceType, "method_id": method, "inputs": map[string]string{"organization_id": project}}, "dev_admin_token")
				var start struct {
					Data struct {
						SessionID string `json:"session_id"`
						State     string `json:"state"`
						AuthURL   string `json:"auth_url"`
					} `json:"data"`
				}
				if started.Code != http.StatusOK || json.Unmarshal([]byte(started.Body), &start) != nil || start.Data.SessionID == "" {
					t.Fatalf("default Google sign-in failed: HTTP %d", started.Code)
				}
				session, ok, err := store.GetProviderAccountOAuthSessionByState(start.Data.State)
				if err != nil || !ok {
					t.Fatal("default OAuth session was not saved")
				}
				var secret subscriptionOAuthSecret
				if json.Unmarshal([]byte(session.CodeVerifier), &secret) != nil {
					t.Fatal("default OAuth session could not be decoded")
				}
				digest := sha256.Sum256([]byte(secret.ClientID + "\x00" + secret.ClientSecret))
				if hex.EncodeToString(digest[:]) != fingerprint || secret.OrganizationID != project {
					t.Fatal("default OAuth client does not match the selected desktop application")
				}
				authorize, err := url.Parse(start.Data.AuthURL)
				if err != nil || authorize.Host != "accounts.google.com" || authorize.Query().Get("client_id") != secret.ClientID || authorize.Query().Get("state") != start.Data.State || authorize.Query().Get("code_challenge") != openAICodeChallenge(secret.Verifier) || authorize.Query().Get("code_challenge_method") != "S256" || authorize.Query().Get("redirect_uri") != definition.redirectURI {
					t.Fatal("default Google sign-in lost its client, state, callback, or PKCE binding")
				}
				var stored providerAccountOAuthSessionRecord
				if err := store.db.Where("id = ?", session.ID).First(&stored).Error; err != nil {
					t.Fatal(err)
				}
				if strings.Contains(stored.CodeVerifierEncrypted, secret.ClientSecret) || strings.Contains(started.Body, secret.ClientSecret) {
					t.Fatal("default OAuth configuration leaked outside encrypted session storage")
				}
				var tokenCalls atomic.Int32
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/token":
						tokenCalls.Add(1)
						if err := r.ParseForm(); err != nil || r.Form.Get("client_id") != secret.ClientID || r.Form.Get("client_secret") != secret.ClientSecret {
							t.Error("token operation used a different desktop client")
						}
						if r.Form.Get("grant_type") == "authorization_code" {
							if r.Form.Get("code_verifier") != secret.Verifier || r.Form.Get("redirect_uri") != session.RedirectURI || r.Form.Get("code") != "synthetic-code" {
								t.Error("default code exchange lost its saved authorization binding")
							}
							_, _ = fmt.Fprint(w, `{"access_token":"synthetic-access","refresh_token":"synthetic-refresh","expires_in":3600}`)
						} else if r.Form.Get("grant_type") == "refresh_token" && r.Form.Get("refresh_token") == "synthetic-refresh" {
							_, _ = fmt.Fprint(w, `{"access_token":"synthetic-refreshed-access","expires_in":3600}`)
						} else {
							t.Error("unexpected default OAuth token operation")
							w.WriteHeader(http.StatusBadRequest)
						}
					case "/userinfo":
						_, _ = fmt.Fprint(w, `{"id":"synthetic-account","email":"synthetic@example.test","verified_email":true}`)
					default:
						w.WriteHeader(http.StatusServiceUnavailable)
					}
				}))
				defer upstream.Close()
				adapter, _ := resolveTypedAdapter[*GoogleSubscriptionAdapter](server.adapterRegistry, definition.providerType)
				adapter.Client, adapter.TokenURL, adapter.UserInfoURL, adapter.BaseURL, adapter.LoadURL = upstream.Client(), upstream.URL+"/token", upstream.URL+"/userinfo", upstream.URL, upstream.URL
				payload := map[string]any{"resource_type": definition.resourceType, "session_id": session.ID, "state": session.State, "code": "synthetic-code"}
				exchanged := doJSON(t, server.Handler(), http.MethodPost, path+"oauth.exchange", payload, "dev_admin_token")
				var result struct {
					Data subscriptionOAuthExchangeResult `json:"data"`
				}
				if exchanged.Code != http.StatusOK || json.Unmarshal([]byte(exchanged.Body), &result) != nil || result.Data.ClientID != secret.ClientID || result.Data.ClientSecret != secret.ClientSecret || result.Data.RefreshToken != "synthetic-refresh" || result.Data.OrganizationID != project {
					t.Fatal("default code exchange did not return its refreshable client configuration")
				}
				replay := doJSON(t, server.Handler(), http.MethodPost, path+"oauth.exchange", payload, "dev_admin_token")
				if replay.Code != http.StatusBadRequest {
					t.Fatal("default OAuth authorization code was reusable")
				}
				provider := store.AddProvider(Provider{Name: "Synthetic Google provider", Type: definition.providerType, Status: StatusActive, Options: map[string]string{providerCredentialRefreshProfileOption: definition.providerType + "_oauth"}})
				resource, err := store.AddProviderResource(ProviderResource{ProviderID: provider.ID, Name: "Synthetic Google account", ResourceType: definition.resourceType, Credentials: &ProviderResourceCredentials{AuthType: "oauth", ClientID: result.Data.ClientID, ClientSecret: result.Data.ClientSecret, AccessToken: result.Data.AccessToken, RefreshToken: result.Data.RefreshToken, ExpiresAt: result.Data.ExpiresAt}})
				if err != nil {
					t.Fatal(err)
				}
				store.ConfigureProviderCredentialRefreshHandlers(adapter.ProviderResourceCredentialRefreshHandlers())
				credentials, err := store.RefreshProviderResourceCredentials(context.Background(), resource.ID, true)
				if err != nil || credentials.ClientID != secret.ClientID || credentials.ClientSecret != secret.ClientSecret || credentials.AccessToken != "synthetic-refreshed-access" || tokenCalls.Load() != 2 {
					t.Fatalf("saved default configuration was not retained during refresh: failed=%t calls=%d client_match=%t refreshed=%t", err != nil, tokenCalls.Load(), credentials.ClientID == secret.ClientID && credentials.ClientSecret == secret.ClientSecret, credentials.AccessToken == "synthetic-refreshed-access")
				}
				var saved ProviderResource
				if err := store.db.First(&saved, "id = ?", resource.ID).Error; err != nil {
					t.Fatal(err)
				}
				if !strings.HasPrefix(saved.CredentialBlob, "enc:v1:") || strings.Contains(saved.CredentialBlob, secret.ClientSecret) {
					t.Fatal("default account configuration was not encrypted at rest")
				}
				audit, _ := json.Marshal(store.ListAuditEvents())
				listed, _ := json.Marshal(store.ListProviderResources())
				for _, sensitive := range []string{secret.ClientSecret, "synthetic-access", "synthetic-refresh", "synthetic-refreshed-access"} {
					if strings.Contains(string(audit), sensitive) || strings.Contains(string(listed), sensitive) {
						t.Fatal("default OAuth credentials leaked through audit or public account data")
					}
				}
			})
		}
	}
}

func TestGoogleSubscriptionAuthorizationRejectsIncompleteOverrides(t *testing.T) {
	for _, providerType := range []string{providerGeminiCLISubscription, providerAntigravitySubscription} {
		store := NewMemoryStore()
		server := New(store)
		path := "/api/admin/provider-actions/" + providerType + "/oauth.start"
		resourceType := strings.TrimSuffix(providerType, "_subscription") + "_account"
		for _, test := range []struct {
			name, method, clientID, clientSecret, errorCode string
		}{
			{name: "unknown method", method: "unknown", errorCode: "google_subscription_oauth_method_invalid"},
			{name: "empty custom client", method: "oauth_client"},
			{name: "missing custom secret", method: "oauth_client", clientID: "synthetic-client"},
			{name: "missing custom ID", method: "oauth_client", clientSecret: "synthetic-secret"},
			{name: "partial default override", method: "google", clientID: "synthetic-client"},
			{name: "partial legacy override", clientSecret: "synthetic-secret"},
			{name: "oversized client ID", clientID: strings.Repeat("x", 4097), clientSecret: "synthetic-secret"},
			{name: "oversized client secret", clientID: "synthetic-client", clientSecret: strings.Repeat("x", 16385)},
		} {
			t.Run(providerType+"/"+test.name, func(t *testing.T) {
				response := doJSON(t, server.Handler(), http.MethodPost, path, map[string]any{"resource_type": resourceType, "method_id": test.method, "inputs": map[string]string{"client_id": test.clientID, "client_secret": test.clientSecret}}, "dev_admin_token")
				code := firstNonEmpty(test.errorCode, "google_subscription_oauth_client_required")
				if response.Code != http.StatusBadRequest || !strings.Contains(response.Body, code) {
					t.Fatalf("incomplete or unsupported authorization configuration was accepted: HTTP %d", response.Code)
				}
			})
		}
		for _, method := range []string{"", "google", "oauth_client"} {
			t.Run(providerType+"/complete-override/"+method, func(t *testing.T) {
				response := doJSON(t, server.Handler(), http.MethodPost, path, map[string]any{"resource_type": resourceType, "method_id": method, "inputs": map[string]string{"client_id": " synthetic-client ", "client_secret": " synthetic-secret "}}, "dev_admin_token")
				var result struct {
					Data struct {
						State string `json:"state"`
					} `json:"data"`
				}
				if response.Code != http.StatusOK || json.Unmarshal([]byte(response.Body), &result) != nil {
					t.Fatalf("complete OAuth override failed: HTTP %d", response.Code)
				}
				session, ok, err := store.GetProviderAccountOAuthSessionByState(result.Data.State)
				if err != nil || !ok {
					t.Fatal("custom OAuth session was not saved")
				}
				var secret subscriptionOAuthSecret
				if json.Unmarshal([]byte(session.CodeVerifier), &secret) != nil || secret.ClientID != "synthetic-client" || secret.ClientSecret != "synthetic-secret" {
					t.Fatal("complete custom OAuth override was mixed with the desktop defaults")
				}
			})
		}
	}
}
