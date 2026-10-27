package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestChatGPTSubscriptionRegistrationRejectsCallbackReplacement(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		server, store, initial := chatGPTCallbackFixture(t, "http://127.0.0.1:8080", automatic)
		registrationID := initial["registration_id"].(string)
		secret := chatGPTRegistrationSecret(t, store, initial)
		if err := store.bindChatGPTSubscriptionRegistration(context.Background(), "administrator", registrationID, secret.HostID, "bound-client", "bound-subject"); err != nil {
			t.Fatal(err)
		}
		data := chatGPTRegistrationStart(t, server, map[string]any{"automatic_callback": automatic, "registration_id": registrationID})
		if automatic {
			response := chatGPTCallbackRequest(server, data, url.Values{"code": {"private-code"}, "client_id": {"other-client"}})
			if response.Code != http.StatusBadRequest || strings.Contains(response.Body.String(), "other-client") {
				t.Fatal("automatic callback accepted or exposed a replacement client ID")
			}
		} else {
			payload := map[string]any{"session_id": data["session_id"], "state": data["state"], "code": "private-code", "client_id": "other-client"}
			if _, err := subscriptionActionFixture(t, server, ProviderChatGPTSubscription, "oauth.exchange", "administrator", payload); AsHTTPError(err).Code != "subscription_oauth_client_mismatch" {
				t.Fatal("manual callback accepted a replacement client ID")
			}
		}
		if _, exists, err := store.GetProviderAccountOAuthSessionByState(data["state"].(string)); err != nil || !exists {
			t.Fatal("mismatched callback consumed the valid attempt")
		}
		if _, err := subscriptionActionFixture(t, server, ProviderChatGPTSubscription, "oauth.start", "other-administrator", map[string]any{"registration_id": registrationID}); AsHTTPError(err).Code != "subscription_oauth_registration_invalid" {
			t.Fatal("another administrator resumed the selected registration")
		}
	}
}

func TestChatGPTSubscriptionRegistrationAutomaticCallbackRetainsClientBeforePolling(t *testing.T) {
	server, store, initial := chatGPTCallbackFixture(t, "http://127.0.0.1:8080", true)
	if response := chatGPTCallbackRequest(server, initial, url.Values{"code": {"private-code"}, "client_id": {"issued-client"}}); response.Code != http.StatusOK {
		t.Fatal("callback failed")
	}
	payload := map[string]any{"session_id": initial["session_id"], "state": initial["state"]}
	if _, err := subscriptionActionFixture(t, server, ProviderChatGPTSubscription, "oauth.cancel", "administrator", payload); err != nil {
		t.Fatal(err)
	}
	registration, err := store.chatGPTSubscriptionRegistration(context.Background(), "administrator", initial["registration_id"].(string))
	if err != nil || registration.ClientID != "issued-client" {
		t.Fatal("cancelling the pending exchange discarded the issued client")
	}
	if _, exists, _ := store.GetProviderAccountOAuthSessionByState(initial["state"].(string)); exists {
		t.Fatal("cancellation retained the ephemeral authorization code")
	}
}

func TestChatGPTSubscriptionRegistrationReauthorizationUsesStoredAccount(t *testing.T) {
	store := NewMemoryStore()
	server := New(store)
	provider, err := store.CreateProvider(Provider{Name: "ChatGPT registration fixture", Type: ProviderChatGPTSubscription, Status: StatusActive})
	if err != nil {
		t.Fatal(err)
	}
	resource := ProviderResource{ID: "existing-chatgpt-account", ProviderID: provider.ID, ResourceType: ProviderResourceChatGPTSubscription, Options: map[string]string{"user_id": "stored-subject"}, CredentialBlob: store.encryptProviderResourceCredentialBlob(ProviderResourceCredentials{ClientID: "stored-client", UserID: "stored-subject", AccessToken: "private-access", RefreshToken: "private-refresh"})}
	if err := store.db.Create(&resource).Error; err != nil {
		t.Fatal(err)
	}
	started := chatGPTRegistrationStart(t, server, map[string]any{"resource_id": resource.ID})
	authorize, _ := url.Parse(started["auth_url"].(string))
	if authorize.Query().Get("client_id") != "stored-client" || authorize.Query().Has("agent_name_hint") {
		t.Fatal("saved-account reauthorization attempted a new client registration")
	}
	registration, err := store.chatGPTSubscriptionRegistration(context.Background(), "administrator", started["registration_id"].(string))
	if err != nil || registration.Subject != "stored-subject" {
		t.Fatal("saved account identity was not bound to the pending registration")
	}
	if err := store.bindChatGPTSubscriptionRegistration(context.Background(), "administrator", registration.ID, registration.HostID, "stored-client", "different-subject"); AsHTTPError(err).Code != "subscription_oauth_identity_mismatch" {
		t.Fatal("saved account identity was replaced")
	}
	var current ProviderResource
	if err := store.db.First(&current, "id = ?", resource.ID).Error; err != nil || current.CredentialBlob != resource.CredentialBlob {
		t.Fatal("starting reauthorization changed the saved account credentials")
	}
	if err := store.db.Model(&resource).Update("resource_type", "other_account").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := subscriptionActionFixture(t, server, ProviderChatGPTSubscription, "oauth.start", "administrator", map[string]any{"resource_id": resource.ID}); AsHTTPError(err).Code != "subscription_oauth_registration_invalid" {
		t.Fatal("saved-account reauthorization accepted an unrelated resource type")
	}
}

func TestChatGPTSubscriptionRegistrationRejectsNoncanonicalRedirects(t *testing.T) {
	for _, redirect := range []string{"http://localhost:1455/auth/callback", "http://[::1]:1455/auth/callback", "http://127.0.0.1:1455/callback", "http://127.0.0.1:1455/auth/callback?next=elsewhere"} {
		if _, err := ChatGPTSubscriptionAuthorizeURL(redirect, "state", "nonce", strings.Repeat("v", 64), "urn:uuid:fixture-host", ""); err == nil {
			t.Fatal("authorization accepted a redirect outside the registered loopback contract")
		}
	}
}

func TestChatGPTSubscriptionRegistrationRecoversLegacySavedSubject(t *testing.T) {
	store := NewMemoryStore()
	server := New(store)
	provider, err := store.CreateProvider(Provider{Name: "Legacy ChatGPT account", Type: ProviderChatGPTSubscription, Status: StatusActive})
	if err != nil {
		t.Fatal(err)
	}
	claims, _ := json.Marshal(map[string]any{"iss": "https://auth.openai.com", "aud": []string{"stored-client"}, "sub": "stored-subject", "exp": time.Now().Add(-time.Hour).Unix()})
	credentials := ProviderResourceCredentials{ClientID: "stored-client", IDToken: "header." + base64.RawURLEncoding.EncodeToString(claims) + ".signature", AccessToken: "private-access", RefreshToken: "private-refresh"}
	resource := ProviderResource{ID: "legacy-chatgpt-account", ProviderID: provider.ID, ResourceType: ProviderResourceChatGPTSubscription, CredentialBlob: store.encryptProviderResourceCredentialBlob(credentials)}
	if err := store.db.Create(&resource).Error; err != nil {
		t.Fatal(err)
	}
	started := chatGPTRegistrationStart(t, server, map[string]any{"resource_id": resource.ID})
	registration, err := store.chatGPTSubscriptionRegistration(context.Background(), "administrator", started["registration_id"].(string))
	if err != nil || registration.Subject != "stored-subject" || registration.ClientID != "stored-client" {
		t.Fatal("legacy saved ID token did not bind the returning account")
	}
	if err := store.bindChatGPTSubscriptionRegistration(context.Background(), "administrator", registration.ID, registration.HostID, "stored-client", "other-subject"); AsHTTPError(err).Code != "subscription_oauth_identity_mismatch" {
		t.Fatal("legacy subject recovery accepted another returning identity")
	}
	var current ProviderResource
	if err := store.db.First(&current, "id = ?", resource.ID).Error; err != nil || current.CredentialBlob != resource.CredentialBlob {
		t.Fatal("legacy identity recovery replaced the saved credentials")
	}
}

func TestChatGPTSubscriptionRegistrationStoredSubjectRejectsUnboundClaims(t *testing.T) {
	for _, claims := range []map[string]any{
		{"iss": "https://other.example", "aud": "stored-client", "sub": "subject"},
		{"iss": "https://auth.openai.com", "aud": "other-client", "sub": "subject"},
		{"iss": "https://auth.openai.com", "aud": "stored-client"},
		{"iss": "https://auth.openai.com", "aud": "stored-client", "sub": 123},
		{"iss": "https://auth.openai.com", "aud": "stored-client", "sub": " "},
	} {
		encoded, _ := json.Marshal(claims)
		credentials := ProviderResourceCredentials{ClientID: "stored-client", IDToken: "header." + base64.RawURLEncoding.EncodeToString(encoded) + ".signature"}
		if chatGPTSubscriptionStoredSubject(credentials) != "" {
			t.Fatal("legacy identity recovery accepted an unrelated or absent subject")
		}
	}
	for _, token := range []string{"", "malformed", "header.%%.signature", strings.Repeat("x", 64<<10+1)} {
		if chatGPTSubscriptionStoredSubject(ProviderResourceCredentials{ClientID: "stored-client", IDToken: token}) != "" {
			t.Fatal("legacy identity recovery accepted malformed token data")
		}
	}
}
