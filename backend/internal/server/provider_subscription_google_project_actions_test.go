package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestGoogleSubscriptionAuthorizationProjectInputs(t *testing.T) {
	for _, definition := range builtinSubscriptionDefinitions() {
		if definition.providerType != providerGeminiCLISubscription && definition.providerType != providerAntigravitySubscription {
			continue
		}
		for _, test := range []struct {
			name, input, legacy, wanted string
		}{
			{name: "project prompt", input: " explicit-project ", wanted: "explicit-project"},
			{name: "legacy field", legacy: " legacy-project ", wanted: "legacy-project"},
			{name: "explicit prompt overrides legacy", input: "new-project", legacy: "old-project", wanted: "new-project"},
			{name: "blank prompt preserves legacy", input: "  ", legacy: "legacy-project", wanted: "legacy-project"},
			{name: "automatic project discovery"},
		} {
			t.Run(definition.providerType+"/"+test.name, func(t *testing.T) {
				store := NewMemoryStore()
				server := New(store)
				response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/provider-actions/"+definition.providerType+"/oauth.start", map[string]any{
					"resource_type": definition.resourceType, "organization_id": test.legacy,
					"inputs": map[string]string{"client_id": "synthetic-client", "client_secret": "synthetic-secret", "organization_id": test.input},
				}, "dev_admin_token")
				var started struct {
					Data struct {
						State string `json:"state"`
					} `json:"data"`
				}
				if response.Code != http.StatusOK || json.Unmarshal([]byte(response.Body), &started) != nil {
					t.Fatalf("OAuth start failed: %s", response.Body)
				}
				session, ok, err := store.GetProviderAccountOAuthSessionByState(started.Data.State)
				if err != nil || !ok {
					t.Fatalf("OAuth session not found: %v", err)
				}
				var secret subscriptionOAuthSecret
				if err := json.Unmarshal([]byte(session.CodeVerifier), &secret); err != nil {
					t.Fatal(err)
				}
				if secret.OrganizationID != test.wanted {
					t.Fatalf("OAuth project = %q, want %q", secret.OrganizationID, test.wanted)
				}
			})
		}
	}
}
