package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestCopilotSubscriptionEnterpriseHostValidation(t *testing.T) {
	for input, expected := range map[string]string{"": "", "github.com": "", "https://GITHUB.com/": "", "Acme.ghe.com": "acme.ghe.com", "https://acme-1.ghe.com/": "acme-1.ghe.com"} {
		if got, err := normalizeCopilotSubscriptionHost(input); err != nil || got != expected {
			t.Errorf("host %q: got %q, %v", input, got, err)
		}
	}
	for _, input := range []string{"ghe.com", "a.b.ghe.com", "github.example", "https://acme.ghe.com/path", "http://acme.ghe.com", "https://user@acme.ghe.com", "https://acme.ghe.com:443", "acme.ghe.com.evil.test", "-acme.ghe.com", "https://acme.ghe.com?redirect=evil"} {
		if _, err := normalizeCopilotSubscriptionHost(input); err == nil {
			t.Errorf("accepted untrusted host %q", input)
		}
	}
}

func TestCopilotSubscriptionEnterpriseOAuthBindsAccountHost(t *testing.T) {
	store := NewMemoryStore()
	server := New(store)
	adapter, ok := resolveTypedAdapter[*CopilotSubscriptionAdapter](server.adapterRegistry, ProviderGitHubCopilotSubscription)
	if !ok {
		t.Fatal("Copilot adapter missing")
	}
	seen := map[string]int{}
	client := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		seen[r.URL.Host+r.URL.Path]++
		var body string
		switch r.URL.Host + r.URL.Path {
		case "acme.ghe.com/login/device/code":
			body = `{"device_code":"device-secret","user_code":"ABCD-EFGH","verification_uri":"https://acme.ghe.com/login/device","expires_in":900,"interval":5}`
		case "acme.ghe.com/login/oauth/access_token":
			if err := r.ParseForm(); err != nil || r.Form.Get("device_code") != "device-secret" {
				t.Error("device secret was not bound to the enterprise")
			}
			body = `{"access_token":"enterprise-account","scope":"read:user"}`
		case "api.acme.ghe.com/copilot_internal/v2/token":
			if r.Header.Get("Authorization") != "token enterprise-account" {
				t.Error("wrong enterprise account token")
			}
			body = fmt.Sprintf(`{"token":"enterprise-session","expires_at":%d}`, time.Now().Add(time.Hour).Unix())
		case "copilot-api.acme.ghe.com/models":
			if r.Header.Get("Authorization") != "Bearer enterprise-session" {
				t.Error("wrong enterprise session token")
			}
			body = `{"data":[]}`
		default:
			t.Errorf("credentials reached an unexpected endpoint: %s", r.URL)
			body = `{}`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	adapter.Client, adapter.CredentialRefreshClient = client, client
	started, err := subscriptionActionFixture(t, server, ProviderGitHubCopilotSubscription, "oauth.start", "administrator", map[string]any{"inputs": map[string]string{"github_host": "https://acme.ghe.com"}})
	if err != nil {
		t.Fatal(err)
	}
	data := started.Data.(map[string]any)
	if data["auth_url"] != "https://acme.ghe.com/login/device" {
		t.Fatalf("incorrect enterprise device page: %v", data)
	}
	session, found, err := store.GetProviderAccountOAuthSessionByState(data["state"].(string))
	if err != nil || !found {
		t.Fatal("OAuth session missing")
	}
	var secret subscriptionOAuthSecret
	if json.Unmarshal([]byte(session.CodeVerifier), &secret) != nil || secret.GitHubHost != "acme.ghe.com" {
		t.Fatal("enterprise host was not saved in the authorization session")
	}
	secret.NextPollAt = time.Now().Add(-time.Second)
	encoded, _ := json.Marshal(secret)
	session.CodeVerifier = string(encoded)
	if err = store.SaveProviderAccountOAuthSession(session); err != nil {
		t.Fatal(err)
	}
	result, err := subscriptionActionFixture(t, server, ProviderGitHubCopilotSubscription, "oauth.exchange", "administrator", map[string]any{"session_id": data["session_id"], "state": data["state"], "github_host": "attacker.ghe.com"})
	if err != nil {
		t.Fatal(err)
	}
	credentials := result.Data.(subscriptionOAuthExchangeResult)
	if credentials.AccessToken != "enterprise-account" || credentials.SessionJSON != `{"github_host":"acme.ghe.com"}` || credentials.Catalog == nil || len(credentials.Catalog.Models) != 1 {
		t.Fatalf("enterprise credentials or catalog missing: %+v", credentials.Catalog)
	}
	for _, endpoint := range []string{"acme.ghe.com/login/device/code", "acme.ghe.com/login/oauth/access_token", "api.acme.ghe.com/copilot_internal/v2/token", "copilot-api.acme.ghe.com/models"} {
		if seen[endpoint] != 1 {
			t.Errorf("enterprise endpoint %s called %d times", endpoint, seen[endpoint])
		}
	}
}

func TestCopilotSubscriptionEnterpriseRejectsCrossAccountEndpoints(t *testing.T) {
	for _, endpoint := range []string{"https://copilot-api.other.ghe.com", "https://api.githubcopilot.com", "https://api.acme.ghe.com"} {
		t.Run(endpoint, func(t *testing.T) {
			client := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "api.acme.ghe.com" {
					t.Errorf("unexpected credential destination: %s", r.URL)
				}
				body := fmt.Sprintf(`{"token":"session","expires_at":%d,"endpoints":{"api":%q}}`, time.Now().Add(time.Hour).Unix(), endpoint)
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			adapter := &CopilotSubscriptionAdapter{CredentialRefreshClient: client}
			_, err := adapter.session(context.Background(), ProviderResourceCredentials{AccessToken: "account", SessionJSON: `{"github_host":"acme.ghe.com"}`}, false)
			if err == nil || AsHTTPError(err).Code != "subscription_endpoint_not_allowed" {
				t.Fatalf("cross-enterprise endpoint accepted: %v", err)
			}
		})
	}
}
