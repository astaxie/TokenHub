package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGoogleSubscriptionActionsRequireAndProtectCustomClientCredentials(t *testing.T) {
	for _, definition := range builtinSubscriptionDefinitions() {
		if definition.providerType != providerGeminiCLISubscription && definition.providerType != providerAntigravitySubscription {
			continue
		}
		t.Run(definition.providerType, func(t *testing.T) {
			store := NewMemoryStore()
			server := New(store)
			path := "/api/admin/provider-actions/" + definition.providerType + "/"
			methods := doJSON(t, server.Handler(), http.MethodPost, path+"oauth.methods", map[string]any{"resource_type": definition.resourceType}, "dev_admin_token")
			if methods.Code != http.StatusOK || !strings.Contains(methods.Body, `"key":"client_secret"`) || !strings.Contains(methods.Body, `"key":"client_id"`) || !strings.Contains(methods.Body, `"key":"organization_id"`) {
				t.Fatalf("Google OAuth client prompts are unavailable: %s", methods.Body)
			}
			var catalog struct {
				Data struct {
					Methods []struct {
						ID      string           `json:"id"`
						Prompts []map[string]any `json:"prompts"`
					} `json:"methods"`
				} `json:"data"`
			}
			if json.Unmarshal([]byte(methods.Body), &catalog) != nil || len(catalog.Data.Methods) != 3 || catalog.Data.Methods[0].ID != "google" || len(catalog.Data.Methods[0].Prompts) != 0 || catalog.Data.Methods[1].ID != "google_project" || len(catalog.Data.Methods[1].Prompts) != 1 || catalog.Data.Methods[2].ID != "oauth_client" || len(catalog.Data.Methods[2].Prompts) != 3 {
				t.Fatal("Google sign-in should default to a prompt-free account method and retain custom configuration")
			}
			missing := doJSON(t, server.Handler(), http.MethodPost, path+"oauth.start", map[string]any{"resource_type": definition.resourceType, "method_id": "oauth_client"}, "dev_admin_token")
			if missing.Code != http.StatusBadRequest || !strings.Contains(missing.Body, "google_subscription_oauth_client_required") {
				t.Fatal("Google authorization accepted a missing OAuth client")
			}
			inputs := map[string]string{"client_id": "synthetic-action-client", "client_secret": "synthetic-action-client-secret", "organization_id": "synthetic-project"}
			started := doJSON(t, server.Handler(), http.MethodPost, path+"oauth.start", map[string]any{"resource_type": definition.resourceType, "inputs": inputs}, "dev_admin_token")
			if started.Code != http.StatusOK || strings.Contains(started.Body, inputs["client_secret"]) {
				t.Fatalf("OAuth start leaked the client secret or failed: %s", started.Body)
			}
			var start struct {
				Data struct {
					SessionID string `json:"session_id"`
					State     string `json:"state"`
				} `json:"data"`
			}
			if json.Unmarshal([]byte(started.Body), &start) != nil || start.Data.SessionID == "" {
				t.Fatal("invalid OAuth start result")
			}
			var stored providerAccountOAuthSessionRecord
			if err := store.db.Where("id = ?", start.Data.SessionID).First(&stored).Error; err != nil {
				t.Fatal(err)
			}
			if strings.Contains(stored.CodeVerifierEncrypted, inputs["client_secret"]) || strings.Contains(stored.CodeVerifierEncrypted, inputs["client_id"]) {
				t.Fatal("OAuth client credentials were stored without encryption")
			}
			payload := map[string]any{"resource_type": definition.resourceType, "session_id": start.Data.SessionID, "state": start.Data.State, "code": "synthetic-code", "client_id": "different-client"}
			mismatch := doJSON(t, server.Handler(), http.MethodPost, path+"oauth.exchange", payload, "dev_admin_token")
			if mismatch.Code != http.StatusBadRequest || !strings.Contains(mismatch.Body, "subscription_oauth_client_mismatch") {
				t.Fatal("OAuth exchange did not bind the authorized client")
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/token":
					if err := r.ParseForm(); err != nil || r.Form.Get("client_id") != inputs["client_id"] || r.Form.Get("client_secret") != inputs["client_secret"] {
						t.Error("OAuth exchange lost the encrypted client credentials")
					}
					_, _ = fmt.Fprint(w, `{"access_token":"synthetic-access","refresh_token":"synthetic-refresh","expires_in":3600}`)
				case "/userinfo":
					_, _ = fmt.Fprint(w, `{"id":"synthetic-account","email":"synthetic@example.test","verified_email":true}`)
				default:
					w.WriteHeader(http.StatusServiceUnavailable)
				}
			}))
			defer upstream.Close()
			adapter, _ := resolveTypedAdapter[*GoogleSubscriptionAdapter](server.adapterRegistry, definition.providerType)
			adapter.Client, adapter.TokenURL, adapter.UserInfoURL, adapter.BaseURL = upstream.Client(), upstream.URL+"/token", upstream.URL+"/userinfo", upstream.URL
			adapter.LoadURL = upstream.URL
			delete(payload, "client_id")
			exchanged := doJSON(t, server.Handler(), http.MethodPost, path+"oauth.exchange", payload, "dev_admin_token")
			if exchanged.Code != http.StatusOK || !strings.Contains(exchanged.Body, `"client_secret":"`+inputs["client_secret"]+`"`) || !strings.Contains(exchanged.Body, `"access_token":"synthetic-access"`) || !strings.Contains(exchanged.Body, `"organization_id":"synthetic-project"`) {
				t.Fatalf("authorized exchange lost the reusable credentials: %s", exchanged.Body)
			}
			events, err := json.Marshal(store.ListAuditEvents())
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{inputs["client_secret"], "synthetic-access", "synthetic-refresh"} {
				if strings.Contains(string(events), secret) {
					t.Fatal("OAuth credentials leaked into audit events")
				}
			}
		})
	}
}
