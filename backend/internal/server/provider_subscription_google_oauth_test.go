package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestGoogleSubscriptionOAuthURLAndExchange(t *testing.T) {
	for _, providerType := range []string{providerGeminiCLISubscription, providerAntigravitySubscription} {
		t.Run(providerType, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/token":
					if err := r.ParseForm(); err != nil {
						t.Error(err)
					}
					if r.Form.Get("code") != "test-code" || r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code_verifier") != strings.Repeat("v", 43) || r.Form.Get("client_secret") != "synthetic-client-secret" || r.Form.Get("client_id") != "synthetic-client-id" {
						t.Error("invalid code exchange form")
					}
					writeJSON(w, 200, map[string]any{"access_token": "test-access", "refresh_token": "test-refresh", "expires_in": 3600, "scope": "test-scope"})
				case "/who":
					if r.Header.Get("Authorization") != "Bearer test-access" {
						t.Error("userinfo authorization missing")
					}
					writeJSON(w, 200, map[string]any{"id": "test-id", "email": "test@example.invalid", "verified_email": true})
				default:
					t.Error("unexpected OAuth request")
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			a := googleSubscriptionTestAdapter(server, providerType)
			a.UserInfoURL = server.URL + "/who"
			verifier := strings.Repeat("v", 43)
			auth, err := GoogleSubscriptionAuthorizeURL(providerType, a.DefaultOAuthRedirectURI(), "test-state", verifier, "synthetic-client-id")
			if err != nil {
				t.Fatal(err)
			}
			target, err := url.Parse(auth)
			if err != nil {
				t.Fatal(err)
			}
			if target.Host != "accounts.google.com" || target.Query().Get("client_id") != "synthetic-client-id" || target.Query().Get("state") != "test-state" || target.Query().Get("code_challenge") != openAICodeChallenge(verifier) || target.Query().Get("code_challenge_method") != "S256" || target.Query().Get("access_type") != "offline" {
				t.Fatal("invalid Google authorization URL")
			}
			credentials, err := a.ExchangeAuthorization(context.Background(), "test-code", verifier, a.DefaultOAuthRedirectURI(), "synthetic-client-id", "synthetic-client-secret")
			if err != nil {
				t.Fatal(err)
			}
			if credentials.Email != "test@example.invalid" || credentials.AccountID != "test-id" || credentials.RefreshToken != "test-refresh" || credentials.ExpiresAt == "" || credentials.ClientID != "synthetic-client-id" || credentials.ClientSecret != "synthetic-client-secret" {
				t.Fatalf("unexpected credentials: %+v", credentials)
			}
		})
	}
}

func TestGoogleSubscriptionOAuthRejectsUntrustedInputAndIdentity(t *testing.T) {
	for _, redirect := range []string{"https://example.com/callback", "http://localhost/callback?token=x", "http://user@localhost/callback", "http://127.0.0.1/callback#fragment"} {
		if _, err := GoogleSubscriptionAuthorizeURL(providerGeminiCLISubscription, redirect, "state", strings.Repeat("v", 43), "synthetic-client-id"); err == nil {
			t.Fatalf("accepted callback %s", redirect)
		}
	}
	for _, verifier := range []string{"short", strings.Repeat("v", 129), strings.Repeat("v", 42) + "!"} {
		if _, err := GoogleSubscriptionAuthorizeURL(providerGeminiCLISubscription, "http://localhost/callback", "state", verifier, "synthetic-client-id"); err == nil {
			t.Fatal("accepted malformed PKCE verifier")
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			writeJSON(w, 200, map[string]any{"access_token": "test-secret", "refresh_token": "refresh-secret", "expires_in": 3600})
			return
		}
		writeJSON(w, 200, map[string]any{"id": "test-id", "email": "test@example.invalid", "verified_email": false})
	}))
	defer server.Close()
	a := googleSubscriptionTestAdapter(server, providerGeminiCLISubscription)
	a.UserInfoURL = server.URL + "/who"
	credentials, err := a.ExchangeAuthorization(context.Background(), "test-code", strings.Repeat("v", 43), a.DefaultOAuthRedirectURI(), "synthetic-client-id", "synthetic-client-secret")
	if err == nil || credentials.AccessToken != "" || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unverified account exposed credentials: %+v %v", credentials, err)
	}
}

func TestGoogleSubscriptionRequiresExplicitOAuthClientCredentials(t *testing.T) {
	verifier := strings.Repeat("v", 43)
	for _, clientID := range []string{"", " ", strings.Repeat("x", 4097)} {
		if _, err := GoogleSubscriptionAuthorizeURL(providerGeminiCLISubscription, "http://localhost/callback", "state", verifier, clientID); AsHTTPError(err).Code != "google_subscription_oauth_client_required" {
			t.Fatal("authorization accepted a missing or invalid administrator client")
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("missing OAuth configuration reached the upstream")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	adapter := googleSubscriptionTestAdapter(server, providerGeminiCLISubscription)
	for _, credentials := range []ProviderResourceCredentials{{}, {ClientID: "synthetic-client-id"}, {ClientSecret: "synthetic-client-secret"}, {ClientID: "synthetic-client-id", ClientSecret: strings.Repeat("x", 16385)}} {
		if _, err := adapter.ExchangeAuthorization(context.Background(), "code", verifier, adapter.DefaultOAuthRedirectURI(), credentials.ClientID, credentials.ClientSecret); AsHTTPError(err).Code != "google_subscription_oauth_client_required" {
			t.Fatal("exchange accepted a missing or invalid administrator client")
		}
		credentials.RefreshToken = "synthetic-refresh"
		if _, err := adapter.refresh(context.Background(), credentials); AsHTTPError(err).Code != "google_subscription_oauth_client_required" {
			t.Fatal("refresh accepted a missing or invalid administrator client")
		}
	}
}
