package server

import (
	"net/url"
	"testing"
)

func assertProviderOAuthCallbackRedirect(t testing.TB, redirect *url.URL, payload providerAccountOAuthGenerateResponse) {
	t.Helper()
	if redirect.Query().Get("provider_account_oauth") != "" || redirect.Query().Get("code") != "" {
		t.Fatalf("sensitive OAuth values leaked into query string: %s", redirect)
	}
	fragment, err := url.ParseQuery(redirect.Fragment)
	if err != nil {
		t.Fatalf("parse OAuth callback fragment: %v", err)
	}
	if fragment.Get("provider_account_oauth") != "1" ||
		fragment.Get("provider_account_oauth_session_id") != payload.SessionID ||
		fragment.Get("provider_account_oauth_state") != payload.State ||
		fragment.Get("code") != "oauth-code" {
		t.Fatalf("unexpected OAuth callback fragment: %s", redirect.Fragment)
	}
}

func TestOAuthRedirectWithFragmentRemovesOAuthValuesFromQuery(t *testing.T) {
	redirect := oauthRedirectWithFragment(
		"http://localhost:3001/providers?keep=1&provider_account_oauth=1&code=forged",
		url.Values{
			"provider_account_oauth": {"1"},
			"code":                   {"authentic"},
		},
	)
	parsed, err := url.Parse(redirect)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("keep") != "1" {
		t.Fatalf("unrelated query parameter was removed: %s", redirect)
	}
	if parsed.Query().Get("provider_account_oauth") != "" || parsed.Query().Get("code") != "" {
		t.Fatalf("OAuth values remained in query string: %s", redirect)
	}
	fragment, err := url.ParseQuery(parsed.Fragment)
	if err != nil {
		t.Fatal(err)
	}
	if fragment.Get("provider_account_oauth") != "1" || fragment.Get("code") != "authentic" {
		t.Fatalf("unexpected OAuth fragment: %s", parsed.Fragment)
	}
}
