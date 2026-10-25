package server

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestProviderCredentialReplacementDoesNotRetainPreviousAccount(t *testing.T) {
	for _, test := range []struct {
		name, providerType, resourceType string
		original, replacement            ProviderResourceCredentials
	}{
		{
			name: "Claude session to setup token", providerType: providerClaudeSubscription, resourceType: providerResourceClaudeSubscription,
			original:    ProviderResourceCredentials{AuthType: "oauth", SessionJSON: `{"claudeAiOauth":{"accessToken":"old-session-access","refreshToken":"old-session-refresh"}}`, AccountID: "old-account", Email: "old@example.invalid"},
			replacement: ProviderResourceCredentials{AuthType: "oauth", AccessToken: "new-setup-token"},
		},
		{
			name: "Claude setup token to session", providerType: providerClaudeSubscription, resourceType: providerResourceClaudeSubscription,
			original:    ProviderResourceCredentials{AuthType: "oauth", AccessToken: "old-setup-token", AccountID: "old-account"},
			replacement: ProviderResourceCredentials{AuthType: "oauth", SessionJSON: `{"claudeAiOauth":{"accessToken":"new-session-access"}}`},
		},
		{
			name: "Google account with a different client", providerType: providerGeminiCLISubscription, resourceType: "gemini_cli_account",
			original:    ProviderResourceCredentials{AuthType: "oauth", AccessToken: "old-access", RefreshToken: "old-refresh", IDToken: "old-id", ClientID: "old-client", ClientSecret: "old-secret", Scopes: "old-scope", ExpiresAt: "2020-01-01T00:00:00Z", AccountID: "old-account", UserID: "old-user", Email: "old@example.invalid", OrganizationID: "old-project", PlanType: "old-plan"},
			replacement: ProviderResourceCredentials{AuthType: "oauth", AccessToken: "new-access", ClientID: "new-client", ClientSecret: "new-secret", ExpiresAt: "2099-01-01T00:00:00Z"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := NewMemoryStore()
			provider := store.AddProvider(Provider{Name: test.name, Type: test.providerType, Status: StatusActive})
			resource, err := store.AddProviderResource(ProviderResource{ProviderID: provider.ID, Name: "Account replacement", ResourceType: test.resourceType, Credentials: &test.original, Options: map[string]string{"scopes": test.original.Scopes, "label": "kept"}})
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(test.replacement)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			if err := json.Unmarshal(encoded, &fields); err != nil {
				t.Fatal(err)
			}
			fields["replace"] = true
			body, err := json.Marshal(map[string]any{"credentials": fields})
			if err != nil {
				t.Fatal(err)
			}
			var patch ProviderResource
			if err := json.Unmarshal(body, &patch); err != nil {
				t.Fatal(err)
			}
			updated, err := store.UpdateProviderResource(resource.ID, patch)
			if err != nil {
				t.Fatal(err)
			}
			credentials, err := store.RefreshProviderResourceCredentials(context.Background(), resource.ID, false)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(credentials, test.replacement) {
				t.Fatal("Replacing an account retained stale credentials or operation metadata")
			}
			if test.original.SessionJSON != "" {
				err := store.ReplaceProviderSession(context.Background(), resource.ID, test.providerType, test.original.SessionJSON, `{"claudeAiOauth":{"accessToken":"late-rotation"}}`)
				if err == nil || AsHTTPError(err).Code != "provider_credentials_changed" {
					t.Fatal("Late session rotation overwrote the replacement account")
				}
			}
			if test.providerType == providerClaudeSubscription && test.replacement.AccessToken != "" {
				environment := strings.Join(claudeSubscriptionEnv("isolated-home", "isolated-config", credentials), "\n")
				if !strings.Contains(environment, "CLAUDE_CODE_OAUTH_TOKEN="+test.replacement.AccessToken) {
					t.Fatal("Claude ignored the newly authorized setup token")
				}
			}
			if updated.Options["label"] != "kept" {
				t.Fatal("Credential replacement discarded unrelated resource options")
			}
			public, err := json.Marshal(updated)
			if err != nil || strings.Contains(string(public), "old-") || strings.Contains(string(public), "old@example.invalid") {
				t.Fatal("Credential replacement retained the old account summary")
			}
			var stored ProviderResource
			if err := store.db.First(&stored, "id = ?", resource.ID).Error; err != nil {
				t.Fatal(err)
			}
			if test.replacement.AccessToken == "" && stored.APIKey != "" {
				t.Fatal("Session replacement retained the previous access token")
			}
			if strings.Contains(store.decryptSecret(stored.CredentialBlob), `"replace"`) {
				t.Fatal("Credential replacement command was persisted")
			}
		})
	}
}

func TestProviderCredentialPartialUpdateKeepsOmittedAuthentication(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{Name: "Partial credentials", Type: providerGeminiCLISubscription, Status: StatusActive})
	original := ProviderResourceCredentials{AuthType: "oauth", AccessToken: "original-access", RefreshToken: "original-refresh", ClientID: "original-client", ClientSecret: "original-secret", AccountID: "original-account"}
	resource, err := store.AddProviderResource(ProviderResource{ProviderID: provider.ID, Name: "Partial account", ResourceType: "gemini_cli_account", Credentials: &original})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateProviderResource(resource.ID, ProviderResource{Credentials: &ProviderResourceCredentials{ClientSecret: "replacement-secret"}}); err != nil {
		t.Fatal(err)
	}
	credentials, err := store.RefreshProviderResourceCredentials(context.Background(), resource.ID, false)
	original.ClientSecret = "replacement-secret"
	if err != nil || !reflect.DeepEqual(credentials, original) {
		t.Fatal("A partial credential update discarded omitted authentication")
	}
}

func TestProviderCredentialReplacementRejectsMissingAuthentication(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{Name: "Invalid replacement", Type: providerClaudeSubscription, Status: StatusActive})
	resource, err := store.AddProviderResource(ProviderResource{ProviderID: provider.ID, Name: "Valid account", ResourceType: providerResourceClaudeSubscription, Credentials: &ProviderResourceCredentials{AccessToken: "retained-token"}})
	if err != nil {
		t.Fatal(err)
	}
	var patch ProviderResource
	if err := json.Unmarshal([]byte(`{"credentials":{"replace":true,"client_id":"new-client"}}`), &patch); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateProviderResource(resource.ID, patch); err == nil || AsHTTPError(err).Code != "provider_credentials_required" {
		t.Fatalf("Missing replacement authentication was accepted: %v", err)
	}
	credentials, err := store.RefreshProviderResourceCredentials(context.Background(), resource.ID, false)
	if err != nil || credentials.AccessToken != "retained-token" || credentials.ClientID != "" {
		t.Fatal("Invalid replacement changed the stored account")
	}
}
