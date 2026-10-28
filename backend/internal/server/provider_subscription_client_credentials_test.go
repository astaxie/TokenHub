package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSubscriptionOAuthClientSecretIsEncryptedAndRedacted(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{Name: "Configured OAuth", Type: providerGeminiCLISubscription, Status: StatusActive})
	resource, err := store.AddProviderResource(ProviderResource{ProviderID: provider.ID, Name: "Configured account", ResourceType: "gemini_cli_account", Credentials: &ProviderResourceCredentials{AccessToken: "synthetic-access", ClientID: "synthetic-client-id", ClientSecret: "synthetic-client-secret"}, Options: map[string]string{"client_secret": "plaintext-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, update := range []func() error{
		func() error { return nil },
		func() error {
			_, err := store.UpdateProviderResource(resource.ID, ProviderResource{Options: map[string]string{"client_secret": "plaintext-secret"}})
			return err
		},
		func() error {
			_, err := store.UpdateProviderResourceOptions(resource.ID, map[string]string{"client_secret": "plaintext-secret"})
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
		if !strings.HasPrefix(stored.CredentialBlob, "enc:v1:") || strings.Contains(stored.CredentialBlob, "synthetic-") || stored.Options["client_secret"] != "" {
			t.Fatal("OAuth client secret bypassed encrypted storage")
		}
		credentials, err := store.RefreshProviderResourceCredentials(context.Background(), resource.ID, false)
		if err != nil || credentials.ClientSecret != "synthetic-client-secret" || credentials.ClientID != "synthetic-client-id" {
			t.Fatal("OAuth client credentials did not survive storage and options updates")
		}
		listed, err := json.Marshal(store.ListProviderResources())
		if err != nil || strings.Contains(string(listed), "client_secret") || strings.Contains(string(listed), "synthetic-client-secret") {
			t.Fatal("Public resource response disclosed OAuth client credentials")
		}
	}
	for _, key := range []string{"client_secret", "ClientSecret"} {
		payload := map[string]any{"credentials": map[string]any{key: "synthetic-client-secret"}}
		audit, _ := auditPayloadBody(payload)
		public, _ := json.Marshal(sanitizePluginActionValue(payload))
		if strings.Contains(audit, "synthetic-client-secret") || strings.Contains(string(public), "synthetic-client-secret") {
			t.Fatal("OAuth client secret leaked through audit or generic action output")
		}
	}
}

func TestSubscriptionOAuthClientSecretEditWinsOverLateRefresh(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{Name: "Configured refresh", Type: providerGeminiCLISubscription, Status: StatusActive, Options: map[string]string{providerCredentialRefreshProfileOption: "synthetic-oauth"}})
	resource, err := store.AddProviderResource(ProviderResource{ProviderID: provider.ID, Name: "Refresh account", ResourceType: "gemini_cli_account", Credentials: &ProviderResourceCredentials{AccessToken: "original-access", RefreshToken: "original-refresh", ClientID: "synthetic-client-id", ClientSecret: "original-client-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	store.ConfigureProviderCredentialRefreshHandlers([]providerResourceCredentialRefreshRegistration{{ProviderType: provider.Type, Profile: "synthetic-oauth", Refresh: func(_ context.Context, credentials ProviderResourceCredentials) (ProviderResourceCredentials, error) {
		_, err := store.UpdateProviderResource(resource.ID, ProviderResource{Credentials: &ProviderResourceCredentials{ClientSecret: "replacement-client-secret"}})
		credentials.AccessToken = "stale-refreshed-access"
		return credentials, err
	}}})
	credentials, err := store.RefreshProviderResourceCredentials(context.Background(), resource.ID, true)
	if err != nil || credentials.ClientSecret != "replacement-client-secret" || credentials.AccessToken != "original-access" {
		t.Fatal("Late refresh overwrote an OAuth client secret edit")
	}
}
