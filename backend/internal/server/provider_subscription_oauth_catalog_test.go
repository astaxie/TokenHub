package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSubscriptionGoogleExchangeIncludesLiveCatalog(t *testing.T) {
	for _, providerType := range []string{providerGeminiCLISubscription, providerAntigravitySubscription} {
		t.Run(providerType, func(t *testing.T) {
			server := New(NewMemoryStore())
			var modelRequests atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/token":
					if err := r.ParseForm(); err != nil || r.Form.Get("client_id") != "synthetic-google-client" || r.Form.Get("client_secret") != "synthetic-google-secret" {
						t.Error("authorization exchange did not use the supplied OAuth client")
					}
					_, _ = fmt.Fprint(w, `{"access_token":"google-access","refresh_token":"google-refresh","expires_in":3600}`)
				case "/userinfo":
					_, _ = fmt.Fprint(w, `{"id":"google-account","email":"synthetic@example.test","verified_email":true}`)
				case "/v1internal:loadCodeAssist":
					_, _ = fmt.Fprint(w, `{"currentTier":{"id":"standard-tier"}}`)
				case "/v1internal:listExperiments":
					_, _ = fmt.Fprint(w, `{"flags":[]}`)
				case "/v1internal:retrieveUserQuota", "/v1internal:fetchAvailableModels":
					modelRequests.Add(1)
					if r.Header.Get("Authorization") != "Bearer google-access" {
						t.Error("model discovery did not use the exchanged token")
					}
					if providerType == providerGeminiCLISubscription {
						_, _ = fmt.Fprint(w, `{"buckets":[{"modelId":"gemini-live"}]}`)
					} else {
						_, _ = fmt.Fprint(w, `{"models":{"gemini-live":{"displayName":"Live Gemini"}}}`)
					}
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer upstream.Close()
			adapter, _ := resolveTypedAdapter[*GoogleSubscriptionAdapter](server.adapterRegistry, providerType)
			adapter.TokenURL, adapter.UserInfoURL, adapter.BaseURL, adapter.Client = upstream.URL+"/token", upstream.URL+"/userinfo", upstream.URL, upstream.Client()
			adapter.LoadURL = upstream.URL
			started, err := subscriptionActionFixture(t, server, providerType, "oauth.start", "administrator", map[string]any{"organization_id": "synthetic-project", "inputs": map[string]string{"client_id": "synthetic-google-client", "client_secret": "synthetic-google-secret"}})
			if err != nil {
				t.Fatal(err)
			}
			start := started.Data.(map[string]any)
			result, err := subscriptionActionFixture(t, server, providerType, "oauth.exchange", "administrator", map[string]any{"session_id": start["session_id"], "state": start["state"], "code": "synthetic-code"})
			if err != nil {
				t.Fatal(err)
			}
			data := result.Data.(subscriptionOAuthExchangeResult)
			if data.AccessToken != "google-access" || data.RefreshToken != "google-refresh" || data.ClientID != "synthetic-google-client" || data.ClientSecret != "synthetic-google-secret" || data.OrganizationID != "synthetic-project" || data.CatalogError != "" || data.Status != "complete" {
				t.Fatal("exchange did not preserve authorized account credentials")
			}
			wantModels, wantFirstModel := 1, "gemini-live"
			if providerType == providerGeminiCLISubscription {
				wantModels, wantFirstModel = 5, "gemini-2.5-flash"
			}
			if data.Catalog == nil || len(data.Catalog.Models) != wantModels || data.Catalog.Models[0].ID != wantFirstModel || modelRequests.Load() != 1 {
				t.Fatal("exchange did not include the live account model catalog")
			}
			encoded, err := json.Marshal(data)
			if err != nil {
				t.Fatal(err)
			}
			var wire map[string]any
			if json.Unmarshal(encoded, &wire) != nil || wire["access_token"] != "google-access" || wire["catalog"] == nil {
				t.Fatal("wire result did not retain top-level credentials and catalog")
			}
		})
	}
}

func TestSubscriptionCatalogFailurePreservesRotatedCredentials(t *testing.T) {
	for _, providerType := range []string{ProviderChatGPTSubscription, providerGeminiCLISubscription, providerAntigravitySubscription} {
		t.Run(providerType, func(t *testing.T) {
			server := New(NewMemoryStore())
			var refreshes, modelRequests atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if googleSubscriptionTestProject(w, r) {
					return
				}
				if r.URL.Path == "/token" {
					refreshes.Add(1)
					if err := r.ParseForm(); err != nil || r.Form.Get("refresh_token") != "original-refresh" {
						t.Error("catalog refresh did not use the exchanged refresh token")
					}
					_, _ = fmt.Fprint(w, `{"access_token":"rotated-access","refresh_token":"rotated-refresh","expires_in":3600,"scope":"chatgpt.tokens.use.direct"}`)
					return
				}
				if modelRequests.Add(1) == 1 && providerType == ProviderChatGPTSubscription {
					if r.Header.Get("Authorization") != "Bearer original-access" {
						t.Error("initial catalog request did not use the exchanged access token")
					}
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				if r.Header.Get("Authorization") != "Bearer rotated-access" {
					t.Error("catalog request did not use rotated credentials")
				}
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = fmt.Fprint(w, `{"error":"rotated-refresh original-refresh rotated-access"}`)
			}))
			defer upstream.Close()
			credentials := ProviderResourceCredentials{AccessToken: "original-access", RefreshToken: "original-refresh", ExpiresAt: time.Now().Add(time.Second).UTC().Format(time.RFC3339), ClientID: "dynamic-client", OrganizationID: "synthetic-project"}
			if providerType == ProviderChatGPTSubscription {
				credentials.ExpiresAt = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
				adapter, _ := resolveTypedAdapter[*ChatGPTSubscriptionAdapter](server.adapterRegistry, providerType)
				adapter.APIBaseURL, adapter.TokenURL, adapter.Client, adapter.CredentialRefreshClient = upstream.URL, upstream.URL+"/token", upstream.Client(), upstream.Client()
			} else {
				adapter, _ := resolveTypedAdapter[*GoogleSubscriptionAdapter](server.adapterRegistry, providerType)
				adapter.BaseURL, adapter.TokenURL, adapter.Client = upstream.URL, upstream.URL+"/token", upstream.Client()
				adapter.LoadURL = upstream.URL
				credentials.ClientID, credentials.ClientSecret = "synthetic-google-client", "synthetic-google-secret"
			}
			var definition builtinSubscriptionDefinition
			for _, item := range builtinSubscriptionDefinitions() {
				if item.providerType == providerType {
					definition = item
				}
			}
			result := server.subscriptionOAuthExchangeCatalog(context.Background(), definition, credentials)
			if result.AccessToken != "rotated-access" || result.RefreshToken != "rotated-refresh" || result.Catalog != nil || result.Status != "complete" {
				t.Fatal("catalog failure discarded the rotated account credentials")
			}
			wantModelRequests := int32(1)
			if providerType == ProviderChatGPTSubscription {
				wantModelRequests = 2
			}
			if result.CatalogError != "subscription_models_unavailable" || strings.Contains(result.CatalogError, "refresh") || refreshes.Load() != 1 || modelRequests.Load() != wantModelRequests {
				t.Fatalf("catalog failure did not produce a bounded safe warning: %q", result.CatalogError)
			}
		})
	}
}
