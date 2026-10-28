package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSubscriptionSessionRefreshSummaryEnablesRenewalWithoutExposingTokens(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{ID: "grok-refresh-summary", Name: "Grok", Type: "grok_subscription", Status: StatusActive})
	session := `{"type":"oauth","access":"synthetic-summary-access","refresh":"synthetic-summary-refresh"}`
	resource, err := store.AddProviderResource(ProviderResource{Name: "Renewable Grok account", ProviderID: provider.ID,
		ResourceType: "grok_account", Credentials: &ProviderResourceCredentials{SessionJSON: session}})
	if err != nil {
		t.Fatal(err)
	}
	if resource.CredentialSummary["has_refresh_token"] != "true" {
		t.Fatalf("renewable subscription account hides token renewal: %+v", resource.CredentialSummary)
	}
	// Accounts saved before session summaries were supported stored this false.
	resource.Options["has_refresh_token"] = "false"
	if err := updateExistingProviderResourceColumns(store.db, &resource, "options"); err != nil {
		t.Fatal(err)
	}
	rotated := `{"type":"oauth","access":"synthetic-summary-rotated","refresh":"synthetic-summary-next"}`
	if err := store.ReplaceProviderSession(context.Background(), resource.ID, provider.Type, session, rotated); err != nil {
		t.Fatal(err)
	}
	listed, err := json.Marshal(store.ListProviderResources())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(listed), "synthetic-summary-") || strings.Contains(string(listed), "session_json") || !strings.Contains(string(listed), `"has_refresh_token":"true"`) {
		t.Fatal("refresh summary was lost or exposed session credentials after rotation")
	}
	resource, err = store.UpdateProviderResource(resource.ID, ProviderResource{Credentials: &ProviderResourceCredentials{
		Replace: true, SessionJSON: `{"type":"oauth","access":"replacement-without-refresh"}`}})
	if err != nil {
		t.Fatal(err)
	}
	if resource.CredentialSummary["has_refresh_token"] != "false" {
		t.Fatalf("replacement retained the prior account's renewal option: %+v", resource.CredentialSummary)
	}
}

func TestSubscriptionSessionRefreshSummaryRequiresRenewableOAuthCredentials(t *testing.T) {
	for _, resourceType := range []string{"cursor_account", "zcode_account", "zed_account", "unknown_account"} {
		if subscriptionWorkerSessionHasRefreshToken(resourceType, `{"type":"oauth","refresh":"identity-metadata"}`) {
			t.Fatalf("adapter without a refresh hook advertised renewal: %s", resourceType)
		}
	}
	for _, session := range []string{
		`{"type":"api","key":"synthetic-key","refresh":"unused"}`,
		`{"type":"oauth","access":"synthetic-access"}`,
		`{"type":"oauth","refresh":"  "}`,
		`{"type":"oauth","refresh":{"token":"invalid-shape"}}`,
		`{"type":"oauth","refresh":null}`,
		`null`, `not-json`,
	} {
		if subscriptionWorkerSessionHasRefreshToken("grok_account", session) {
			t.Fatal("nonrenewable credentials advertised token renewal")
		}
	}
	for _, definition := range subscriptionWorkerDefinitions() {
		options := map[string]string{}
		applyProviderAccountOptions(options, definition.resourceType(), ProviderResourceCredentials{SessionJSON: `{"type":"oauth","access":"synthetic-access","refresh":"synthetic-refresh"}`})
		if (options["has_refresh_token"] == "true") != subscriptionWorkerHasRefresh(definition.ID) {
			t.Fatalf("provider %s refresh summary does not match its runtime capability", definition.ID)
		}
	}
}
